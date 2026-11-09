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
	Service string
	Domains []string
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
	var file struct {
		Bindings map[string]map[string]struct {
			Domains []string `yaml:"domains"`
		} `yaml:"bindings"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, &Error{fmt.Sprintf("cannot parse %s: %v", path, err)}
	}
	var bindings []Binding
	for _, service := range slices.Sorted(maps.Keys(file.Bindings)) {
		kinds := file.Bindings[service]
		binding := Binding{Service: service}
		for _, kind := range slices.Sorted(maps.Keys(kinds)) {
			for _, domain := range kinds[kind].Domains {
				if !slices.Contains(binding.Domains, domain) {
					binding.Domains = append(binding.Domains, domain)
				}
			}
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}
