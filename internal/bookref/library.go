package bookref

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/source"
)

const (
	Scheme       = "book-tree/1"
	MaxFileBytes = 1 << 20
	MaxBookBytes = 8 << 20
	MaxFiles     = 1000
)

type Book struct {
	Ref         Ref
	Dir         string
	Commit      string
	Fingerprint string
}

type Library struct{ Home string }

func (l Library) LockPath() string  { return filepath.Join(l.Home, LockName) }
func (l Library) CacheRoot() string { return filepath.Join(l.Home, "books") }

type file struct {
	path    string
	exec    bool
	content []byte
}

func (l Library) Open(ref Ref, team *Lock) (Book, error) {
	key := ref.String()
	mine, err := ReadLock(l.LockPath())
	if err != nil {
		return Book{}, err
	}
	own, haveOwn := mine.Find(key)
	var theirs Entry
	haveTheirs := false
	if team != nil {
		theirs, haveTheirs = team.Find(key)
	}
	if haveOwn && haveTheirs && own != theirs {
		return Book{}, errorf("%s is locked to %s in %s and to %s in %s; to use the repository's, run `stage book update %s --from <repo>`; to give the repository yours, run `stage book lock <repo>` and commit it",
			key, own.describe(), mine.Path, theirs.describe(), team.Path, key)
	}
	book, entry, err := l.Pin(ref, mine, team)
	if err != nil {
		return Book{}, err
	}
	if !haveOwn {
		if err := addToLock(mine.Path, entry); err != nil {
			return Book{}, err
		}
	}
	return book, nil
}

func (l Library) Pin(ref Ref, locks ...*Lock) (Book, Entry, error) {
	key := ref.String()
	for _, lock := range locks {
		if lock == nil {
			continue
		}
		entry, found := lock.Find(key)
		if !found {
			continue
		}
		if entry.Scheme != Scheme {
			return Book{}, Entry{}, errorf("%s is locked in %s with fingerprint scheme %s, which this stage does not read (it reads %s); nothing was changed",
				key, lock.Path, strconv.QuoteToASCII(entry.Scheme), Scheme)
		}
		cached, err := l.cached(ref, entry.SHA256)
		if err != nil {
			return Book{}, Entry{}, err
		}
		if !cached {
			files, err := fetchCommit(ref, entry.Commit)
			if err != nil {
				return Book{}, Entry{}, err
			}
			if got := fingerprint(files); got != entry.SHA256 {
				return Book{}, Entry{}, errorf("%s at commit %s does not match its lock in %s (locked sha256 %s, fetched %s); nothing was changed",
					key, entry.Commit[:12], lock.Path, entry.SHA256[:12], got[:12])
			}
			if err := l.store(ref, entry.SHA256, files); err != nil {
				return Book{}, Entry{}, err
			}
		}
		return l.book(ref, entry), entry, nil
	}
	files, _, commit, err := fetchVersion(ref)
	if err != nil {
		return Book{}, Entry{}, err
	}
	entry := Entry{Ref: key, Commit: commit, SHA256: fingerprint(files), Scheme: Scheme}
	if err := l.store(ref, entry.SHA256, files); err != nil {
		return Book{}, Entry{}, err
	}
	return l.book(ref, entry), entry, nil
}

func (l Library) book(ref Ref, entry Entry) Book {
	return Book{Ref: ref, Dir: filepath.Join(l.CacheRoot(), entry.SHA256), Commit: entry.Commit, Fingerprint: entry.SHA256}
}

func (l Library) Cached(ref Ref) (Book, bool) {
	mine, err := ReadLock(l.LockPath())
	if err != nil {
		return Book{}, false
	}
	entry, found := mine.Find(ref.String())
	if !found || entry.Scheme != Scheme {
		return Book{}, false
	}
	if cached, err := l.cached(ref, entry.SHA256); err != nil || !cached {
		return Book{}, false
	}
	return Book{Ref: ref, Dir: filepath.Join(l.CacheRoot(), entry.SHA256), Commit: entry.Commit, Fingerprint: entry.SHA256}, true
}

type Fetched struct {
	Ref         Ref
	Tag         string
	Commit      string
	Fingerprint string
	files       []file
}

func (f Fetched) Entry() Entry {
	return Entry{Ref: f.Ref.String(), Commit: f.Commit, SHA256: f.Fingerprint, Scheme: Scheme}
}

func (l Library) Fetch(ref Ref) (Fetched, error) {
	files, tag, commit, err := fetchVersion(ref)
	if err != nil {
		return Fetched{}, err
	}
	return Fetched{Ref: ref, Tag: tag, Commit: commit, Fingerprint: fingerprint(files), files: files}, nil
}

func (l Library) FetchAt(ref Ref, commit string) (Fetched, error) {
	files, err := fetchCommit(ref, commit)
	if err != nil {
		return Fetched{}, err
	}
	return Fetched{Ref: ref, Commit: commit, Fingerprint: fingerprint(files), files: files}, nil
}

