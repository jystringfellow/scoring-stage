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

func decodeIncludes(data map[string]any, manifest string) ([]bookUse, error) {
	uses, err := decodeBookUses(data["includes"], "includes", manifest)
	if err != nil {
		return nil, err
	}
	for _, use := range uses {
		entry := use.text
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
	return uses, nil
}

type bookLoader struct {
	settings *Settings
	library  bookref.Library
	team     *bookref.Lock
	pin      func(bookref.Ref) (bookref.Book, error)
}

func (l bookLoader) open(ref bookref.Ref, alias string) (Book, error) {
	var fetched bookref.Book
	var err error
	if l.pin != nil {
		fetched, err = l.pin(ref)
	} else {
		fetched, err = l.library.Open(ref, l.team)
	}
	if err != nil {
		return Book{}, err
	}
	return loadFetched(l.settings.Agents, fetched, alias)
}

func loadFetched(agents Agents, fetched bookref.Book, alias string) (Book, error) {
	book, err := LoadBook(agents, fetched.Dir)
	if err != nil {
		return Book{}, errorf("%s: %v", fetched.Ref, err)
	}
	book.Ref, book.Alias, book.fingerprint = fetched.Ref.String(), alias, fetched.Fingerprint
	book.origin, book.commit = fetched.Ref, fetched.Commit
	return *book, nil
}

func (l bookLoader) include(parent Book, use bookUse) (Book, error) {
	book, err := l.includeBook(parent, use.text)
	if err != nil {
		return Book{}, err
	}
	return book.withArgs(use.args, parent.place())
}

func (l bookLoader) includeBook(parent Book, entry string) (Book, error) {
	ref, byRef, err := includedRef(parent, entry)
	if err != nil {
		return Book{}, err
	}
	if byRef {
		return l.open(ref, "")
	}
	book, err := LoadBook(l.settings.Agents, filepath.Join(parent.Path, filepath.FromSlash(entry)))
	if err != nil {
		return Book{}, err
	}
	return *book, nil
}

func includedRef(parent Book, entry string) (bookref.Ref, bool, error) {
	switch {
	case bookref.IsRef(entry):
		ref, err := bookref.Parse(entry)
		if err != nil {
			return bookref.Ref{}, false, errorf("%s: includes: %v", parent.place(), err)
		}
		return ref, true, nil
	case parent.Ref != "":
		joined := path.Clean(path.Join(parent.origin.Path, entry))
		if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") {
			return bookref.Ref{}, false, errorf("%s: include %s leaves the repository", parent.place(), Printable(entry))
		}
		sibling := parent.origin
		sibling.Path, sibling.Version = joined, parent.commit
		ref, err := bookref.Parse(sibling.String())
		if err != nil {
			return bookref.Ref{}, false, errorf("%s: includes: %v", parent.place(), err)
		}
		return ref, true, nil
	}
	return bookref.Ref{}, false, nil
}

func IncludedRefs(agents Agents, fetched bookref.Book) ([]bookref.Ref, error) {
	book, err := loadFetched(agents, fetched, "")
	if err != nil {
		return nil, err
	}
	var refs []bookref.Ref
	for _, use := range book.includes {
		ref, byRef, err := includedRef(book, use.text)
		if err != nil {
			return nil, err
		}
		if byRef {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (b Book) identity() string {
	if b.Ref != "" {
		return "ref:" + strings.ToLower(b.origin.Owner+"/"+b.origin.Repo) + "//" + b.origin.Path
	}
	return "dir:" + b.Path
}

func expandIncludes(roots []Book, repoOnlyRoot func(Book) bool, include func(Book, bookUse) (Book, error)) (active, repoOnly []Book, err error) {
	type reached struct {
		version, chain string
		index          int
	}
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
			merged, err := active[earlier.index].withArgsOf(book)
			if err != nil {
				return err
			}
			active[earlier.index] = merged
			return nil
		}
		seen[id] = reached{book.commit, strings.Join(chain, " → "), len(active)}
		active = append(active, book)
		onPath[id] = true
		for _, entry := range book.includes {
			next := strings.Join(append(chain, entry.text), " → ")
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
	for i, book := range active {
		if active[i], err = book.applyArgs(); err != nil {
			return nil, nil, err
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
	Approval    string
	Details     []string
}

func (s *Settings) describeFetched(books []Book) []IncludedBook {
	var described []IncludedBook
	for _, book := range books {
		described = append(described, IncludedBook{Ref: book.origin, Name: book.Name, Commit: book.commit, Fingerprint: book.fingerprint,
			Approval: book.approvalFingerprint(book.fingerprint), Details: summarizeBook(s.Agents, book)})
	}
	return described
}

func (s *Settings) IncludesOf(root bookref.Book, pin func(bookref.Ref) (bookref.Book, error)) ([]IncludedBook, error) {
	book, err := loadFetched(s.Agents, root, "")
	if err != nil {
		return nil, err
	}
	loader := bookLoader{settings: s, pin: pin}
	books, _, err := expandIncludes([]Book{book}, func(Book) bool { return false }, loader.include)
	if err != nil {
		return nil, err
	}
	var included []Book
	for _, reached := range books {
		if reached.Ref != "" && reached.identity() != book.identity() {
			included = append(included, reached)
		}
	}
	return s.describeFetched(included), nil
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
	return s.describeFetched(books), nil
}
