package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type kitSpec struct {
	Capabilities []struct {
		Type   string         `yaml:"type"`
		Config map[string]any `yaml:"config"`
	} `yaml:"capabilities"`
}

// SummarizeKit describes what a local kit's capabilities do, for a person deciding whether to approve it.
func SummarizeKit(dir string) []string {
	var details []string
	for _, file := range kitFiles(dir) {
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var spec kitSpec
		if err := yaml.Unmarshal(content, &spec); err != nil {
			details = append(details, fmt.Sprintf("%s: unreadable (%v); read it before approving", filepath.Base(file), err))
			continue
		}
		for _, capability := range spec.Capabilities {
			details = append(details, describeCapability(capability.Type, capability.Config)...)
		}
	}
	if len(details) == 0 {
		details = append(details, "no capabilities found; read the kit's files before approving")
	}
	for i, detail := range details {
		details[i] = Printable(detail)
	}
	return details
}

func kitFiles(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	more, _ := filepath.Glob(filepath.Join(dir, "*.yml"))
	files = append(files, more...)
	sort.Strings(files)
	return files
}

func capabilityName(kind string) string {
	name := kind[strings.LastIndex(kind, "/")+1:]
	name, _, _ = strings.Cut(name, "@")
	return name
}

func refuseReservedKitCredentials(dir, owner string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	for _, file := range kitFiles(dir) {
		content, err := os.ReadFile(file)
		if err != nil {
			return errorf("%s: kit %s: cannot read %s: %v", owner, dir, filepath.Base(file), err)
		}
		var spec kitSpec
		if err := yaml.Unmarshal(content, &spec); err != nil {
			return errorf("%s: kit %s: %s is unreadable: %v", owner, dir, filepath.Base(file), err)
		}
		for _, capability := range spec.Capabilities {
			if capabilityName(capability.Type) != "credential" {
				continue
			}
			service, ok := capability.Config["service"].(string)
			if !ok {
				return errorf("%s: kit %s declares a credential without a service name in %s", owner, dir, filepath.Base(file))
			}
			if isReservedService(service) {
				return errorf("%s: kit %s declares credential %s, which is reserved; %s", owner, dir, Printable(service), reservedReason())
			}
		}
	}
	return nil
}

func describeCapability(kind string, config map[string]any) []string {
	name := capabilityName(kind)
	switch name {
	case "network-policy":
		var details []string
		for _, phase := range []string{"install", "runtime"} {
			rules, _ := config[phase].(map[string]any)
			for _, decision := range []string{"allow", "deny"} {
				if hosts := ruleHosts(rules[decision]); len(hosts) > 0 {
					details = append(details, fmt.Sprintf("network %s %s: %s", phase, decision, strings.Join(hosts, ", ")))
				}
			}
		}
		return details
	case "credential":
		service, _ := config["service"].(string)
		var domains []string
		for _, kindConfig := range config {
			table, _ := kindConfig.(map[string]any)
			injects, _ := table["inject"].([]any)
			for _, inject := range injects {
				if entry, ok := inject.(map[string]any); ok {
					if domain, ok := entry["domain"].(string); ok {
						domains = append(domains, domain)
					}
				}
			}
		}
		if len(domains) == 0 {
			return []string{fmt.Sprintf("credential %s: unrecognised shape; read the kit's file before approving", service)}
		}
		return []string{fmt.Sprintf("credential %s injected on %s", service, strings.Join(domains, ", "))}
	case "lifecycle":
		files, _ := config["files"].([]any)
		var commands int
		for _, phase := range []string{"install", "startup", "start"} {
			list, _ := config[phase].([]any)
			commands += len(list)
		}
		return []string{fmt.Sprintf("writes %d file(s) and runs %d command(s) in the sandbox", len(files), commands)}
	case "host-mount":
		return []string{fmt.Sprintf("mounts from the host: %v", config)}
	default:
		return []string{name}
	}
}

func ruleHosts(value any) []string {
	list, _ := value.([]any)
	var hosts []string
	for _, entry := range list {
		switch typed := entry.(type) {
		case string:
			hosts = append(hosts, typed)
		case map[string]any:
			names, _ := typed["hosts"].([]any)
			for _, host := range names {
				rule := fmt.Sprint(host)
				if methods, ok := typed["methods"].([]any); ok {
					rule += fmt.Sprintf(" %v", methods)
				}
				if paths, ok := typed["paths"].([]any); ok {
					rule += fmt.Sprintf(" %v", paths)
				}
				hosts = append(hosts, rule)
			}
		}
	}
	return hosts
}
