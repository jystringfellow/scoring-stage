package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jystringfellow/scoring-stage/internal/kits"
)

const (
	DefaultBase      = "docker.io/sbx/shell:1.1.0"
	DefaultAWSRegion = "us-east-1"
	BrokerService    = "stage-broker"
)

var DefaultAgents = []string{
	"docker.io/sbx/claude-mixin:2.1.319",
	"docker.io/sbx/codex-mixin:0.167.0",
	"docker.io/sbx/copilot-mixin:1.0.86",
}

var CopilotHosts = []string{
	"api.githubcopilot.com",
	"api.individual.githubcopilot.com",
	"api.business.githubcopilot.com",
	"api.enterprise.githubcopilot.com",
	"copilot.github.com",
}

func defaultBindings() map[string]any {
	return map[string]any{
		"github":    domains("apiKey", "api.github.com", "github.com"),
		"copilot":   domains("apiKey", CopilotHosts...),
		"anthropic": domains("oauth", "platform.claude.com", "api.anthropic.com"),
		"openai":    domains("oauth", "auth.openai.com", "chatgpt.com"),
	}
}

func brokerBinding() map[string]any {
	return domains("apiKey", "localhost")
}

func domains(kind string, hosts ...string) map[string]any {
	list := make([]any, len(hosts))
	for i, host := range hosts {
		list[i] = host
	}
	return map[string]any{kind: map[string]any{"domains": list}}
}

var (
	userKeys = []string{"packs", "base", "agents", "kits", "aws", "secrets", "bindings", "ship"}
	packKeys = []string{"name", "description", "base", "kits", "aws", "secrets", "bindings", "repos"}
	repoKeys = []string{"match", "kits", "aws"}
	awsKeys  = []string{"profiles", "region"}
	shipKeys = []string{"pr_footer"}
)

type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...any) error {
	return &Error{fmt.Sprintf(format, args...)}
}

type Kit struct {
	Source string
	Args   map[string]any
}

func (k Kit) IsLocal() bool { return strings.HasPrefix(k.Source, "/") }

func (k Kit) Equal(other Kit) bool {
	if k.Source != other.Source {
		return false
	}
	if len(k.Args) == 0 && len(other.Args) == 0 {
		return true
	}
	return reflect.DeepEqual(k.Args, other.Args)
}

type Composition struct {
	Base        Kit
	Kits        []Kit
	AWSProfiles []string
	AWSRegion   string
	Secrets     map[string]any
	Bindings    map[string]any
	Packs       []string
}

func (c *Composition) AllKits() []Kit {
	return append([]Kit{c.Base}, c.Kits...)
}

type aws struct {
	profiles  []string
	region    string
	hasRegion bool
}

type repo struct {
	patterns []string
	kits     []any
	aws      aws
}

type Pack struct {
	Name     string
	Path     string
	base     any
	kits     []any
	aws      aws
	secrets  map[string]any
	bindings map[string]any
	repos    []repo
}

type Settings struct {
	Home     string
	Packs    []Pack
	base     any
	agents   []any
	hasAgent bool
	kits     []any
	aws      aws
	secrets  map[string]any
	bindings map[string]any
	prFooter string
}

func (s *Settings) ReadonlyToken() string {
	return filepath.Join(s.Home, "secrets", "github-readonly-token")
}

func (s *Settings) PRFooter() string { return s.prFooter }

func (s *Settings) CoreKits() string { return kits.Dir(s.Home) }

func StageHome() string {
	home := os.Getenv("STAGE_HOME")
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".scoring-stage")
		}
	}
	return Resolve(ExpandUser(home))
}

func LoadSettings(home string, extraPacks []string) (*Settings, error) {
	path := filepath.Join(home, "config.toml")
	data := map[string]any{}
	if _, err := os.Stat(path); err == nil {
		if data, err = readTOML(path); err != nil {
			return nil, err
		}
	}
	if err := checkKeys(data, userKeys, path); err != nil {
		return nil, err
	}
	if err := checkTables(data, path); err != nil {
		return nil, err
	}
	settings := &Settings{Home: home, base: data["base"]}
	var err error
	if settings.agents, settings.hasAgent = asList(data["agents"]); data["agents"] != nil && !settings.hasAgent {
		return nil, errorf("%s: agents must be a list", path)
	}
	if settings.kits, err = optionalList(data, "kits", path); err != nil {
		return nil, err
	}
	if settings.aws, err = decodeAWS(data["aws"], path+" [aws]"); err != nil {
		return nil, err
	}
	if settings.secrets, err = optionalTable(data, "secrets", path); err != nil {
		return nil, err
	}
	if settings.bindings, err = optionalTable(data, "bindings", path); err != nil {
		return nil, err
	}
	if ship, ok := data["ship"].(map[string]any); ok {
		if footer, present := ship["pr_footer"]; present {
			text, ok := footer.(string)
			if !ok {
				return nil, errorf("%s [ship]: pr_footer must be a string", path)
			}
			settings.prFooter = text
		}
	}
	packPaths, err := optionalStrings(data, "packs", path)
	if err != nil {
		return nil, err
	}
	for _, packPath := range append(packPaths, extraPacks...) {
		pack, err := LoadPack(ExpandUser(packPath))
		if err != nil {
			return nil, err
		}
		settings.Packs = append(settings.Packs, *pack)
	}
	return settings, nil
}

