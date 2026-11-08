package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const RepoConfigPath = ".scoring-stage/config.toml"

var (
	repoKeys     = []string{"agents", "packs", "kits"}
	hostOnlyKeys = []string{"aws", "base", "bindings", "copy_logins", "secrets", "ship"}
)

// RepoConfig is what a repository asks for. A repository is not trusted with
// anything that acts on the host, so it may name agents, installed packs and
// kits, but not secrets, bindings, AWS profiles or pack locations.
type RepoConfig struct {
	Path   string
	dir    string
	agents []any
	packs  []string
	kits   []any
}

func LoadRepoConfig(repoDir string) (*RepoConfig, error) {
	path := filepath.Join(repoDir, RepoConfigPath)
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	data, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	var refused []string
	for _, key := range hostOnlyKeys {
		if _, present := data[key]; present {
			refused = append(refused, key)
		}
	}
	if len(refused) > 0 {
		sort.Strings(refused)
		return nil, errorf("%s: a repository cannot set %s; set it in ~/.scoring-stage/config.toml or a pack", path, strings.Join(refused, ", "))
	}
	if err := checkKeys(data, repoKeys, path); err != nil {
		return nil, err
	}
	repo := &RepoConfig{Path: path, dir: repoDir}
	if repo.agents, err = optionalList(data, "agents", path); err != nil {
		return nil, err
	}
	if repo.kits, err = optionalList(data, "kits", path); err != nil {
		return nil, err
	}
	if repo.packs, err = optionalStrings(data, "packs", path); err != nil {
		return nil, err
	}
	for _, name := range repo.packs {
		if !looksLikeName(strings.ReplaceAll(name, ".", "")) {
			return nil, errorf("%s: packs are named, not located: %s; install it by adding its directory to packs in ~/.scoring-stage/config.toml", path, name)
		}
	}
	return repo, nil
}
