package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type agentSpec struct {
	kit      string
	bindings map[string]any
	secrets  map[string]any
	login    []LoginFile
}

// LoginFile is a file an agent keeps inside the sandbox for a sign-in the proxy
// cannot hold on the host. Optional files are carried when the sandbox has them.
type LoginFile struct {
	Path     string
	Required bool
}

// ghPlaceholder in an agent's secret command stands for the gh CLI found on
// the host; the secret is left out of a sandbox when there is none.
const ghPlaceholder = "{gh}"

// builtinAgents maps the short agent names to Docker's published mixins and the
// credential bindings each needs. agy signs in to Google inside the sandbox and
// keeps the token, and the record that its first-run setup is done, in files
// stage can copy between sandboxes.
var builtinAgents = map[string]agentSpec{
	"claude": {
		kit:      "docker.io/sbx/claude-mixin:2.1.319",
		bindings: map[string]any{"anthropic": domains("oauth", "platform.claude.com", "api.anthropic.com")},
	},
	"codex": {
		kit:      "docker.io/sbx/codex-mixin:0.167.0",
		bindings: map[string]any{"openai": domains("oauth", "auth.openai.com", "chatgpt.com")},
	},
	"copilot": {
		kit: "docker.io/sbx/copilot-mixin:1.0.86",
		bindings: map[string]any{"copilot": domains("apiKey",
			"api.githubcopilot.com",
			"api.individual.githubcopilot.com",
			"api.business.githubcopilot.com",
			"api.enterprise.githubcopilot.com",
			"copilot.github.com",
		)},
		secrets: map[string]any{"copilot": map[string]any{"command": ghPlaceholder + " auth token"}},
	},
	"agy": {
		kit: "docker.io/sbx/antigravity-mixin:1.0.0",
		login: []LoginFile{
			{Path: "/home/agent/.gemini/antigravity-cli/antigravity-oauth-token", Required: true},
			{Path: "/home/agent/.gemini/antigravity-cli/cache/onboarding.json"},
		},
	},
}

// agentDefinition records what a definition in the user's config changed, for
// managed settings to judge.
type agentDefinition struct {
	name            string
	keepsBuiltinKit bool
	setsLoginFiles  bool
}

// Agents is the agent table: the built-in agents, with the definitions from
// the user's config added or laid over them.
type Agents struct {
	specs   map[string]agentSpec
	defined []agentDefinition
}

func BuiltinAgents() Agents {
	specs := make(map[string]agentSpec, len(builtinAgents))
	for name, spec := range builtinAgents {
		specs[name] = spec
	}
	return Agents{specs: specs}
}

func LoadAgents(home string) (Agents, error) {
	file := filepath.Join(home, "config.toml")
	if _, err := os.Stat(file); err != nil {
		return BuiltinAgents(), nil
	}
	data, err := readTOML(file)
	if err != nil {
		return Agents{}, err
	}
	return decodeAgents(data["agent"], file, home)
}

// AgentNames lists the agents for help text. A config that cannot be read
// yields the built-in names; the command reports the error when it loads the
// settings.
func AgentNames(home string) []string {
	agents, err := LoadAgents(home)
	if err != nil {
		agents = BuiltinAgents()
	}
	return agents.Names()
}

var (
	agentName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	agentKeys = []string{"kit", "bindings", "secrets", "login_files"}
)

func decodeAgents(value any, file, home string) (Agents, error) {
	agents := BuiltinAgents()
	if value == nil {
		return agents, nil
	}
	definitions, ok := value.(map[string]any)
	if !ok {
		return agents, errorf("%s: agent must be a table of agent definitions, such as [agent.example]", file)
	}
	for _, name := range sortedKeys(definitions) {
		if !agentName.MatchString(name) {
			return agents, errorf("%s: agent name %q must match %s", file, Printable(name), agentName)
		}
		where := file + " [agent." + name + "]"
		table, ok := definitions[name].(map[string]any)
		if !ok {
			return agents, errorf("%s: must be a table", where)
		}
		builtin, overrides := builtinAgents[name]
		spec, err := decodeAgent(table, builtin, where, home)
		if err != nil {
			return agents, err
		}
		_, setsLoginFiles := table["login_files"]
		agents.specs[name] = spec
		agents.defined = append(agents.defined, agentDefinition{
			name:            name,
			keepsBuiltinKit: overrides && sameKit(spec.kit, builtin.kit),
			setsLoginFiles:  setsLoginFiles,
		})
	}
	names := agents.Names()
	for i, name := range names {
		for _, other := range names[:i] {
			if sameKit(agents.specs[name].kit, agents.specs[other].kit) {
				return agents, errorf("%s: agents %s and %s both use %s; a kit belongs to one agent, so define the name that already has it",
					file, other, name, kitIdentity(agents.specs[name].kit))
			}
		}
	}
	return agents, nil
}

