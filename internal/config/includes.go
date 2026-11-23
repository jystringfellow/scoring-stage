package config

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/bookref"
)

const (
	MaxIncludeDepth  = 8
	MaxIncludedBooks = 50
)

func decodeIncludes(data map[string]any, manifest string) ([]string, error) {
	entries, err := optionalStrings(data, "includes", manifest)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		switch {
		case bookref.IsRef(entry):
			if _, err := bookref.Parse(entry); err != nil {
				return nil, errorf("%s: includes: %v", manifest, err)
			}
		case (strings.HasPrefix(entry, "./") || strings.HasPrefix(entry, "../")) && !HasControl(entry):
		default:
			return nil, errorf("%s: includes: %s must be a book reference or a ./ or ../ path", manifest, Printable(entry))
		}
	}
	return entries, nil
}

type bookLoader struct {
	settings *Settings
	library  bookref.Library
	team     *bookref.Lock
}

func (l bookLoader) open(ref bookref.Ref, alias string) (Book, error) {
	fetched, err := l.library.Open(ref, l.team)
	if err != nil {
		return Book{}, err
	}
	book, err := LoadBook(l.settings.Agents, fetched.Dir)
	if err != nil {
		return Book{}, errorf("%s: %v", ref, err)
	}
	book.Ref, book.Alias, book.fingerprint = ref.String(), alias, fetched.Fingerprint
	book.origin, book.commit = ref, fetched.Commit
	return *book, nil
}

func (l bookLoader) include(parent Book, entry string) (Book, error) {
	switch {
	case bookref.IsRef(entry):
		ref, err := bookref.Parse(entry)
		if err != nil {
			return Book{}, errorf("%s: includes: %v", parent.place(), err)
		}
		return l.open(ref, "")
	case parent.Ref != "":
		joined := path.Clean(path.Join(parent.origin.Path, entry))
		if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") {
			return Book{}, errorf("%s: include %s leaves the repository", parent.place(), Printable(entry))
		}
		sibling := parent.origin
		sibling.Path, sibling.Version = joined, parent.commit
		ref, err := bookref.Parse(sibling.String())
		if err != nil {
			return Book{}, errorf("%s: includes: %v", parent.place(), err)
		}
		return l.open(ref, "")
	default:
		book, err := LoadBook(l.settings.Agents, filepath.Join(parent.Path, filepath.FromSlash(entry)))
		if err != nil {
			return Book{}, err
		}
		return *book, nil
	}
}

func (b Book) identity() string {
	if b.Ref != "" {
		return "ref:" + strings.ToLower(b.origin.Owner+"/"+b.origin.Repo) + "//" + b.origin.Path
	}
	return "dir:" + b.Path
}

func expandIncludes(roots []Book, repoOnlyRoot func(Book) bool, include func(Book, string) (Book, error)) (active, repoOnly []Book, err error) {
	type reached struct{ version, chain string }
	seen := map[string]reached{}
	onPath := map[string]bool{}
	children := map[string][]string{}
	var walk func(book Book, chain []string) error
	walk = func(book Book, chain []string) error {
		id := book.identity()
		chain = append(append([]string{}, chain...), book.place())
		if onPath[id] {
			return errorf("book includes form a cycle: %s", strings.Join(chain, " → "))
		}
		if earlier, found := seen[id]; found {
			if earlier.version != book.commit {
				return errorf("two versions of one book are included: %s and %s; include one version", earlier.chain, strings.Join(chain, " → "))
			}
			return nil
		}
		seen[id] = reached{book.commit, strings.Join(chain, " → ")}
		active = append(active, book)
		onPath[id] = true
		for _, entry := range book.includes {
			next := strings.Join(append(chain, entry), " → ")
			if len(chain) >= MaxIncludeDepth {
				return errorf("an include chain is longer than %d books: %s; nothing was fetched past it", MaxIncludeDepth, next)
			}
			if len(active) >= MaxIncludedBooks {
				return errorf("includes reach more than %d books: %s; nothing was fetched past it", MaxIncludedBooks, next)
			}
			child, err := include(book, entry)
			if err != nil {
				return err
			}
			children[id] = append(children[id], child.identity())
			if err := walk(child, chain); err != nil {
				return err
			}
		}
		onPath[id] = false
		return nil
	}
	for _, root := range roots {
		if err := walk(root, nil); err != nil {
			return nil, nil, err
		}
	}
	reachedOtherwise := map[string]bool{}
	var mark func(id string)
	mark = func(id string) {
		if reachedOtherwise[id] {
			return
		}
		reachedOtherwise[id] = true
		for _, child := range children[id] {
			mark(child)
		}
	}
	for _, root := range roots {
		if !repoOnlyRoot(root) {
			mark(root.identity())
		}
	}
	names := map[string]Book{}
	for _, book := range active {
		if other, taken := names[book.Name]; taken {
			return nil, nil, errorf("books %s and %s are both named %s", other.place(), book.place(), book.Name)
		}
		names[book.Name] = book
		if !reachedOtherwise[book.identity()] {
			repoOnly = append(repoOnly, book)
		}
	}
	return active, repoOnly, nil
}

type IncludedBook struct {
	Ref         bookref.Ref
	Name        string
	Commit      string
	Fingerprint string
	Details     []string
}

func (s *Settings) FetchWithIncludes(refs []bookref.Ref, team *bookref.Lock) ([]IncludedBook, error) {
	library := bookref.Library{Home: s.Home}
	mine, err := bookref.ReadLock(library.LockPath())
	if err != nil {
		return nil, err
	}
	onlyTheirs := &bookref.Lock{Path: team.Path}
	for _, entry := range team.Entries {
		if _, yours := mine.Find(entry.Ref); !yours {
			onlyTheirs.Entries = append(onlyTheirs.Entries, entry)
		}
	}
	loader := bookLoader{settings: s, library: library, team: onlyTheirs}
	var roots []Book
	for _, ref := range refs {
		book, err := loader.open(ref, "")
		if err != nil {
			return nil, err
		}
		roots = append(roots, book)
	}
	books, _, err := expandIncludes(roots, func(Book) bool { return false }, loader.include)
	if err != nil {
		return nil, err
	}
	var fetched []IncludedBook
	for _, book := range books {
		fetched = append(fetched, IncludedBook{Ref: book.origin, Name: book.Name, Commit: book.commit, Fingerprint: book.fingerprint, Details: summarizeBook(s.Agents, book)})
	}
	return fetched, nil
}
