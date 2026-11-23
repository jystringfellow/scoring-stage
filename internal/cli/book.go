package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/bookref"
	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/source"
)

func (a *App) cmdBook(args []string, home string) (int, error) {
	if len(args) == 0 {
		return 0, &usageError{"usage: stage book list|prune|update|lock"}
	}
	switch args[0] {
	case "list":
		return 0, a.bookList(args[1:], home)
	case "prune":
		return 0, a.bookPrune(args[1:], home)
	case "update":
		return 0, a.bookUpdate(args[1:], home)
	case "lock":
		return 0, a.bookLock(args[1:], home)
	}
	return 0, &usageError{"unknown book action: " + config.Printable(args[0])}
}

func short(commit string) string { return commit[:min(12, len(commit))] }

func (a *App) bookList(args []string, home string) error {
	if _, err := a.newFlags("book list", "").parse(args); err != nil {
		return err
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return err
	}
	library := bookref.Library{Home: settings.Home}
	mine, err := bookref.ReadLock(library.LockPath())
	if err != nil {
		return err
	}
	aliases := map[string][]string{}
	for _, reference := range settings.BookReferences() {
		if reference.Alias != "" {
			aliases[reference.Ref.String()] = append(aliases[reference.Ref.String()], reference.Alias)
		}
	}
	listed := false
	for _, entry := range mine.Entries {
		ref, _ := bookref.Parse(entry.Ref)
		name, state := "?", "not cached"
		if book, cached := library.Cached(ref); cached {
			state = "cached"
			if described, _, err := config.DescribeBook(settings.Agents, book.Dir); err == nil {
				name = described
			}
		}
		line := fmt.Sprintf("%s  %s  commit %s  %s", name, entry.Ref, short(entry.Commit), state)
		for _, alias := range aliases[entry.Ref] {
			line += "  (short name " + alias + ")"
		}
		fmt.Fprintln(a.Stdout, config.Printable(line))
		listed = true
	}
	for _, reference := range settings.BookReferences() {
		if _, locked := mine.Find(reference.Ref.String()); !locked {
			fmt.Fprintf(a.Stdout, "%s  not fetched yet; stage up fetches it when a sandbox needs it\n", reference.Ref)
			listed = true
		}
	}
	unlocked, err := library.Unlocked()
	if err != nil {
		return err
	}
	for _, path := range unlocked {
		fmt.Fprintf(a.Stdout, "%s  cached but not in your lock; stage book prune removes it\n", config.Printable(path))
		listed = true
	}
	if !listed {
		a.say("no books by reference")
	}
	return nil
}

func (a *App) bookPrune(args []string, home string) error {
	if _, err := a.newFlags("book prune", "").parse(args); err != nil {
		return err
	}
	library := bookref.Library{Home: home}
	unlocked, err := library.Unlocked()
	if err != nil {
		return err
	}
	if len(unlocked) == 0 {
		a.say("nothing to prune: every cached book is in your lock")
		return nil
	}
	for _, path := range unlocked {
		a.say("removing %s", config.Printable(path))
	}
	for _, path := range unlocked {
		if err := library.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) requireTerminal(command, why string) error {
	if a.approvalReadOnly || !a.IsTerminal() {
		return errorf("stage %s %s, so it asks you in a terminal and has no flag that answers for you; run it yourself", command, why)
	}
	return nil
}

type bookTarget struct {
	ref      bookref.Ref
	inConfig bool
}

func bookTargets(settings *config.Settings, names []string) ([]bookTarget, error) {
	references := settings.BookReferences()
	inConfig := func(ref bookref.Ref) bool {
		for _, reference := range references {
			if reference.Ref == ref {
				return true
			}
		}
		return false
	}
	if len(names) == 0 {
		var targets []bookTarget
		for _, reference := range references {
			targets = append(targets, bookTarget{reference.Ref, true})
		}
		return targets, nil
	}
	var targets []bookTarget
	for _, name := range names {
		if bookref.IsRef(name) {
			ref, err := bookref.Parse(name)
			if err != nil {
				return nil, err
			}
			targets = append(targets, bookTarget{ref, inConfig(ref)})
			continue
		}
		found := false
		for _, reference := range references {
			if reference.Alias == name {
				targets, found = append(targets, bookTarget{reference.Ref, true}), true
				break
			}
		}
		for _, book := range settings.Books {
			if !found && book.Ref != "" && book.Name == name {
				ref, _ := bookref.Parse(book.Ref)
				targets, found = append(targets, bookTarget{ref, inConfig(ref)}), true
			}
		}
		if !found {
			return nil, errorf("no book by reference in your config is named %s; give its reference or its short name", config.Printable(name))
		}
	}
	return targets, nil
}

func (a *App) bookUpdate(args []string, home string) error {
	fs := a.newFlags("book update", "[NAME|REF]... [--to VERSION] [--from REPO]")
	to := fs.String("to", "", "move the reference to this tag or full commit, and change it in your config")
	from := fs.String("from", "", "take the entry from this repository's .scoring-stage/stage.lock")
	names, err := fs.parseOptional(args, 1<<16)
	if err != nil {
		return err
	}
	if *to != "" && *from != "" {
		return &usageError{"--to and --from cannot be used together"}
	}
	if err := a.requireTerminal("book update", "records your approval of the book's new content"); err != nil {
		return err
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return err
	}
	targets, err := bookTargets(settings, names)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		a.say("your config names no books by reference")
		return nil
	}
	if *to != "" && len(targets) != 1 {
		return &usageError{"--to moves one reference; name exactly one book"}
	}
	var team *bookref.Lock
	if *from != "" {
		dir := config.Resolve(config.ExpandUser(*from))
		if team, err = bookref.ReadLock(filepath.Join(dir, config.RepoLockPath)); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if err := a.updateBook(settings, target, *to, team); err != nil {
			return err
		}
	}
	return nil
}