func decodeAgent(table map[string]any, inherited agentSpec, where, home string) (agentSpec, error) {
	spec := inherited
	if err := checkKeys(table, agentKeys, where); err != nil {
		return spec, err
	}
	if table["kit"] == nil {
		return spec, errorf("%s: kit is required", where)
	}
	reference, ok := table["kit"].(string)
	if !ok || looksLikeName(reference) || HasHidden(reference) || strings.ContainsAny(reference, " ") {
		return spec, errorf("%s: kit must be a kit reference such as docker.io/sbx/example-mixin:1.0.0 or a ./ directory, not %s", where, Printable(fmt.Sprint(table["kit"])))
	}
	kit, err := parseKit(reference, home, where)
	if err != nil {
		return spec, err
	}
	spec.kit = kit.Source
	for key, target := range map[string]*map[string]any{"bindings": &spec.bindings, "secrets": &spec.secrets} {
		if _, present := table[key]; !present {
			continue
		}
		if *target, err = optionalTable(table, key, where); err != nil {
			return spec, err
		}
	}
	if err := checkAgentCredentials(spec.secrets, spec.bindings, where); err != nil {
		return spec, err
	}
	if _, present := table["login_files"]; present {
		if spec.login, err = decodeLoginFiles(table["login_files"], where); err != nil {
			return spec, err
		}
	}
	return spec, nil
}

func checkAgentCredentials(secrets, bindings map[string]any, where string) error {
	for kind, table := range map[string]map[string]any{"secret": secrets, "binding": bindings} {
		for _, service := range sortedKeys(table) {
			if HasHidden(service) {
				return errorf("%s: %s %q contains hidden characters", where, kind, Printable(service))
			}
			if normalizeService(service) == BrokerService {
				return errorf("%s: %s %s is stage's own; an agent cannot set it", where, kind, BrokerService)
			}
		}
	}
	for _, service := range sortedKeys(secrets) {
		table, ok := secrets[service].(map[string]any)
		if !ok {
			return errorf(`%s: secret %s: a secret is a table such as {command = "..."}`, where, service)
		}
		if command, present := table["command"]; present {
			if _, ok := command.(string); !ok {
				return errorf("%s: secret %s: command must be a string", where, service)
			}
		}
	}
	for _, service := range sortedKeys(bindings) {
		hosts, err := bindingDomains(bindings[service])
		if err != nil {
			return errorf("%s: binding %s: %v", where, service, err)
		}
		for _, host := range hosts {
			if HasHidden(host) {
				return errorf("%s: binding %s: domain %q contains hidden characters", where, service, Printable(host))
			}
		}
	}
	return refuseGitHubService(secrets, bindings, where)
}

func decodeLoginFiles(value any, where string) ([]LoginFile, error) {
	entries, ok := asList(value)
	if !ok {
		return nil, errorf("%s: login_files must be a list", where)
	}
	var files []LoginFile
	saved := map[string]string{}
	for _, entry := range entries {
		file := LoginFile{Required: true}
		switch typed := entry.(type) {
		case string:
			file.Path = typed
		case map[string]any:
			location, hasPath := typed["path"].(string)
			optional, isBool := typed["optional"].(bool)
			if !hasPath || typed["optional"] != nil && !isBool || len(unknownKeys(typed, []string{"path", "optional"})) > 0 {
				return nil, errorf("%s: a login file is a path or { path, optional }, not %s", where, Printable(fmt.Sprint(entry)))
			}
			file.Path, file.Required = location, !optional
		default:
			return nil, errorf("%s: a login file is a path or { path, optional }, not %s", where, Printable(fmt.Sprint(entry)))
		}
		if !strings.HasPrefix(file.Path, "/") || !isTakePath(file.Path) || HasHidden(file.Path) {
			return nil, errorf("%s: login file %s must be an absolute path in the sandbox with no .., doubled or trailing slash", where, Printable(file.Path))
		}
		name := path.Base(file.Path)
		if other, taken := saved[name]; taken {
			return nil, errorf("%s: login files %s and %s are both saved as %s on the host; give them different file names", where, other, file.Path, name)
		}
		saved[name] = file.Path
		files = append(files, file)
	}
	return files, nil
}

func (a Agents) Names() []string {
	names := make([]string, 0, len(a.specs))
	for name := range a.specs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a Agents) Has(name string) bool {
	_, known := a.specs[name]
	return known
}

func looksLikeName(entry string) bool {
	return entry != "" && !strings.ContainsAny(entry, "/:@~.")
}

// forKit finds the agent whose kit source refers to. A published kit matches
// by repository, ignoring tag and digest, so a pinned reference to another
// version keeps its bindings.
func (a Agents) forKit(source string) (string, bool) {
	for _, name := range a.Names() {
		if sameKit(a.specs[name].kit, source) {
			return name, true
		}
	}
	return "", false
}

func isLocalKit(source string) bool { return strings.HasPrefix(source, "/") }

func kitIdentity(source string) string {
	if isLocalKit(source) {
		return source
	}
	return repositoryOf(source)
}

func sameKit(one, other string) bool {
	return isLocalKit(one) == isLocalKit(other) && kitIdentity(one) == kitIdentity(other)
}

