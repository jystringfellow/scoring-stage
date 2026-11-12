package config

import (
	"slices"
	"strings"
)

type StageBinding struct {
	Domains []string
	Reasons []string
}

func (s *Settings) StageBindings() map[string]*StageBinding {
	expected := map[string]*StageBinding{}
	AddStageBindings(expected, map[string]any{BrokerService: brokerBinding()}, "the AWS broker")
	if mode := s.GitHubMode(); mode != "" && mode != GitHubNone {
		AddStageBindings(expected, map[string]any{GitHubService: gitHubBinding()}, `[github] mode "`+mode+`"`)
	}
	entries := slices.Clone(s.agents)
	for _, book := range s.Books {
		entries = append(entries, book.agents...)
		for _, r := range book.repos {
			entries = append(entries, r.agents...)
		}
	}
	for _, entry := range entries {
		if name, known := s.Agents.named(entry); known {
			AddStageBindings(expected, s.Agents.specs[name].bindings, "agent "+name)
		}
	}
	AddStageBindings(expected, s.bindings, "config.toml [bindings]")
	for _, book := range s.Books {
		AddStageBindings(expected, book.bindings, "book "+book.Name)
	}
	return expected
}

func AddStageBindings(expected map[string]*StageBinding, bindings map[string]any, reason string) {
	for _, service := range sortedKeys(bindings) {
		hosts, err := bindingDomains(bindings[service])
		if err != nil {
			continue
		}
		binding := expected[service]
		if binding == nil {
			binding = &StageBinding{}
			expected[service] = binding
		}
		for _, host := range hosts {
			host = strings.ToLower(host)
			if !contains(binding.Domains, host) {
				binding.Domains = append(binding.Domains, host)
			}
		}
		if !contains(binding.Reasons, reason) {
			binding.Reasons = append(binding.Reasons, reason)
		}
	}
}

func (a Agents) named(entry any) (string, bool) {
	source := kitSource(entry)
	if source == "" {
		return "", false
	}
	if looksLikeName(source) {
		return source, a.Has(source)
	}
	return a.forKit(source)
}

func (s *Settings) KnownStageBindings() map[string]*StageBinding {
	known := map[string]*StageBinding{}
	AddStageBindings(known, map[string]any{BrokerService: brokerBinding()}, "the AWS broker")
	AddStageBindings(known, map[string]any{GitHubService: gitHubBinding()}, "[github] mode")
	for _, name := range s.Agents.Names() {
		AddStageBindings(known, s.Agents.specs[name].bindings, "agent "+name)
	}
	return known
}