func (l Library) Keep(fetched Fetched) (Book, error) {
	if err := l.store(fetched.Ref, fetched.Fingerprint, fetched.files); err != nil {
		return Book{}, err
	}
	return Book{Ref: fetched.Ref, Dir: filepath.Join(l.CacheRoot(), fetched.Fingerprint), Commit: fetched.Commit, Fingerprint: fetched.Fingerprint}, nil
}

func (l Library) Unlocked() ([]string, error) {
	mine, err := ReadLock(l.LockPath())
	if err != nil {
		return nil, err
	}
	named := map[string]bool{}
	for _, entry := range mine.Entries {
		named[entry.SHA256] = true
	}
	entries, err := os.ReadDir(l.CacheRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var unlocked []string
	for _, entry := range entries {
		if !named[entry.Name()] {
			unlocked = append(unlocked, filepath.Join(l.CacheRoot(), entry.Name()))
		}
	}
	return unlocked, nil
}

func (l Library) Remove(path string) error {
	if filepath.Dir(path) != l.CacheRoot() {
		return errorf("%s is not in the book cache %s", path, l.CacheRoot())
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.Remove(path)
	}
	unseal(path)
	return os.RemoveAll(path)
}

func fingerprint(files []file) string {
	sorted := append([]file{}, files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00", Scheme)
	for _, f := range sorted {
		executable := 0
		if f.exec {
			executable = 1
		}
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", f.path, executable, len(f.content))
		hash.Write(f.content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func withScratch(ref Ref, run func(dir string) error) error {
	dir, err := os.MkdirTemp("", "stage-book-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if _, err := source.GitBytes(dir, "init", "--quiet", "--bare"); err != nil {
		return err
	}
	if err := run(dir); err != nil {
		var refused *Error
		if errors.As(err, &refused) {
			return err
		}
		return errorf("cannot fetch %s: %v; nothing was changed", ref, err)
	}
	return nil
}

func fetchVersion(ref Ref) (files []file, tag, commit string, err error) {
	err = withScratch(ref, func(dir string) error {
		if tag, commit, err = resolve(dir, ref); err != nil {
			return err
		}
		files, err = fetchInto(dir, ref, commit)
		return err
	})
	return files, tag, commit, err
}

func fetchCommit(ref Ref, commit string) (files []file, err error) {
	err = withScratch(ref, func(dir string) error {
		files, err = fetchInto(dir, ref, commit)
		return err
	})
	return files, err
}

func (r Ref) candidateTags() []string {
	if fullCommit.MatchString(r.Version) {
		return nil
	}
	return []string{r.Path + "/" + r.Version, r.Version}
}

func resolve(dir string, ref Ref) (tag, commit string, err error) {
	if fullCommit.MatchString(ref.Version) {
		return "", ref.Version, nil
	}
	var patterns []string
	for _, name := range ref.candidateTags() {
		patterns = append(patterns, "refs/tags/"+name, "refs/tags/"+name+"^{}", "refs/heads/"+name)
	}
	listing, err := source.GitBytes(dir, append([]string{"ls-remote", ref.URL()}, patterns...)...)
	if err != nil {
		return "", "", err
	}
	found := map[string]string{}
	for _, line := range strings.Split(string(listing), "\n") {
		if commit, name, ok := strings.Cut(line, "\t"); ok {
			found[name] = commit
		}
	}
	for _, name := range ref.candidateTags() {
		for _, listed := range []string{"refs/tags/" + name + "^{}", "refs/tags/" + name} {
			if commit := found[listed]; fullCommit.MatchString(commit) {
				return name, commit, nil
			}
		}
	}
	for _, name := range ref.candidateTags() {
		if found["refs/heads/"+name] != "" {
			return "", "", errorf("%s: %s is a branch on %s; a version is a tag or a full commit, so the book cannot change without you choosing it", ref, name, ref.repository())
		}
	}
	tags := ref.candidateTags()
	return "", "", errorf("%s: %s has neither tag %s nor tag %s; nothing was changed", ref, ref.repository(), tags[0], tags[1])
}

func fetchInto(dir string, ref Ref, commit string) ([]file, error) {
	if _, err := source.GitBytes(dir, "fetch", "--quiet", "--no-tags", "--depth", "1", ref.URL(), commit); err != nil {
		return nil, err
	}
	if got, err := source.GitBytes(dir, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}"); err != nil || strings.TrimSpace(string(got)) != commit {
		return nil, errorf("cannot fetch %s: %s did not send commit %s; nothing was changed", ref, ref.repository(), commit)
	}
	listing, err := source.GitBytes(dir, "ls-tree", "-r", "-l", "-z", "--full-tree", commit, "--", ref.Path+"/")
	if err != nil {
		return nil, err
	}
	prefix := ref.Path + "/"
	var files []file
	total := 0
	spelled := map[string]string{}
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, path, _ := strings.Cut(string(record), "\t")
		fields := strings.Fields(meta)
		relative := strings.TrimPrefix(path, prefix)
		if len(fields) != 4 || !strings.HasPrefix(path, prefix) {
			return nil, errorf("%s: cannot read the tree at %s", ref, commit[:12])
		}
		shown := strconv.QuoteToASCII(relative)
		switch fields[0] {
		case "120000":
			return nil, errorf("%s: %s is a symbolic link; a fetched book cannot contain links", ref, shown)
		case "160000":
			return nil, errorf("%s: %s is a submodule; a fetched book cannot contain submodules", ref, shown)
		case "100644", "100755":
		default:
			return nil, errorf("%s: %s has git mode %s, which a book cannot use", ref, shown, fields[0])
		}
		if err := checkRelative(relative); err != nil {
			return nil, errorf("%s: %s %v", ref, shown, err)
		}
		parts := strings.Split(relative, "/")
		for i := range parts {
			spelling := strings.Join(parts[:i+1], "/")
			lower := strings.ToLower(spelling)
			if other, seen := spelled[lower]; seen && other != spelling {
				return nil, errorf("%s: %s and %s differ only by case, which some filesystems cannot hold apart", ref, strconv.QuoteToASCII(other), strconv.QuoteToASCII(spelling))
			}
			spelled[lower] = spelling
		}
		size, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, errorf("%s: cannot read the size of %s", ref, shown)
		}
		if size > MaxFileBytes {
			return nil, errorf("%s: %s is %d bytes, over the %d-byte limit for a file in a book", ref, shown, size, MaxFileBytes)
		}
		if total += size; total > MaxBookBytes {
			return nil, errorf("%s: the book is over the %d-byte limit", ref, MaxBookBytes)
		}
		if len(files) == MaxFiles {
			return nil, errorf("%s: the book has more than %d files", ref, MaxFiles)
		}
		content, err := source.GitBytes(dir, "cat-file", "blob", fields[2])
		if err != nil {
			return nil, err
		}
		if len(content) != size {
			return nil, errorf("%s: %s is not the size the tree says", ref, shown)
		}
		files = append(files, file{path: relative, exec: fields[0] == "100755", content: content})
	}
	if len(files) == 0 {
		return nil, errorf("%s: %s has no directory %s at %s", ref, ref.repository(), ref.Path, commit[:12])
	}
	return files, nil
}

func checkRelative(path string) error {
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.EqualFold(segment, ".git") {
			return errorf("is not a path a book can hold")
		}
	}
	if strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' }) {
		return errorf("has control characters or a backslash in its name")
	}
	return nil
}