func LoadPack(path string) (*Pack, error) {
	path = Resolve(path)
	manifest := filepath.Join(path, "pack.toml")
	if info, err := os.Stat(manifest); err != nil || !info.Mode().IsRegular() {
		return nil, errorf("%s is not a pack: no pack.toml", path)
	}
	data, err := readTOML(manifest)
	if err != nil {
		return nil, err
	}
	if err := checkKeys(data, packKeys, manifest); err != nil {
		return nil, err
	}
	if err := checkTables(data, manifest); err != nil {
		return nil, err
	}
	name, _ := data["name"].(string)
	if name == "" {
		return nil, errorf("%s: name is required", manifest)
	}
	pack := &Pack{Name: name, Path: path, base: data["base"]}
	if pack.kits, err = optionalList(data, "kits", manifest); err != nil {
		return nil, err
	}
	if pack.aws, err = decodeAWS(data["aws"], manifest+" [aws]"); err != nil {
		return nil, err
	}
	if pack.secrets, err = optionalTable(data, "secrets", manifest); err != nil {
		return nil, err
	}
	if pack.bindings, err = optionalTable(data, "bindings", manifest); err != nil {
		return nil, err
	}
	repos, ok := asList(data["repos"])
	if data["repos"] != nil && !ok {
		return nil, errorf("%s: repos must be a list of tables", manifest)
	}
	for index, entry := range repos {
		where := fmt.Sprintf("%s: repos[%d]", manifest, index)
		table, ok := entry.(map[string]any)
		if !ok {
			return nil, errorf("%s: must be a table", where)
		}
		if err := checkKeys(table, repoKeys, where); err != nil {
			return nil, err
		}
		var r repo
		switch match := table["match"].(type) {
		case string:
			r.patterns = []string{match}
		default:
			patterns, err := asStrings(match)
			if err != nil || len(patterns) == 0 {
				return nil, errorf("%s: match must be a pattern or a list of patterns", where)
			}
			r.patterns = patterns
		}
		if r.kits, err = optionalList(table, "kits", where); err != nil {
			return nil, err
		}
		if table["aws"] != nil {
			if r.aws, err = decodeAWS(table["aws"], where+".aws"); err != nil {
				return nil, err
			}
		}
		pack.repos = append(pack.repos, r)
	}
	return pack, nil
}

