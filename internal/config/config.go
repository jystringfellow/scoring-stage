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
	userKeys      = []string{"books", "base", "agents", "copy_logins", "kits", "aws", "secrets", "bindings", "ship"}
	bookKeys      = []string{"name", "description", "base", "agents", "kits", "aws", "secrets", "bindings", "repos"}
	repoMatchKeys = []string{"match", "agents", "kits", "aws"}
	awsKeys       = []string{"profiles", "region", "default_profile"}
	shipKeys      = []string{"pr_footer"}
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
	Base              Kit
	Agents            []string
	Kits              []Kit
	AWSProfiles       []string
	AWSRegion         string
	AWSDefaultProfile string
	Secrets           map[string]any
	Bindings          map[string]any
	Books             []string
	RepoKits          []Kit
}

func (c *Composition) AllKits() []Kit {
	return append([]Kit{c.Base}, c.Kits...)
}

type aws struct {
	profiles       []string
	region         string
	hasRegion      bool
	defaultProfile string
}

type repo struct {
	patterns []string
	agents   []any
	kits     []any
	aws      aws
}

type Book struct {
	Name     string
	Path     string
	base     any
	agents   []any
	kits     []any
	aws      aws
	secrets  map[string]any
	bindings map[string]any
	repos    []repo
}

type Settings struct {
	Home       string
	Books      []Book
	base       any
	agents     []any
	kits       []any
	aws        aws
	secrets    map[string]any
	bindings   map[string]any
	prFooter   string
	copyLogins []string
}

func (s *Settings) ReadonlyToken() string {
	return filepath.Join(s.Home, "secrets", "github-readonly-token")
}

func (s *Settings) PRFooter() string { return s.prFooter }

func (s *Settings) CopiesLogin(agent string) bool { return contains(s.copyLogins, agent) }

// SavedLogin is the host copy of one of an agent's in-sandbox login files.
func (s *Settings) SavedLogin(agent string, file LoginFile) string {
	return filepath.Join(s.Home, "logins", agent, filepath.Base(file.Path))
}

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

