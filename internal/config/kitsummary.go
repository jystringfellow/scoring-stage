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
			var spec kitSpec
			err := decoder.Decode(&spec)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, errorf("kit %s: %s is unreadable: %v", dir, relative, err)
			}
			documents = append(documents, kitDocument{file: relative, spec: spec})
		}
	}
	return documents, nil
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

func checkUntrustedKit(dir, owner string, owned map[string]any) error {
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
			if isReservedService(normalized) {
				return errorf("%s: declares credential %s, which is reserved; %s", where, normalized, reservedReason())
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
				if ReachesReserved(domain) {
					return errorf("%s: credential %s is injected on %s, a host a reserved service uses; %s", where, service, Printable(domain), reservedReason())
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