func Compose(settings *Settings, repoNames []string, gh string) (*Composition, error) {
	var base *Kit
	var baseOwner, region, regionOwner string
	var kitList []Kit
	var profiles []string
	secrets := map[string]any{}
	bindings := defaultBindings()
	secretOwner := map[string]string{}
	bindingOwner := map[string]string{}

	addProfiles := func(names []string) {
		for _, name := range names {
			if !contains(profiles, name) {
				profiles = append(profiles, name)
			}
		}
	}
	addAWS := func(a aws, owner string) error {
		addProfiles(a.profiles)
		if a.hasRegion {
			if regionOwner != "" && a.region != region {
				return errorf("%s sets AWS region %s, but %s set %s", owner, a.region, regionOwner, region)
			}
			region, regionOwner = a.region, owner
		}
		return nil
	}
	addKits := func(entries []any, dir, owner string) error {
		for _, entry := range entries {
			kit, err := parseKit(entry, dir, owner)
			if err != nil {
				return err
			}
			kitList = append(kitList, kit)
		}
		return nil
	}

	for _, pack := range settings.Packs {
		owner := "pack " + pack.Name
		if pack.base != nil {
			candidate, err := parseKit(pack.base, pack.Path, owner)
			if err != nil {
				return nil, err
			}
			if base != nil && !candidate.Equal(*base) {
				return nil, errorf("%s sets base %s, but %s set %s", owner, candidate.Source, baseOwner, base.Source)
			}
			base, baseOwner = &candidate, owner
		}
		if err := addKits(pack.kits, pack.Path, owner); err != nil {
			return nil, err
		}
		if err := addAWS(pack.aws, owner); err != nil {
			return nil, err
		}
		for _, service := range sortedKeys(pack.secrets) {
			spec, err := packSecret(pack.secrets[service], pack.Path, owner)
			if err != nil {
				return nil, err
			}
			if err := mergeOnce(secrets, secretOwner, service, spec, owner, "secret"); err != nil {
				return nil, err
			}
		}
		for _, service := range sortedKeys(pack.bindings) {
			if err := mergeOnce(bindings, bindingOwner, service, pack.bindings[service], owner, "binding"); err != nil {
				return nil, err
			}
		}
		for _, r := range pack.repos {
			if !matchesAny(repoNames, r.patterns) {
				continue
			}
			repoOwner := fmt.Sprintf("%s repo %s", owner, r.patterns[0])
			if err := addKits(r.kits, pack.Path, repoOwner); err != nil {
				return nil, err
			}
			if err := addAWS(r.aws, repoOwner); err != nil {
				return nil, err
			}
		}
	}

	if settings.base != nil {
		candidate, err := parseKit(settings.base, settings.Home, "config")
		if err != nil {
			return nil, err
		}
		base = &candidate
	}
	agentEntries := settings.agents
	if !settings.hasAgent {
		for _, agent := range DefaultAgents {
			agentEntries = append(agentEntries, agent)
		}
	}
	var agents []Kit
	for _, entry := range agentEntries {
		kit, err := parseKit(entry, settings.Home, "config")
		if err != nil {
			return nil, err
		}
		agents = append(agents, kit)
	}
	addProfiles(settings.aws.profiles)
	if settings.aws.hasRegion {
		region = settings.aws.region
	}
	if region == "" {
		region = DefaultAWSRegion
	}

	core := []Kit{{Source: filepath.Join(settings.CoreKits(), "stage")}}
	if len(profiles) > 0 {
		core = append(core, Kit{
			Source: filepath.Join(settings.CoreKits(), "aws-broker"),
			Args:   map[string]any{"profiles": strings.Join(profiles, " "), "region": region},
		})
		bindings[BrokerService] = brokerBinding()
	}
	var userKits []Kit
	for _, entry := range settings.kits {
		kit, err := parseKit(entry, settings.Home, "config")
		if err != nil {
			return nil, err
		}
		userKits = append(userKits, kit)
	}

	if gh != "" {
		if _, set := secrets["copilot"]; !set {
			secrets["copilot"] = map[string]any{"command": ShellQuote(gh) + " auth token"}
		}
	}
	if info, err := os.Stat(settings.ReadonlyToken()); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		if _, set := secrets["github"]; !set {
			secrets["github"] = map[string]any{"command": "/bin/cat " + ShellQuote(settings.ReadonlyToken())}
		}
	}
	for service, spec := range settings.secrets {
		secrets[service] = spec
	}
	for service, spec := range settings.bindings {
		bindings[service] = spec
	}

	composition := &Composition{
		Base:        Kit{Source: DefaultBase},
		AWSProfiles: profiles,
		AWSRegion:   region,
		Secrets:     secrets,
		Bindings:    bindings,
	}
	if base != nil {
		composition.Base = *base
	}
	composition.Kits = append(append(append(agents, core...), kitList...), userKits...)
	for _, pack := range settings.Packs {
		composition.Packs = append(composition.Packs, pack.Name)
	}
	return composition, nil
}

func parseKit(entry any, baseDir, owner string) (Kit, error) {
	var kit Kit
	switch typed := entry.(type) {
	case string:
		kit.Source = typed
	case map[string]any:
		source, ok := typed["source"].(string)
		args, argsOK := typed["args"].(map[string]any)
		if typed["args"] == nil {
			argsOK = true
		}
		if !ok || !argsOK || len(unknownKeys(typed, []string{"source", "args"})) > 0 {
			return kit, errorf("%s: a kit is a reference string or {source, args}, not %v", owner, entry)
		}
		kit.Source = source
		if len(args) > 0 {
			kit.Args = make(map[string]any, len(args))
			for k, v := range args {
				kit.Args[k] = v
			}
		}
	default:
		return kit, errorf("%s: a kit is a reference string or {source, args}, not %v", owner, entry)
	}
	switch {
	case strings.HasPrefix(kit.Source, "./"), strings.HasPrefix(kit.Source, "../"):
		kit.Source = Resolve(filepath.Join(baseDir, kit.Source))
	case strings.HasPrefix(kit.Source, "~"):
		kit.Source = Resolve(ExpandUser(kit.Source))
	}
	return kit, nil
}