func LoadSettings(home string) (*Settings, error) {
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
	if settings.agents, err = optionalList(data, "agents", path); err != nil {
		return nil, err
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
	if settings.copyLogins, err = optionalStrings(data, "copy_logins", path); err != nil {
		return nil, err
	}
	for _, agent := range settings.copyLogins {
		if !contains(copyableAgents(), agent) {
			return nil, errorf("%s: copy_logins: %s cannot be copied (copyable: %s); claude, codex and copilot sign in through the proxy", path, agent, strings.Join(copyableAgents(), ", "))
		}
	}
	bookPaths, err := optionalStrings(data, "books", path)
	if err != nil {
		return nil, err
	}
	installed := map[string]string{}
	for _, bookPath := range bookPaths {
		book, err := LoadBook(ExpandUser(bookPath))
		if err != nil {
			return nil, err
		}
		if other, taken := installed[book.Name]; taken {
			return nil, errorf("%s: books %s and %s are both named %s", path, other, book.Path, book.Name)
		}
		installed[book.Name] = book.Path
		settings.Books = append(settings.Books, *book)
	}
	return settings, nil
}

func LoadBook(path string) (*Book, error) {
	path = Resolve(path)
	manifest := filepath.Join(path, "book.toml")
	if info, err := os.Stat(manifest); err != nil || !info.Mode().IsRegular() {
		return nil, errorf("%s is not a book: no book.toml", path)
	}
	data, err := readTOML(manifest)
	if err != nil {
		return nil, err
	}
	if err := checkKeys(data, bookKeys, manifest); err != nil {
		return nil, err
	}
	if err := checkTables(data, manifest); err != nil {
		return nil, err
	}
	name, _ := data["name"].(string)
	if name == "" {
		return nil, errorf("%s: name is required", manifest)
	}
	book := &Book{Name: name, Path: path, base: data["base"]}
	if book.agents, err = optionalList(data, "agents", manifest); err != nil {
		return nil, err
	}
	if book.kits, err = optionalList(data, "kits", manifest); err != nil {
		return nil, err
	}
	if book.aws, err = decodeAWS(data["aws"], manifest+" [aws]"); err != nil {
		return nil, err
	}
	if book.secrets, err = optionalTable(data, "secrets", manifest); err != nil {
		return nil, err
	}
	if book.bindings, err = optionalTable(data, "bindings", manifest); err != nil {
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
		if err := checkKeys(table, repoMatchKeys, where); err != nil {
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
		if r.agents, err = optionalList(table, "agents", where); err != nil {
			return nil, err
		}
		if r.kits, err = optionalList(table, "kits", where); err != nil {
			return nil, err
		}
		if table["aws"] != nil {
			if r.aws, err = decodeAWS(table["aws"], where+".aws"); err != nil {
				return nil, err
			}
		}
		book.repos = append(book.repos, r)
	}
	return book, nil
}

// Selection is what one sandbox asks for beyond the user's own settings.
type Selection struct {
	RepoNames  []string
	Agents     []string
	Books      []string
	Repo       *RepoConfig
	Gh         string
	AWSProfile string
}

func Compose(settings *Settings, selection Selection) (*Composition, error) {
	active, err := activeBooks(settings, selection)
	if err != nil {
		return nil, err
	}
	var base *Kit
	var baseOwner, region, regionOwner, defaultProfile, defaultProfileOwner string
	var agentKits, bookKits, repoKits, userKits []Kit
	var profiles, agentNames []string
	secrets := map[string]any{}
	bindings := map[string]any{"github": domains("apiKey", "api.github.com", "github.com")}
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
		if a.defaultProfile != "" {
			if defaultProfileOwner != "" && a.defaultProfile != defaultProfile {
				return errorf("%s sets AWS default profile %s, but %s set %s", owner, a.defaultProfile, defaultProfileOwner, defaultProfile)
			}
			defaultProfile, defaultProfileOwner = a.defaultProfile, owner
		}
		return nil
	}
	addKits := func(target *[]Kit, entries []any, dir, owner string) error {
		for _, entry := range entries {
			kit, err := parseKit(entry, dir, owner)
			if err != nil {
				return err
			}
			*target = append(*target, kit)
		}
		return nil
	}
	addAgents := func(entries []any, dir, owner string) error {
		for _, entry := range entries {
			kit, err := parseAgent(entry, dir, owner)
			if err != nil {
				return err
			}
			if containsKit(agentKits, kit) {
				continue
			}
			agentKits = append(agentKits, kit)
			if name, known := agentFor(kit.Source); known && !contains(agentNames, name) {
				agentNames = append(agentNames, name)
			}
		}
		return nil
	}

	if err := addAgents(settings.agents, settings.Home, "config"); err != nil {
		return nil, err
	}
	for _, book := range active {
		owner := "book " + book.Name
		if book.base != nil {
			candidate, err := parseKit(book.base, book.Path, owner)
			if err != nil {
				return nil, err
			}
			if base != nil && !candidate.Equal(*base) {
				return nil, errorf("%s sets base %s, but %s set %s", owner, candidate.Source, baseOwner, base.Source)
			}
			base, baseOwner = &candidate, owner
		}
		if err := addAgents(book.agents, book.Path, owner); err != nil {
			return nil, err
		}
		if err := addKits(&bookKits, book.kits, book.Path, owner); err != nil {
			return nil, err
		}
		if err := addAWS(book.aws, owner); err != nil {
			return nil, err
		}
		for _, service := range sortedKeys(book.secrets) {
			spec, err := bookSecret(book.secrets[service], book.Path, owner)
			if err != nil {
				return nil, err
			}
			if err := mergeOnce(secrets, secretOwner, service, spec, owner, "secret"); err != nil {
				return nil, err
			}
		}
		for _, service := range sortedKeys(book.bindings) {
			if err := mergeOnce(bindings, bindingOwner, service, book.bindings[service], owner, "binding"); err != nil {
				return nil, err
			}
		}
		for _, r := range book.repos {
			if !matchesAny(selection.RepoNames, r.patterns) {
				continue
			}
			repoOwner := fmt.Sprintf("%s repo %s", owner, r.patterns[0])
			if err := addAgents(r.agents, book.Path, repoOwner); err != nil {
				return nil, err
			}
			if err := addKits(&bookKits, r.kits, book.Path, repoOwner); err != nil {
				return nil, err
			}
			if err := addAWS(r.aws, repoOwner); err != nil {
				return nil, err
			}
		}
	}
	if repo := selection.Repo; repo != nil {
		for _, entry := range repo.agents {
			if name, ok := entry.(string); !ok || !looksLikeName(name) {
				return nil, errorf("%s: agents are named (%s), not given as kits; a repository's own kits go in kits", repo.Path, strings.Join(AgentNames(), ", "))
			}
		}
		if err := addAgents(repo.agents, repo.dir, repo.Path); err != nil {
			return nil, err
		}
		if err := addKits(&repoKits, repo.kits, repo.dir, repo.Path); err != nil {
			return nil, err
		}
		root := Resolve(repo.dir)
		for _, kit := range repoKits {
			if !kit.IsLocal() {
				continue
			}
			if relative, err := filepath.Rel(root, kit.Source); err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
				return nil, errorf("%s: kit %s is outside the repository; a repository's local kits live in the repository", repo.Path, kit.Source)
			}
		}
	}
	cliAgents := make([]any, len(selection.Agents))
	for i, agent := range selection.Agents {
		cliAgents[i] = agent
	}
	if err := addAgents(cliAgents, ".", "--agent"); err != nil {
		return nil, err
	}

	if settings.base != nil {
		candidate, err := parseKit(settings.base, settings.Home, "config")
		if err != nil {
			return nil, err
		}
		base = &candidate
	}
	addProfiles(settings.aws.profiles)
	if settings.aws.hasRegion {
		region = settings.aws.region
	}
	if region == "" {
		region = DefaultAWSRegion
	}
	if settings.aws.defaultProfile != "" {
		defaultProfile = settings.aws.defaultProfile
	}
	if selection.AWSProfile != "" {
		addProfiles([]string{selection.AWSProfile})
		defaultProfile = selection.AWSProfile
	}
	if defaultProfile == "" && len(profiles) == 1 {
		defaultProfile = profiles[0]
	}
	if defaultProfile != "" && !contains(profiles, defaultProfile) {
		return nil, errorf("AWS default profile %s is not a granted profile (%s)", defaultProfile, strings.Join(profiles, " "))
	}

	core := []Kit{{Source: filepath.Join(settings.CoreKits(), "stage")}}
	if len(profiles) > 0 {
		brokerArgs := map[string]any{"profiles": strings.Join(profiles, " "), "region": region}
		if defaultProfile != "" {
			brokerArgs["default_profile"] = defaultProfile
		}
		core = append(core, Kit{Source: filepath.Join(settings.CoreKits(), "aws-broker"), Args: brokerArgs})
		bindings[BrokerService] = brokerBinding()
	}
	if err := addKits(&userKits, settings.kits, settings.Home, "config"); err != nil {
		return nil, err
	}

	for _, name := range agentNames {
		for service, binding := range agents[name].bindings {
			if _, set := bindings[service]; !set {
				bindings[service] = binding
			}
		}
	}
	if selection.Gh != "" && contains(agentNames, "copilot") {
		if _, set := secrets["copilot"]; !set {
			secrets["copilot"] = map[string]any{"command": ShellQuote(selection.Gh) + " auth token"}
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
		Base:              Kit{Source: DefaultBase},
		Agents:            agentNames,
		AWSProfiles:       profiles,
		AWSRegion:         region,
		AWSDefaultProfile: defaultProfile,
		Secrets:           secrets,
		Bindings:          bindings,
		RepoKits:          repoKits,
	}
	if base != nil {
		composition.Base = *base
	}
	for _, group := range [][]Kit{agentKits, core, bookKits, repoKits, userKits} {
		composition.Kits = append(composition.Kits, group...)
	}
	for _, book := range active {
		composition.Books = append(composition.Books, book.Name)
	}
	return composition, nil
}

// activeBooks returns the installed books this sandbox asked for, by --book,
// by the repository's config or by a [[repos]] match, followed by books given
// as a directory on the command line.
func activeBooks(settings *Settings, selection Selection) ([]Book, error) {
	installed := map[string]bool{}
	var names []string
	for _, book := range settings.Books {
		installed[book.Name] = true
		names = append(names, book.Name)
	}
	installedList := strings.Join(names, ", ")
	if installedList == "" {
		installedList = "none"
	}
	wanted := map[string]bool{}
	var adhoc []Book
	for _, ref := range selection.Books {
		if looksLikeName(strings.ReplaceAll(ref, ".", "")) && !strings.HasPrefix(ref, ".") {
			if !installed[ref] {
				return nil, errorf("--book %s: no installed book has that name (installed: %s); pass the book's directory or add it to books in config.toml", ref, installedList)
			}
			wanted[ref] = true
			continue
		}
		book, err := LoadBook(ExpandUser(ref))
		if err != nil {
			return nil, err
		}
		adhoc = append(adhoc, *book)
	}
	if repo := selection.Repo; repo != nil {
		for _, name := range repo.books {
			if !installed[name] {
				return nil, errorf("%s asks for book %s, which is not installed (installed: %s); add its directory to books in ~/.scoring-stage/config.toml", repo.Path, name, installedList)
			}
			wanted[name] = true
		}
	}
	var active []Book
	seen := map[string]string{}
	for _, book := range settings.Books {
		matched := false
		for _, r := range book.repos {
			matched = matched || matchesAny(selection.RepoNames, r.patterns)
		}
		if wanted[book.Name] || matched {
			active = append(active, book)
			seen[book.Name] = book.Path
		}
	}
	for _, book := range adhoc {
		if path, taken := seen[book.Name]; taken {
			if path == book.Path {
				continue
			}
			return nil, errorf("--book %s is named %s, like the installed book at %s", book.Path, book.Name, path)
		}
		active = append(active, book)
		seen[book.Name] = book.Path
	}
	return active, nil
}

func parseAgent(entry any, dir, owner string) (Kit, error) {
	if name, ok := entry.(string); ok && looksLikeName(name) {
		spec, known := agents[name]
		if !known {
			return Kit{}, errorf("%s: unknown agent %s (known: %s, or a kit reference)", owner, name, strings.Join(AgentNames(), ", "))
		}
		return Kit{Source: spec.kit}, nil
	}
	return parseKit(entry, dir, owner)
}

func containsKit(kits []Kit, kit Kit) bool {
	for _, existing := range kits {
		if existing.Equal(kit) {
			return true
		}
	}
	return false
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

func bookSecret(spec any, bookPath, owner string) (map[string]any, error) {
	table, ok := spec.(map[string]any)
	if !ok {
		return nil, errorf(`%s: a secret is a table such as {command = "..."}`, owner)
	}
	copied := make(map[string]any, len(table))
	for k, v := range table {
		copied[k] = v
	}
	if command, ok := copied["command"].(string); ok {
		copied["command"] = strings.ReplaceAll(command, "{book}", ShellQuote(bookPath))
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
	if value, present := table["default_profile"]; present {
		profile, ok := value.(string)
		if !ok {
			return result, errorf("%s: default_profile must be a string", where)
		}
		result.defaultProfile = profile
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

func (s *Settings) AgentEntries() []string {
	var entries []string
	for _, entry := range s.agents {
		entries = append(entries, fmt.Sprint(entry))
	}
	return entries
}

// SaveAgents writes agents as the first line of config.toml, where a
// top-level key stays outside any table.
func SaveAgents(home string, names []string) error {
	for _, name := range names {
		if !IsAgentName(name) {
			return errorf("unknown agent %s (known: %s)", name, strings.Join(AgentNames(), ", "))
		}
	}
	path := filepath.Join(home, "config.toml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(existing) > 0 {
		data, err := readTOML(path)
		if err != nil {
			return err
		}
		if _, set := data["agents"]; set {
			return errorf("%s already sets agents; edit it there", path)
		}
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = `"` + name + `"`
	}
	line := "agents = [" + strings.Join(quoted, ", ") + "]\n"
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(line), existing...), 0o600)
}
