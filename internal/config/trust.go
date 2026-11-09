package config

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func trustFile(home string) string { return filepath.Join(home, "trusted-kits") }

// KitFingerprint identifies a local kit by its files and a published kit by its reference, each with its arguments.
func KitFingerprint(kit Kit) (string, error) {
	hash := sha256.New()
	args, err := json.Marshal(kit.Args)
	if err != nil {
		return "", err
	}
	if !kit.IsLocal() {
		fmt.Fprintf(hash, "ref\x00%s\x00%s", kit.Source, args)
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	fmt.Fprintf(hash, "dir\x00%s\x00", args)
	var files []string
	err = filepath.WalkDir(kit.Source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", errorf("cannot read kit %s: %v", kit.Source, err)
	}
	sort.Strings(files)
	for _, path := range files {
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		relative, _ := filepath.Rel(kit.Source, path)
		fmt.Fprintf(hash, "%s\x00%o\x00", relative, info.Mode())
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(hash, "%s\x00", target)
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%d\x00", len(content))
		hash.Write(content)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func trusted(home string) (map[string]bool, error) {
	known := map[string]bool{}
	file, err := os.Open(trustFile(home))
	if errors.Is(err, os.ErrNotExist) {
		return known, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) > 0 {
			known[fields[0]] = true
		}
	}
	return known, scanner.Err()
}

// UntrustedKits returns the kits the user has not approved, with their fingerprints.
func UntrustedKits(home string, kits []Kit) ([]Kit, []string, error) {
	known, err := trusted(home)
	if err != nil {
		return nil, nil, err
	}
	var untrusted []Kit
	var fingerprints []string
	for _, kit := range kits {
		fingerprint, err := KitFingerprint(kit)
		if err != nil {
			return nil, nil, err
		}
		if !known[fingerprint] {
			untrusted = append(untrusted, kit)
			fingerprints = append(fingerprints, fingerprint)
		}
	}
	return untrusted, fingerprints, nil
}

func TrustKits(home string, kits []Kit, fingerprints []string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(trustFile(home), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	for i, kit := range kits {
		if _, err := fmt.Fprintf(file, "%s %s\n", fingerprints[i], kit.Source); err != nil {
			return err
		}
	}
	return nil
}