func packSecret(spec any, packPath, owner string) (map[string]any, error) {
	table, ok := spec.(map[string]any)
	if !ok {
		return nil, errorf(`%s: a secret is a table such as {command = "..."}`, owner)
	}
	copied := make(map[string]any, len(table))
	for k, v := range table {
		copied[k] = v
	}
	if command, ok := copied["command"].(string); ok {
		copied["command"] = strings.ReplaceAll(command, "{pack}", ShellQuote(packPath))
	}
	return copied, nil
}

func mergeOnce(target map[string]any, owners map[string]string, key string, value any, owner, kind string) error {
	if previous, set := owners[key]; set && !reflect.DeepEqual(target[key], value) {
		return errorf("%s sets %s %s differently from %s", owner, kind, key, previous)
	}
	target[key] = value
	owners[key] = owner
	return nil
}

func matchesAny(names, patterns []string) bool {
	for _, pattern := range patterns {
		for _, name := range names {
			if Fnmatch(name, pattern) {
				return true
			}
		}
	}
	return false
}

func readTOML(path string) (map[string]any, error) {
	data := map[string]any{}
	if _, err := toml.DecodeFile(path, &data); err != nil {
		return nil, errorf("%s: %v", path, err)
	}
	return data, nil
}

func checkKeys(data map[string]any, allowed []string, where string) error {
	if unknown := unknownKeys(data, allowed); len(unknown) > 0 {
		return errorf("%s: unknown keys %s", where, strings.Join(unknown, ", "))
	}
	return nil
}

func unknownKeys(data map[string]any, allowed []string) []string {
	var unknown []string
	for key := range data {
		if !contains(allowed, key) {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func checkTables(data map[string]any, where string) error {
	for _, table := range []struct {
		key     string
		allowed []string
	}{{"aws", awsKeys}, {"ship", shipKeys}} {
		value, present := data[table.key]
		if !present {
			continue
		}
		typed, ok := value.(map[string]any)
		if !ok {
			return errorf("%s: %s must be a table", where, table.key)
		}
		if err := checkKeys(typed, table.allowed, fmt.Sprintf("%s [%s]", where, table.key)); err != nil {
			return err
		}
	}
	return nil
}

func decodeAWS(value any, where string) (aws, error) {
	var result aws
	if value == nil {
		return result, nil
	}
	table, ok := value.(map[string]any)
	if !ok {
		return result, errorf("%s: must be a table", where)
	}
	if err := checkKeys(table, awsKeys, where); err != nil {
		return result, err
	}
	if table["profiles"] != nil {
		profiles, err := asStrings(table["profiles"])
		if err != nil {
			return result, errorf("%s: profiles must be a list of strings", where)
		}
		result.profiles = profiles
	}
	if regionValue, present := table["region"]; present {
		region, ok := regionValue.(string)
		if !ok {
			return result, errorf("%s: region must be a string", where)
		}
		result.region, result.hasRegion = region, true
	}
	return result, nil
}

func optionalList(data map[string]any, key, where string) ([]any, error) {
	if data[key] == nil {
		return nil, nil
	}
	list, ok := asList(data[key])
	if !ok {
		return nil, errorf("%s: %s must be a list", where, key)
	}
	return list, nil
}

func optionalStrings(data map[string]any, key, where string) ([]string, error) {
	if data[key] == nil {
		return nil, nil
	}
	values, err := asStrings(data[key])
	if err != nil {
		return nil, errorf("%s: %s must be a list of strings", where, key)
	}
	return values, nil
}

func optionalTable(data map[string]any, key, where string) (map[string]any, error) {
	if data[key] == nil {
		return nil, nil
	}
	table, ok := data[key].(map[string]any)
	if !ok {
		return nil, errorf("%s: %s must be a table", where, key)
	}
	return table, nil
}

func asList(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []map[string]any:
		list := make([]any, len(typed))
		for i, table := range typed {
			list[i] = table
		}
		return list, true
	}
	return nil, false
}

func asStrings(value any) ([]string, error) {
	list, ok := asList(value)
	if !ok {
		return nil, errors.New("not a list")
	}
	values := make([]string, 0, len(list))
	for _, element := range list {
		text, ok := element.(string)
		if !ok {
			return nil, errors.New("not a string")
		}
		values = append(values, text)
	}
	return values, nil
}

func sortedKeys(table map[string]any) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contains(list []string, value string) bool {
	for _, element := range list {
		if element == value {
			return true
		}
	}
	return false
}

func FindGh() string {
	path, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	return path
}
