package config

import (
	"regexp"
	"sort"
	"strings"
)

type agentSpec struct {
	kit      string
	bindings map[string]any
	login    []LoginFile
}

// LoginFile is a file an agent keeps inside the sandbox for a sign-in the proxy
// cannot hold on the host. Optional files are carried when the sandbox has them.
type LoginFile struct {
	Path     string
	Required bool
}

var CopilotHosts = []string{
	"api.githubcopilot.com",
	"api.individual.githubcopilot.com",
	"api.business.githubcopilot.com",
	"api.enterprise.githubcopilot.com",
	"copilot.github.com",
}

// agents maps the short agent names to Docker's published mixins and the
// credential bindings each needs. agy signs in to Google inside the sandbox and
// keeps the token, and the record that its first-run setup is done, in files
// stage can copy between sandboxes.
var agents = map[string]agentSpec{
	"claude": {
		kit:      "docker.io/sbx/claude-mixin:2.1.319",
		bindings: map[string]any{"anthropic": domains("oauth", "platform.claude.com", "api.anthropic.com")},
	},
	"codex": {
		kit:      "docker.io/sbx/codex-mixin:0.167.0",
		bindings: map[string]any{"openai": domains("oauth", "auth.openai.com", "chatgpt.com")},
	},
	"copilot": {
		kit:      "docker.io/sbx/copilot-mixin:1.0.86",
		bindings: map[string]any{"copilot": domains("apiKey", CopilotHosts...)},
	},
	"agy": {
		kit: "docker.io/sbx/antigravity-mixin:1.0.0",
		login: []LoginFile{
			{Path: "/home/agent/.gemini/antigravity-cli/antigravity-oauth-token", Required: true},
			{Path: "/home/agent/.gemini/antigravity-cli/cache/onboarding.json"},
		},
	},
}

func AgentNames() []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func IsAgentName(entry string) bool {
	_, known := agents[entry]
	return known
}

func looksLikeName(entry string) bool {
	return entry != "" && !strings.ContainsAny(entry, "/:@~.")
}

// agentFor finds the agent whose mixin repository source refers to, ignoring
// tag and digest, so a pinned reference to another version keeps its bindings.
func agentFor(source string) (string, bool) {
	repository := repositoryOf(source)
	for name, spec := range agents {
		if repositoryOf(spec.kit) == repository {
			return name, true
		}
	}
	return "", false
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

func LoginFiles(agent string) []LoginFile {
	return agents[agent].login
}

func copyableAgents() []string {
	var names []string
	for _, name := range AgentNames() {
		if len(agents[name].login) > 0 {
			names = append(names, name)
		}
	}
	return names
}

var sbxBuiltinServices = []string{"anthropic", "copilot", "cursor", "devin", "droid", "github", "google", "groq", "mistral", "nebius", "openai", "openrouter", "xai"}

var gitHubApexes = []string{"github.com", "github.ai", "githubusercontent.com"}

func reservedServices() []string {
	reserved := map[string]bool{"copilot": true, GitHubService: true, BrokerService: true}
	for _, service := range sbxBuiltinServices {
		reserved[service] = true
	}
	for _, spec := range agents {
		for service := range spec.bindings {
			reserved[service] = true
		}
	}
	names := make([]string, 0, len(reserved))
	for service := range reserved {
		names = append(names, service)
	}
	sort.Strings(names)
	return names
}

func isReservedService(service string) bool {
	return contains(reservedServices(), normalizeService(service))
}

func reservedReason() string {
	return "books and repository kits cannot touch the credentials of agents, sbx's built-in services, GitHub or the stage broker (reserved: " + strings.Join(reservedServices(), ", ") + ")"
}

func reservedDomains() []string {
	targets := append([]string{}, gitHubApexes...)
	bindings := []map[string]any{{BrokerService: brokerBinding()}}
	for _, spec := range agents {
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

func reachesAny(domain string, targets []string) bool {
	host := normalizeHost(domain)
	for _, target := range targets {
		if hostReaches(host, target) {
			return true
		}
	}
	return false
}

func ReachesReserved(domain string) bool {
	return reachesAny(domain, reservedDomains())
}
