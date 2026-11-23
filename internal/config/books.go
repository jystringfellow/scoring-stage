package config

import (
	"path/filepath"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/bookref"
)

type bookEntry struct {
	alias string
	ref   bookref.Ref
}

func (s *Settings) decodeBooks(value any, path string) error {
	type entry struct{ alias, text string }
	var entries []entry
	switch typed := value.(type) {
	case nil:
	case map[string]any:
		for _, alias := range sortedKeys(typed) {
			text, ok := typed[alias].(string)
			if !ok {
				return errorf("%s [books]: %s must be a book reference or a book's directory", path, Printable(alias))
			}
			if !bookName.MatchString(alias) {
				return errorf("%s [books]: short name %q must match %s", path, Printable(alias), bookName)
			}
			entries = append(entries, entry{alias, text})
		}
	default:
		list, err := asStrings(value)
		if err != nil {
			return errorf("%s: books must be a list of book directories or a [books] table", path)
		}
		for _, text := range list {
			entries = append(entries, entry{"", text})
		}
	}
	for _, e := range entries {
		if bookref.IsRef(e.text) {
			ref, err := bookref.Parse(e.text)
			if err != nil {
				return errorf("%s: %v", path, err)
			}
			s.bookRefs = append(s.bookRefs, bookEntry{alias: e.alias, ref: ref})
			if book, found := s.cachedBook(ref); found {
				book.Alias = e.alias
				s.Books = append(s.Books, book)
			}
			continue
		}
		book, err := LoadBook(s.Agents, bookDirectory(s.Home, e.text))
		if err != nil {
			return err
		}
		book.Alias = e.alias
		s.Books = append(s.Books, *book)
	}
	return checkBookNames(s.Books, path)
}

func (s *Settings) cachedBook(ref bookref.Ref) (Book, bool) {
	fetched, found := bookref.Library{Home: s.Home}.Cached(ref)
	if !found {
		return Book{}, false
	}
	book, err := LoadBook(s.Agents, fetched.Dir)
	if err != nil {
		return Book{}, false
	}
	book.Ref, book.fingerprint = ref.String(), fetched.Fingerprint
	return *book, true
}

func (s *Settings) BookNames() []string {
	var names []string
	add := func(name string) {
		if name != "" && !contains(names, name) {
			names = append(names, name)
		}
	}
	for _, book := range s.Books {
		add(book.Name)
		add(book.Alias)
	}
	for _, entry := range s.bookRefs {
		add(entry.alias)
	}
	return names
}

func (b Book) place() string {
	if b.Ref != "" {
		return b.Ref
	}
	return b.Path
}

func checkBookNames(books []Book, where string) error {
	byName := map[string]Book{}
	for _, book := range books {
		if other, taken := byName[book.Name]; taken {
			return errorf("%s: books %s and %s are both named %s", where, other.place(), book.place(), book.Name)
		}
		byName[book.Name] = book
	}
	for _, book := range books {
		if other, taken := byName[book.Alias]; book.Alias != "" && taken && other.Name != book.Name {
			return errorf("%s [books]: short name %s is another book's name (%s); choose another short name", where, book.Alias, other.place())
		}
	}
	return nil
}

