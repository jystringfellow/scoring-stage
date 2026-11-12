package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type kitCapability struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

type kitSpec struct {
	Extends      any             `yaml:"extends"`
	Capabilities []kitCapability `yaml:"capabilities"`
}

type kitDocument struct {
	file string
	spec kitSpec
}

var kitFileExtensions = []string{".yaml", ".yml", ".json"}

func kitFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path == filepath.Join(dir, "files") {
			return filepath.SkipDir
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return errorf("%s is a link to a directory", path)
			}
		}
		if !entry.IsDir() && contains(kitFileExtensions, strings.ToLower(filepath.Ext(path))) {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func loadKit(dir string) ([]kitDocument, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, errorf("kit %s is not a directory", dir)
	}
	files, err := kitFiles(dir)
	if err != nil {
		return nil, errorf("kit %s: %v", dir, err)
	}
	var documents []kitDocument
	for _, file := range files {
		relative, _ := filepath.Rel(dir, file)
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, errorf("kit %s: cannot read %s: %v", dir, relative, err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(content))
		for {
			var raw any
			err := decoder.Decode(&raw)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, errorf("kit %s: %s is unreadable: %v", dir, relative, err)
			}
			if err := checkKitKeys(raw); err != nil {
				return nil, errorf("kit %s: %s: %v", dir, relative, err)
			}
			var spec kitSpec
			if err := convert(raw, &spec); err != nil {
				return nil, errorf("kit %s: %s is unreadable: %v", dir, relative, err)
			}
			documents = append(documents, kitDocument{file: relative, spec: spec})
		}
	}
	return documents, nil
}

func convert(raw any, target any) error {
	encoded, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(encoded, target)
}

func refuseCaseVariants(table map[string]any, known []string, where string) error {
	for key := range table {
		for _, name := range known {
			if key != name && strings.EqualFold(key, name) {
				return errorf("%s: key %q differs from %q only in case", where, Printable(key), name)
			}
		}
	}
	return nil
}

func checkKitKeys(raw any) error {
	if raw == nil {
		return nil
	}
	top, ok := raw.(map[string]any)
	if !ok {
		return errorf("a kit document must be a mapping")
	}
	if err := refuseCaseVariants(top, []string{"capabilities", "extends"}, "kit"); err != nil {
		return err
	}
	capabilities, _ := top["capabilities"].([]any)
	for index, entry := range capabilities {
		where := fmt.Sprintf("capabilities[%d]", index)
		capability, ok := entry.(map[string]any)
		if !ok {
			return errorf("%s must be a mapping", where)
		}
		if err := refuseCaseVariants(capability, []string{"type", "config"}, where); err != nil {
			return err
		}
		kind, _ := capability["type"].(string)
		if capabilityName(kind) != "credential" {
			continue
		}
		config, _ := capability["config"].(map[string]any)
		if err := refuseCaseVariants(config, []string{"service"}, where+".config"); err != nil {
			return err
		}
		for name, value := range config {
			table, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if err := refuseCaseVariants(table, []string{"inject"}, where+".config."+name); err != nil {
				return err
			}
			injects, ok := table["inject"].([]any)
			if table["inject"] != nil && !ok {
				return errorf("%s.config.%s.inject must be a list", where, name)
			}
			for position, inject := range injects {
				entryWhere := fmt.Sprintf("%s.config.%s.inject[%d]", where, name, position)
				fields, ok := inject.(map[string]any)
				if !ok {
					return errorf("%s must be a mapping", entryWhere)
				}
				if err := refuseCaseVariants(fields, []string{"domain"}, entryWhere); err != nil {
					return err
				}
				if domain, ok := fields["domain"].(string); !ok || strings.TrimSpace(domain) == "" {
					return errorf("%s has no domain string", entryWhere)
				}
			}
		}
	}
	return nil
}

var knownCapabilities = []string{"network-policy", "credential", "lifecycle", "agent-context", "host-mount"}

func capabilityName(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	name, found := strings.CutPrefix(kind, "com.docker.sandbox/")
	if !found {
		return ""
	}
	name, _, _ = strings.Cut(name, "@")
	if !contains(knownCapabilities, name) {
		return ""
	}
	return name
}

func credentialDomains(config map[string]any) []string {
	var found []string
	for _, kindConfig := range config {
		table, _ := kindConfig.(map[string]any)
		injects, _ := table["inject"].([]any)
		for _, inject := range injects {
			if entry, ok := inject.(map[string]any); ok {
				if domain, ok := entry["domain"].(string); ok {
					found = append(found, domain)
				}
			}
		}
	}
	sort.Strings(found)
	return found
}

func normalizeService(service string) string {
	return strings.ToLower(strings.TrimSpace(service))
}

func owns(owned map[string]any, service string) bool {
	_, ok := owned[service]
	return ok
}

func checkUntrustedKit(agents Agents, dir, owner string, owned map[string]any) error {
	documents, err := loadKit(dir)
	if err != nil {
		return errorf("%s: %v", owner, err)
	}
	for _, document := range documents {
		where := fmt.Sprintf("%s: kit %s (%s)", owner, dir, document.file)
		if document.spec.Extends != nil {
			return errorf("%s: extends another kit, which stage cannot check; copy what it needs into the kit", where)
		}
		for _, capability := range document.spec.Capabilities {
			name := capabilityName(capability.Type)
			if name == "" {
				return errorf("%s: unrecognised capability type %q", where, Printable(capability.Type))
			}
			if name != "credential" {
				continue
			}
			service, _ := capability.Config["service"].(string)
			if HasControl(service) {
				return errorf("%s: credential service %q contains control characters", where, Printable(service))
			}
			normalized := normalizeService(service)
			if normalized == "" {
				return errorf("%s: declares a credential without a service name", where)
			}
			if agents.isReservedService(normalized) {
				return errorf("%s: declares credential %s, which is reserved; %s", where, normalized, agents.reservedReason())
			}
			if owned == nil {
				return errorf("%s: declares credential %s; a repository's kits may declare no credentials, since a repository supplies none", where, normalized)
			}
			if service != normalized || !owns(owned, normalized) {
				return errorf("%s: declares credential %q, but the book has no [secrets.%s]; a book's kits may declare only credentials the book supplies, named in lower case", where, service, normalized)
			}
			domains := credentialDomains(capability.Config)
			if len(domains) == 0 {
				return errorf("%s: credential %s has no inject domains stage recognises", where, service)
			}
			for _, domain := range domains {
				if hasDomainGlob(domain) {
					return errorf("%s: credential %s is injected on %s; %s", where, service, Printable(domain), domainGlobReason)
				}
				if agents.ReachesReserved(domain) {
					return errorf("%s: credential %s is injected on %s, a host a reserved service uses; %s", where, service, Printable(domain), agents.reservedReason())
				}
			}
		}
	}
	return nil
}

func SummarizeKit(dir string) []string {
	var details []string
	documents, err := loadKit(dir)
	if err != nil {
		details = append(details, fmt.Sprintf("%v; read it before approving", err))
	}
	for _, document := range documents {
		if document.spec.Extends != nil {
			details = append(details, fmt.Sprintf("%s: extends %v; read the parent kit before approving", document.file, document.spec.Extends))
		}
		for _, capability := range document.spec.Capabilities {
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

func describeCapability(kind string, config map[string]any) []string {
	name := capabilityName(kind)
	switch name {
	case "":
		return []string{fmt.Sprintf("unrecognised capability %s; read the kit's file before approving", kind)}
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
		domains := credentialDomains(config)
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