func (l Library) cached(ref Ref, sum string) (bool, error) {
	dir := filepath.Join(l.CacheRoot(), sum)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	files, err := readTree(dir)
	if err == nil && fingerprint(files) == sum {
		return true, nil
	}
	return false, errorf("the cached copy of %s at %s has changed since it was fetched; remove that directory and run again; nothing was changed", ref, dir)
}

func readTree(root string) ([]file, error) {
	var files []file
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errorf("%s is not a regular file", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		files = append(files, file{path: filepath.ToSlash(relative), exec: info.Mode()&0o100 != 0, content: content})
		return nil
	})
	return files, err
}

func (l Library) store(ref Ref, sum string, files []file) error {
	if cached, err := l.cached(ref, sum); err != nil || cached {
		return err
	}
	root := l.CacheRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	building, err := os.MkdirTemp(root, ".fetch-")
	if err != nil {
		return err
	}
	defer func() {
		if _, err := os.Lstat(building); err == nil {
			unseal(building)
			os.RemoveAll(building)
		}
	}()
	for _, f := range files {
		path := filepath.Join(building, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.exec {
			mode = 0o755
		}
		if err := os.WriteFile(path, f.content, mode); err != nil {
			return err
		}
	}
	written, err := readTree(building)
	if err != nil {
		return err
	}
	if fingerprint(written) != sum {
		return errorf("%s: the files written to %s do not match what was fetched; nothing was changed", ref, building)
	}
	if err := sealBelow(building); err != nil {
		return err
	}
	final := filepath.Join(root, sum)
	if err := os.Rename(building, final); err != nil {
		if cached, cacheErr := l.cached(ref, sum); cacheErr != nil || cached {
			return cacheErr
		}
		return err
	}
	return os.Chmod(final, 0o555)
}

func sealBelow(root string) error { return chmodBelow(root, 0o555, 0o444) }

func unseal(root string) {
	os.Chmod(root, 0o755)
	chmodBelow(root, 0o755, 0o644)
}

func chmodBelow(root string, executable, plain os.FileMode) error {
	var paths []string
	filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err == nil && path != root {
			paths = append(paths, path)
		}
		return nil
	})
	for i := len(paths) - 1; i >= 0; i-- {
		info, err := os.Lstat(paths[i])
		if err != nil {
			return err
		}
		mode := plain
		if info.IsDir() || info.Mode()&0o100 != 0 {
			mode = executable
		}
		if err := os.Chmod(paths[i], mode); err != nil {
			return err
		}
	}
	return nil
}