// repositoryOf normalises a kit reference to registry/path without tag or
// digest, so docker.io's aliases and letter case name the same repository.
func repositoryOf(source string) string {
	source = strings.ToLower(source)
	slash := strings.LastIndex(source, "/")
	head, last := source[:slash+1], source[slash+1:]
	last, _, _ = strings.Cut(last, "@")
	last, _, _ = strings.Cut(last, ":")
	repository := head + last
	first, rest, nested := strings.Cut(repository, "/")
	switch {
	case !nested:
		return "docker.io/library/" + repository
	case first == "index.docker.io", first == "registry-1.docker.io":
		return "docker.io/" + rest
	case !strings.ContainsAny(first, ".:") && first != "localhost":
		return "docker.io/" + repository
	}
	return repository
}

var mixinRepository = regexp.MustCompile(`(^|/)sbx/[^/]+-mixin$`)

// looksLikeAgentMixin reports a reference to one of Docker's agent mixins,
// known to stage or not.
func looksLikeAgentMixin(source string) bool {
	return mixinRepository.MatchString(repositoryOf(source))
}

func (a Agents) LoginFiles(agent string) []LoginFile {
	return a.specs[agent].login
}

func (a Agents) copyable() []string {
	var names []string
	for _, name := range a.Names() {
		if len(a.specs[name].login) > 0 {
			names = append(names, name)
		}
	}
	return names
}

// agentSecret resolves an agent's default secret for one sandbox, and reports
// false when its command needs a gh CLI the host does not have.
func agentSecret(spec any, gh string) (map[string]any, bool) {
	table, _ := spec.(map[string]any)
	resolved := make(map[string]any, len(table))
	for key, value := range table {
		resolved[key] = value
	}
	if command, ok := resolved["command"].(string); ok && strings.Contains(command, ghPlaceholder) {
		if gh == "" {
			return nil, false
		}
		resolved["command"] = strings.ReplaceAll(command, ghPlaceholder, ShellQuote(gh))
	}
	return resolved, true
}

func boundByBuiltinAgent(host string) bool {
	for _, spec := range builtinAgents {
		for _, binding := range spec.bindings {
			hosts, _ := bindingDomains(binding)
			for _, bound := range hosts {
				if normalizeHost(bound) == host {
					return true
				}
			}
		}
	}
	return false
}

var sbxBuiltinServices = []string{"anthropic", "copilot", "cursor", "devin", "droid", "github", "google", "groq", "mistral", "nebius", "openai", "openrouter", "xai"}

var gitHubApexes = []string{"github.com", "github.ai", "githubusercontent.com"}

func (a Agents) reservedServices() []string {
	reserved := map[string]bool{GitHubService: true, BrokerService: true}
	for _, service := range sbxBuiltinServices {
		reserved[service] = true
	}
	for _, spec := range a.specs {
		for _, table := range []map[string]any{spec.bindings, spec.secrets} {
			for service := range table {
				reserved[normalizeService(service)] = true
			}
		}
	}
	names := make([]string, 0, len(reserved))
	for service := range reserved {
		names = append(names, service)
	}
	sort.Strings(names)
	return names
}

func (a Agents) isReservedService(service string) bool {
	return contains(a.reservedServices(), normalizeService(service))
}

func (a Agents) reservedReason() string {
	return "books and repository kits cannot touch the credentials of agents, sbx's built-in services, GitHub or the stage broker (reserved: " + strings.Join(a.reservedServices(), ", ") + ")"
}

func (a Agents) reservedDomains() []string {
	targets := append([]string{}, gitHubApexes...)
	bindings := []map[string]any{{BrokerService: brokerBinding()}}
	for _, spec := range a.specs {
		bindings = append(bindings, spec.bindings)
	}
	for _, table := range bindings {
		for _, binding := range table {
			domains, _ := bindingDomains(binding)
			for _, domain := range domains {
				targets = append(targets, normalizeHost(domain))
			}
		}
	}
	sort.Strings(targets)
	return targets
}

func normalizeHost(domain string) string {
	host := strings.ToLower(strings.TrimSpace(domain))
	if index := strings.LastIndex(host, ":"); index >= 0 {
		host = host[:index]
	}
	return strings.TrimSuffix(host, ".")
}

func hostReaches(host, target string) bool {
	if star := strings.LastIndex(host, "*"); star >= 0 {
		tail := host[star+1:]
		return tail == "" || strings.HasSuffix(target, tail) || strings.HasSuffix(tail, "."+target)
	}
	return host == target || strings.HasSuffix(host, "."+target) || strings.HasSuffix(target, "."+host)
}

const domainGlobReason = "glob characters other than * are refused because stage cannot tell which hosts they match"

func hasDomainGlob(domain string) bool {
	return strings.ContainsAny(normalizeHost(domain), `?[]{}\`)
}

func reachesAny(domain string, targets []string) bool {
	host := normalizeHost(domain)
	if hasDomainGlob(host) {
		return true
	}
	for _, target := range targets {
		if hostReaches(host, target) {
			return true
		}
	}
	return false
}

func (a Agents) ReachesReserved(domain string) bool {
	return reachesAny(domain, a.reservedDomains())
}