func quotedOccurrences(text, value string) int {
	return strings.Count(text, `"`+value+`"`) + strings.Count(text, `'`+value+`'`)
}

func (a *App) updateBook(settings *config.Settings, target bookTarget, to string, team *bookref.Lock) error {
	library := bookref.Library{Home: settings.Home}
	configPath := filepath.Join(settings.Home, "config.toml")
	key := target.ref.String()
	next := target.ref
	if to != "" {
		moved, err := bookref.Parse(strings.TrimSuffix(key, "@"+target.ref.Version) + "@" + to)
		if err != nil {
			return err
		}
		next = moved
		if !target.inConfig {
			return errorf("%s is not in your config, so --to has nothing to change; nothing was changed; if a repository names it, edit it by hand there, to:\n  %s", key, next)
		}
		text, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		if count := quotedOccurrences(string(text), key); count != 1 {
			return errorf("%s appears %d times in %s, so stage cannot tell which to change; nothing was changed; edit it by hand, to:\n  %s", key, count, configPath, next)
		}
	}
	mine, err := bookref.ReadLock(library.LockPath())
	if err != nil {
		return err
	}
	current, haveCurrent := mine.Find(key)

	var fetched bookref.Fetched
	switch {
	case team != nil:
		entry, found := team.Find(key)
		if !found {
			return errorf("%s has no entry for %s; nothing was changed", team.Path, key)
		}
		if fetched, err = library.FetchAt(target.ref, entry.Commit); err != nil {
			return err
		}
		if fetched.Fingerprint != entry.SHA256 {
			return errorf("%s at commit %s does not match its entry in %s; nothing was changed", key, short(entry.Commit), team.Path)
		}
	default:
		if fetched, err = library.Fetch(next); err != nil {
			return err
		}
	}
	nextKey := next.String()
	if existing, found := mine.Find(nextKey); to == "" && found && existing == fetched.Entry() {
		a.say("%s is unchanged at commit %s", nextKey, short(existing.Commit))
		return nil
	}
	switch {
	case team != nil && haveCurrent:
		a.say("%s: the repository's lock has commit %s; yours has commit %s", key, short(fetched.Commit), short(current.Commit))
	case to == "" && haveCurrent && current.Commit != fetched.Commit:
		a.say("%s: tag %s moved: it was commit %s and is now commit %s", key, target.ref.Version, short(current.Commit), short(fetched.Commit))
	case to != "":
		a.say("%s moves to %s at commit %s", key, nextKey, short(fetched.Commit))
	}

	book, err := library.Keep(fetched)
	if err != nil {
		return err
	}
	name, details, err := config.DescribeBook(settings.Agents, book.Dir)
	if err != nil {
		return err
	}
	var before []string
	if old, cached := library.Cached(target.ref); cached {
		if _, oldDetails, err := config.DescribeBook(settings.Agents, old.Dir); err == nil {
			before = oldDetails
		}
	}
	a.showBookChange(before, details)
	if !a.confirm(fmt.Sprintf("Use %s at commit %s, and approve what it does?", nextKey, short(fetched.Commit))) {
		return errorf("not updated; nothing was changed")
	}

	approval := config.Approval{Label: "book " + name + " (" + nextKey + ")", Fingerprint: fetched.Fingerprint, Details: details}
	err = writeTogether(
		fileWrite{config.TrustPath(settings.Home), func() error {
			missing, err := config.Unapproved(settings.Home, []config.Approval{approval})
			if err != nil || len(missing) == 0 {
				return err
			}
			return config.Approve(settings.Home, missing)
		}},
		fileWrite{configPath, func() error {
			if to == "" {
				return nil
			}
			text, err := os.ReadFile(configPath)
			if err != nil {
				return err
			}
			if quotedOccurrences(string(text), key) != 1 {
				return errorf("%s changed while updating; nothing was changed", configPath)
			}
			updated := strings.Replace(strings.Replace(string(text), `"`+key+`"`, `"`+nextKey+`"`, 1), `'`+key+`'`, `'`+nextKey+`'`, 1)
			return os.WriteFile(configPath, []byte(updated), 0o644)
		}},
		fileWrite{library.LockPath(), func() error {
			return bookref.UpdateLock(library.LockPath(), func(lock *bookref.Lock) error {
				lock.Set(fetched.Entry())
				return nil
			})
		}},
	)
	if err != nil {
		return err
	}
	a.say("%s is now commit %s; approved %s", nextKey, short(fetched.Commit), short(fetched.Fingerprint))
	return nil
}

