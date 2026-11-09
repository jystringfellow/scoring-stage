package sbx

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

type Binding struct {
	Service      string
	Domains      []string
	Unrecognised bool
}

func CredentialsPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "sbx", "credentials.yaml")
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "sbx", "credentials.yaml")
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "sbx", "credentials.yaml")
	}
	return ""
}

func ReadBindings(path string) ([]Binding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file any
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, &Error{fmt.Sprintf("cannot parse %s: %v", path, err)}
	}
	if file == nil {
		return nil, nil
	}
	top, ok := file.(map[string]any)
	if !ok {
		return nil, &Error{fmt.Sprintf("cannot parse %s: the top level is not a mapping", path)}
	}
	if top["bindings"] == nil {
		return nil, nil
	}
	services, ok := top["bindings"].(map[string]any)
	if !ok {
		return nil, &Error{fmt.Sprintf("cannot parse %s: bindings is not a mapping", path)}
	}
	var bindings []Binding
	for _, service := range slices.Sorted(maps.Keys(services)) {
		domains, ok := bindingDomains(services[service])
		bindings = append(bindings, Binding{Service: service, Domains: domains, Unrecognised: !ok})
	}
	return bindings, nil
}

func bindingDomains(value any) ([]string, bool) {
	kinds, ok := value.(map[string]any)
	if !ok || len(kinds) == 0 {
		return nil, false
	}
	var domains []string
	for _, kind := range slices.Sorted(maps.Keys(kinds)) {
		table, ok := kinds[kind].(map[string]any)
		if !ok {
			return nil, false
		}
		list, ok := table["domains"].([]any)
		if !ok {
			return nil, false
		}
		for _, entry := range list {
			domain, ok := entry.(string)
			if !ok {
				return nil, false
			}
			if !slices.Contains(domains, domain) {
				domains = append(domains, domain)
			}
		}
	}
	return domains, true
}
