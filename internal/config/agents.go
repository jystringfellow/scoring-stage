package config

import (
	"sort"
	"strings"
)

type agentSpec struct {
	kit      string
	bindings map[string]any
}

var CopilotHosts = []string{
	"api.githubcopilot.com",
	"api.individual.githubcopilot.com",
	"api.business.githubcopilot.com",
	"api.enterprise.githubcopilot.com",
	"copilot.github.com",
}

// Agents maps the short agent names to Docker's published mixins and the
// credential bindings each needs. agy signs in from inside the sandbox, so it
// needs no binding.
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

func repositoryOf(source string) string {
	slash := strings.LastIndex(source, "/")
	head, last := source[:slash+1], source[slash+1:]
	last, _, _ = strings.Cut(last, "@")
	last, _, _ = strings.Cut(last, ":")
	return head + last
}