func activeBooks(settings *Settings, selection Selection) (active, repoOnly []Book, err error) {
	var team *bookref.Lock
	if selection.Repo != nil {
		team = selection.Repo.lock
	}
	loader := bookLoader{settings: settings, library: bookref.Library{Home: settings.Home}, team: team}
	open := loader.open
	var available []Book
	for _, book := range settings.Books {
		if book.Ref == "" {
			available = append(available, book)
		}
	}
	for _, entry := range settings.bookRefs {
		book, err := open(entry.ref, entry.alias)
		if err != nil {
			return nil, nil, err
		}
		available = append(available, book)
	}
	if err := checkBookNames(available, filepath.Join(settings.Home, "config.toml")); err != nil {
		return nil, nil, err
	}
	find := func(name string) (Book, bool) {
		for _, book := range available {
			if book.Alias == name {
				return book, true
			}
		}
		for _, book := range available {
			if book.Name == name {
				return book, true
			}
		}
		return Book{}, false
	}
	installedList := strings.Join(settings.BookNames(), ", ")
	if installedList == "" {
		installedList = "none"
	}

	var asked []Book
	byCommandLine, byRepo, byRule := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, entry := range selection.Books {
		var book Book
		switch {
		case bookref.IsRef(entry):
			ref, err := bookref.Parse(entry)
			if err != nil {
				return nil, nil, errorf("--book: %v", err)
			}
			if book, err = open(ref, ""); err != nil {
				return nil, nil, err
			}
		case looksLikeName(strings.ReplaceAll(entry, ".", "")) && !strings.HasPrefix(entry, "."):
			var found bool
			if book, found = find(entry); !found {
				return nil, nil, errorf("--book %s: no installed book has that name (installed: %s); pass the book's directory or add it to books in config.toml", entry, installedList)
			}
		default:
			loaded, err := LoadBook(settings.Agents, ExpandUser(entry))
			if err != nil {
				return nil, nil, err
			}
			book = *loaded
		}
		byCommandLine[book.Path] = true
		asked = append(asked, book)
	}
	if repo := selection.Repo; repo != nil {
		for _, entry := range repo.books {
			var book Book
			if bookref.IsRef(entry) {
				if settings.Managed.RepoKitsDenied() {
					return nil, nil, repoKitsRefused(settings, repo)
				}
				ref, err := bookref.Parse(entry)
				if err != nil {
					return nil, nil, errorf("%s: %v", repo.Path, err)
				}
				if book, err = open(ref, ""); err != nil {
					return nil, nil, err
				}
			} else {
				var found bool
				if book, found = find(entry); !found {
					return nil, nil, errorf("%s asks for book %s, which is not installed (installed: %s); add it to books in ~/.scoring-stage/config.toml", repo.Path, entry, installedList)
				}
			}
			byRepo[book.Path] = true
			asked = append(asked, book)
		}
	}
	for _, book := range available {
		for _, r := range book.repos {
			if matchesAny(selection.RepoNames, r.patterns) {
				byRule[book.Path] = true
			}
		}
	}

	var roots []Book
	seen := map[string]Book{}
	add := func(book Book) error {
		if other, taken := seen[book.Name]; taken {
			if other.Path == book.Path {
				return nil
			}
			return errorf("books %s and %s are both named %s", other.place(), book.place(), book.Name)
		}
		seen[book.Name] = book
		roots = append(roots, book)
		return nil
	}
	for _, book := range available {
		if byCommandLine[book.Path] || byRepo[book.Path] || byRule[book.Path] {
			if err := add(book); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, book := range asked {
		if err := add(book); err != nil {
			return nil, nil, err
		}
	}
	return expandIncludes(roots, func(book Book) bool {
		return byRepo[book.Path] && !byCommandLine[book.Path] && !byRule[book.Path]
	}, loader.include)
}

func repoKitsRefused(settings *Settings, repo *RepoConfig) error {
	return errorf("%s: the repository's kits and books are refused by %s (repo_kits = %q); ask for books with --book",
		repo.Path, settings.Managed.where(), RepoKitsDeny)
}

type BookReference struct {
	Alias string
	Ref   bookref.Ref
}

func (s *Settings) BookReferences() []BookReference {
	var references []BookReference
	for _, entry := range s.bookRefs {
		references = append(references, BookReference{Alias: entry.alias, Ref: entry.ref})
	}
	return references
}

func DescribeBook(agents Agents, dir string) (string, []string, error) {
	book, err := LoadBook(agents, dir)
	if err != nil {
		return "", nil, err
	}
	return book.Name, summarizeBook(agents, *book), nil
}

func TrustPath(home string) string { return trustFile(home) }
