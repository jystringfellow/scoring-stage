package kits

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	scoringstage "github.com/jystringfellow/scoring-stage"
)

const root = "kits"

var digest = sync.OnceValue(func() string {
	hash := sha256.New()
	err := fs.WalkDir(scoringstage.Kits, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := scoringstage.Kits.ReadFile(path)
		if err != nil {
			return err
		}
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(content)
		hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
})

// Dir is where the core kits embedded in this binary live once extracted;
// sbx accepts local kits only as directories.
func Dir(home string) string {
	return filepath.Join(home, "kits", digest())
}

func Extract(home string) (string, error) {
	dir := Dir(home)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, ".extract-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o755); err != nil {
		return "", err
	}
	err = fs.WalkDir(scoringstage.Kits, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		target := filepath.Join(staging, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := scoringstage.Kits.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.Rename(staging, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return dir, nil
		}
		return "", errors.Join(errors.New("cannot extract the core kits"), err)
	}
	return dir, nil
}
