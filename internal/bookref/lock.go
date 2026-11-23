package bookref

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
)

const LockName = "stage.lock"

type Entry struct {
	Ref    string
	Commit string
	SHA256 string
	Scheme string
}

func (e Entry) describe() string {
	return "commit " + e.Commit[:12] + " (sha256 " + e.SHA256[:12] + ")"
}

type Lock struct {
	Path    string
	Entries []Entry
}

var (
	lockKeys    = []string{"ref", "commit", "sha256", "scheme"}
	sha256Shape = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ReadLock(path string) (*Lock, error) {
	lock := &Lock{Path: path}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return lock, nil
	}
	if err != nil {
		return nil, err
	}
	data := map[string]any{}
	if _, err := toml.Decode(string(content), &data); err != nil {
		return nil, errorf("%s: %v", path, err)
	}
	for key := range data {
		if key != "book" {
			return nil, errorf("%s: unknown key %s; a lock holds only [[book]] entries", path, strconv.QuoteToASCII(key))
		}
	}
	books, ok := data["book"].([]map[string]any)
	if _, present := data["book"]; present && !ok {
		return nil, errorf("%s: book must be [[book]] entries", path)
	}
	seen := map[string]bool{}
	for i, book := range books {
		where := path + ": [[book]] " + strconv.Itoa(i+1)
		var fields [4]string
		for key, value := range book {
			index := indexOf(lockKeys, key)
			text, isText := value.(string)
			if index < 0 {
				return nil, errorf("%s: unknown key %s", where, strconv.QuoteToASCII(key))
			}
			if !isText {
				return nil, errorf("%s: %s must be a string", where, key)
			}
			fields[index] = text
		}
		entry := Entry{Ref: fields[0], Commit: fields[1], SHA256: fields[2], Scheme: fields[3]}
		ref, err := Parse(entry.Ref)
		if err != nil {
			return nil, errorf("%s: %v", where, err)
		}
		entry.Ref = ref.String()
		switch {
		case !fullCommit.MatchString(entry.Commit):
			return nil, errorf("%s: commit must be a full 40-character commit", where)
		case !sha256Shape.MatchString(entry.SHA256):
			return nil, errorf("%s: sha256 must be 64 lowercase hex characters", where)
		case entry.Scheme == "" || strings.ContainsFunc(entry.Scheme, func(r rune) bool { return r < 0x21 || r > 0x7e }):
			return nil, errorf("%s: scheme must name the fingerprint scheme, such as %q", where, Scheme)
		case seen[entry.Ref]:
			return nil, errorf("%s: %s is locked twice", where, entry.Ref)
		}
		seen[entry.Ref] = true
		lock.Entries = append(lock.Entries, entry)
	}
	return lock, nil
}

func indexOf(values []string, value string) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}
	return -1
}

func (l *Lock) Find(ref string) (Entry, bool) {
	for _, entry := range l.Entries {
		if entry.Ref == ref {
			return entry, true
		}
	}
	return Entry{}, false
}

func (l *Lock) render() []byte {
	entries := append([]Entry{}, l.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Ref < entries[j].Ref })
	var out strings.Builder
	for i, entry := range entries {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString("[[book]]\n")
		for _, field := range [][2]string{{"ref", entry.Ref}, {"commit", entry.Commit}, {"sha256", entry.SHA256}, {"scheme", entry.Scheme}} {
			out.WriteString(field[0] + " = " + strconv.Quote(field[1]) + "\n")
		}
	}
	return []byte(out.String())
}

func addToLock(path string, entry Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	guard, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(guard.Fd()), syscall.LOCK_UN)
	lock, err := ReadLock(path)
	if err != nil {
		return err
	}
	if existing, found := lock.Find(entry.Ref); found {
		if existing != entry {
			return errorf("%s: %s was locked to %s while this run fetched %s; run again", path, entry.Ref, existing.describe(), entry.describe())
		}
		return nil
	}
	lock.Entries = append(lock.Entries, entry)
	temporary, err := os.CreateTemp(filepath.Dir(path), ".stage.lock-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(lock.render())
	if err := errors.Join(writeErr, temporary.Chmod(0o644), temporary.Sync(), temporary.Close()); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