func (a *App) showBookChange(before, after []string) {
	if before == nil {
		a.say("what the book does:")
		for _, line := range after {
			fmt.Fprintf(a.Stdout, "  + %s\n", config.Printable(line))
		}
		return
	}
	changed := false
	for _, line := range before {
		if !slices.Contains(after, line) {
			fmt.Fprintf(a.Stdout, "  - %s\n", config.Printable(line))
			changed = true
		}
	}
	for _, line := range after {
		if !slices.Contains(before, line) {
			fmt.Fprintf(a.Stdout, "  + %s\n", config.Printable(line))
			changed = true
		}
	}
	if !changed {
		a.say("what the book does is unchanged; other files in it changed")
	}
}

type fileWrite struct {
	path  string
	write func() error
}

func writeTogether(writes ...fileWrite) error {
	var restores []func()
	for _, change := range writes {
		restores = append(restores, keepCopy(change.path))
		if err := change.write(); err != nil {
			for j := len(restores) - 1; j >= 0; j-- {
				restores[j]()
			}
			return fmt.Errorf("%w; nothing was changed", err)
		}
	}
	return nil
}

func keepCopy(path string) func() {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return func() { os.Remove(path) }
	}
	mode := fs.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	return func() {
		if err == nil {
			os.WriteFile(path, content, mode)
		}
	}
}

func (a *App) bookLock(args []string, home string) error {
	positional, err := a.newFlags("book lock", "<repo>").parse(args, "repo")
	if err != nil {
		return err
	}
	if err := a.requireTerminal("book lock", "writes a repository's lock for you to commit"); err != nil {
		return err
	}
	dir := config.Resolve(config.ExpandUser(positional[0]))
	top, err := source.Git(dir, "rev-parse", "--show-toplevel")
	if err != nil || config.Resolve(top) != dir {
		return errorf("%s is not the top level of a git checkout", config.Printable(dir))
	}
	repo, err := config.LoadRepoConfig(dir)
	if err != nil {
		return err
	}
	if repo == nil || len(repo.BookReferences()) == 0 {
		a.say("%s names no books by reference; nothing to lock", config.RepoConfigPath)
		return nil
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return err
	}
	books, err := settings.FetchWithIncludes(repo.BookReferences())
	if err != nil {
		return err
	}
	var approvals []config.Approval
	for _, book := range books {
		approvals = append(approvals, config.Approval{Label: "book " + book.Name + " (" + book.Ref.String() + ")", Fingerprint: book.Fingerprint, Details: book.Details})
	}
	missing, err := config.Unapproved(settings.Home, approvals)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		printApprovals(a.Stderr, missing)
		if !a.confirm("Approve these books, and lock them for the repository?") {
			return errorf("not approved; the repository's lock was not written")
		}
		if err := config.Approve(settings.Home, missing); err != nil {
			return err
		}
	}
	team := repo.Lock()
	changed := false
	for _, book := range books {
		key := book.Ref.String()
		entry := bookref.Entry{Ref: key, Commit: book.Commit, SHA256: book.Fingerprint, Scheme: bookref.Scheme}
		old, had := team.Find(key)
		switch {
		case !had:
			a.say("added %s at commit %s", key, short(entry.Commit))
		case old != entry:
			a.say("changed %s from commit %s to commit %s", key, short(old.Commit), short(entry.Commit))
		default:
			a.say("%s is unchanged at commit %s", key, short(entry.Commit))
			continue
		}
		team.Set(entry)
		changed = true
	}
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(team.Path), 0o755); err != nil {
		return err
	}
	if err := bookref.WriteLock(team.Path, team); err != nil {
		return err
	}
	a.say("wrote %s; review it with git diff and commit it", config.Printable(team.Path))
	return nil
}
