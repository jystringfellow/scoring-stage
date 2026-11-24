package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/bookref"
)

const (
	RepoConfigPath = ".scoring-stage/config.toml"
	RepoLockPath   = ".scoring-stage/" + bookref.LockName
)

var (
	repoKeys     = []string{"agents", "books", "kits", "takes"}
	hostOnlyKeys = []string{"aws", "base", "bindings", "copy_logins", "github", "secrets", "ship"}
)

// RepoConfig is what a repository asks for. A repository is not trusted with
// anything that acts on the host, so it may name agents, installed books,
// books by reference and kits, but not secrets, bindings, AWS profiles or
// book directories.
type RepoConfig struct {
	Path   string
	dir    string
	agents []any
	books  []bookUse
	kits   []any
	takes  []string
	lock   *bookref.Lock
}

func LoadRepoConfig(repoDir string) (*RepoConfig, error) {
	path := filepath.Join(repoDir, RepoConfigPath)
	if found, err := repoFile(repoDir, RepoConfigPath); err != nil || !found {
		return nil, err
	}
	data, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	if _, present := data["agent"]; present {
		return nil, errorf("%s: a repository cannot define agents; it names them in agents, and an agent is defined in ~/.scoring-stage/config.toml", path)
	}
	if err := refuseGear(data, path+": a repository"); err != nil {
		return nil, err
	}
	if err := refuseHall(data, path+": a repository"); err != nil {
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
		return nil, errorf("%s: a repository cannot set %s; set it in ~/.scoring-stage/config.toml or a book", path, strings.Join(refused, ", "))
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
	if repo.books, err = decodeBookUses(data["books"], "books", path); err != nil {
		return nil, err
	}
	if repo.takes, err = decodeTakes(data, path); err != nil {
		return nil, err
	}
	for _, use := range repo.books {
		name := use.text
		if bookref.IsRef(name) {
			if _, err := bookref.Parse(name); err != nil {
				return nil, errorf("%s: %v", path, err)
			}
			continue
		}
		if !looksLikeName(strings.ReplaceAll(name, ".", "")) {
			return nil, errorf("%s: books are named, not located: %s; name an installed book or give a reference such as github.com/example-org/books//backend/go@v1.2.0", path, Printable(name))
		}
	}
	if _, err := repoFile(repoDir, RepoLockPath); err != nil {
		return nil, err
	}
	if repo.lock, err = bookref.ReadLock(filepath.Join(repoDir, RepoLockPath)); err != nil {
		return nil, err
	}
	return repo, nil
}

func repoFile(repoDir, relative string) (bool, error) {
	path := repoDir
	for _, part := range strings.Split(relative, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return false, errorf("%s is a symbolic link; stage does not follow a repository's links", path)
		}
	}
	return true, nil
}

func (r *RepoConfig) BookReferences() []bookref.Ref {
	var references []bookref.Ref
	for _, use := range r.books {
		if ref, err := bookref.Parse(use.text); err == nil && bookref.IsRef(use.text) {
			references = append(references, ref)
		}
	}
	return references
}

func (r *RepoConfig) Lock() *bookref.Lock { return r.lock }
