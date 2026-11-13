package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/envfile"
	"github.com/jystringfellow/scoring-stage/internal/source"
)

type initFile struct {
	path, content, skipped    string
	exists, written, scaffold bool
}

func uniqueInitNames(names []string) []string {
	var result []string
	for _, name := range names {
		if !slices.Contains(result, name) {
			result = append(result, name)
		}
	}
	return result
}

func (a *App) cmdInit(args []string, home string) (int, error) {
	flags := a.newFlags("init", "[<repo>] [--agent NAME]... [--book NAME]... [--kit NAME]... [--dry-run] [--guide]")
	var agents, books, kits stringList
	flags.Var(&agents, "agent", "name an agent in the repository config (repeatable)")
	flags.Var(&books, "book", "name an installed book in the repository config (repeatable)")
	flags.Var(&kits, "kit", "scaffold a repository kit that grants nothing (repeatable)")
	dryRun := flags.Bool("dry-run", false, "print exactly what would be written, and check it without writing")
	printGuide := flags.Bool("guide", false, "print how to work out what a repository needs, without reading or writing files")
	positional, err := flags.parseOptional(args, 1)
	if err != nil {
		return 0, err
	}
	if *printGuide {
		_, err := fmt.Fprint(a.Stdout, guide)
		return 0, err
	}
	repo := "."
	if len(positional) == 1 {
		repo = positional[0]
	}
	repo = config.Resolve(repo)
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return 0, errorf("repository %s is not an existing directory", config.Printable(repo))
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, errorf("%s", config.Printable(err.Error()))
	}
	agents, books, kits = uniqueInitNames(agents), uniqueInitNames(books), uniqueInitNames(kits)
	for _, name := range kits {
		if err := config.CheckScaffoldKitName(name); err != nil {
			return 0, err
		}
	}
	files := []initFile{{path: config.RepoConfigPath, content: config.DraftRepoConfig(agents, books, kits)}}
	for _, name := range kits {
		path, content := config.ScaffoldKit(name)
		files = append(files, initFile{path: path, content: content, scaffold: true})
	}
	files[0].exists, err = inspectInitPath(repo, files[0].path)
	if err != nil {
		for i := range files {
			files[i].skipped = err.Error()
		}
		return a.finishInit(repo, settings, books, files, *dryRun, nil, nil, err)
	}
	checkDir := repo
	if files[0].exists {
		for i := 1; i < len(files); i++ {
			files[i].skipped = "config.toml exists; add \"./.scoring-stage/kits/" + kits[i-1] + "\" to its kits yourself"
		}
	} else if len(agents)+len(books)+len(kits) == 0 {
		files[0].skipped = "no --agent, --book or --kit given"
	} else {
		err = validateInitNames(settings, agents, books)
		if err == nil {
			for i := 1; i < len(files); i++ {
				files[i].exists, err = inspectInitPath(repo, files[i].path)
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			for i := range files {
				if !files[i].exists {
					files[i].skipped = err.Error()
				}
			}
			return a.finishInit(repo, settings, books, files, *dryRun, nil, nil, err)
		}
		temporary, err := os.MkdirTemp("", "stage-init-")
		if err != nil {
			return 0, err
		}
		defer os.RemoveAll(temporary)
		checkDir = config.Resolve(temporary)
		for _, name := range kits {
			if err = copyInitKit(repo, checkDir, filepath.Join(".scoring-stage", "kits", name)); err != nil {
				break
			}
		}
		if err == nil {
			for _, file := range files {
				if file.exists {
					continue
				}
				path := filepath.Join(checkDir, file.path)
				if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					break
				}
				if err = os.WriteFile(path, []byte(file.content), 0644); err != nil {
					break
				}
			}
		}
		if err != nil {
			err = errorf("%s", strings.ReplaceAll(err.Error(), checkDir, repo))
			for i := range files {
				if !files[i].exists {
					files[i].skipped = err.Error()
				}
			}
			return a.finishInit(repo, settings, books, files, *dryRun, nil, nil, err)
		}
	}
	composition, missing, existing, checkErr := a.checkInit(settings, repo, checkDir)
	if checkErr == nil && files[0].exists && existing != nil {
		if unrequested := existing.MissingRequests(agents, books, kits); len(unrequested) > 0 {
			files[0].skipped = "existing config does not contain: " + strings.Join(unrequested, ", ")
		}
	}
	if checkErr == nil && !*dryRun && !files[0].exists && files[0].skipped == "" {
		for i := range files {
			files[i].written, checkErr = writeInitFile(repo, files[i])
			if checkErr != nil {
				break
			}
			if !files[i].written {
				files[i].exists = true
				// A config created concurrently also keeps all scaffolds untouched.
				if i == 0 {
					for j := 1; j < len(files); j++ {
						files[j].skipped = "config.toml exists; not changed"
					}
					break
				}
			}
		}
		if checkErr == nil {
			composition, missing, _, checkErr = a.checkInit(settings, repo, repo)
		}
	}
	if checkErr != nil {
		for i := range files {
			if !files[i].exists && !files[i].written && files[i].skipped == "" {
				files[i].skipped = "check failed; nothing written"
			}
		}
	}
	return a.finishInit(repo, settings, books, files, *dryRun, composition, missing, checkErr)
}

func validateInitNames(settings *config.Settings, agents, books []string) error {
	known := settings.Agents.Names()
	for _, name := range agents {
		if !slices.Contains(known, name) {
			return errorf("unknown agent %s (known agents: %s)", config.Printable(name), strings.Join(known, ", "))
		}
	}
	var installed, descriptions []string
	for _, book := range settings.Books {
		installed = append(installed, book.Name)
		descriptions = append(descriptions, book.Name+" - "+book.Description)
	}
	if len(descriptions) == 0 {
		descriptions = []string{"none"}
	}
	for _, name := range books {
		if !slices.Contains(installed, name) {
			return errorf("repository asks for book %s, which is not installed (installed: %s); books are named, not located", config.Printable(name), config.Printable(strings.Join(descriptions, ", ")))
		}
	}
	return nil
}

// Check every component before opening files; never follow a repository's links.
func inspectInitPath(repo, relative string) (bool, error) {
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	path := repo
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, errorf("%s is a symbolic link", relative)
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return false, errorf("%s is not a regular file", relative)
			}
			return true, nil
		}
		if !info.IsDir() {
			return false, errorf("%s has a component that is not a directory", relative)
		}
	}
	return false, nil
}

func copyInitKit(repo, staged, relative string) error {
	root := filepath.Join(repo, relative)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		target := filepath.Join(staged, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errorf("%s is a symbolic link", rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errorf("%s is not a regular file", rel)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, content, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(target, info.Mode())
	})
}

func writeInitFile(repo string, file initFile) (bool, error) {
	exists, err := inspectInitPath(repo, file.path)
	if err != nil || exists {
		return false, err
	}
	// Root keeps a concurrent directory replacement from escaping the repository.
	root, err := os.OpenRoot(repo)
	if err != nil {
		return false, err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(file.path), 0755); err != nil {
		return false, err
	}
	if _, err := inspectInitPath(repo, file.path); err != nil {
		return false, err
	}
	output, err := root.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = output.WriteString(file.content)
	return true, errors.Join(err, output.Close())
}

func (a *App) checkInit(settings *config.Settings, repo, staged string) (*config.Composition, []config.Approval, *config.RepoConfig, error) {
	remap := func(text string) string { return strings.ReplaceAll(text, staged, repo) }
	fail := func(err error) (*config.Composition, []config.Approval, *config.RepoConfig, error) {
		return nil, nil, nil, errorf("%s", config.Printable(remap(err.Error())))
	}
	requested, err := config.LoadRepoConfig(staged)
	if err != nil {
		return fail(err)
	}
	names := []string{filepath.Base(repo)}
	repository := filepath.Base(repo)
	owner := ""
	if remote, err := source.Git(repo, "remote", "get-url", "origin"); err == nil {
		if github, err := source.GitHubRepo(remote); err == nil {
			names = append(names, github)
			repository = github
			owner, _, _ = strings.Cut(github, "/")
		}
	}
	composition, err := config.Compose(settings, config.Selection{Repo: requested, RepoNames: names, Gh: a.FindGh(), GitHubOwner: owner})
	if err != nil {
		return fail(err)
	}
	approvals, err := config.RepoApprovals(composition, repository)
	if err != nil {
		return fail(err)
	}
	missing, err := config.Unapproved(settings.Home, approvals)
	if err != nil {
		return fail(err)
	}
	for i := range missing {
		missing[i].Label = remap(missing[i].Label)
		for j := range missing[i].Details {
			missing[i].Details[j] = remap(missing[i].Details[j])
		}
	}
	for i := range composition.Warnings {
		composition.Warnings[i] = remap(composition.Warnings[i])
	}
	return composition, missing, requested, nil
}

func (a *App) finishInit(repo string, settings *config.Settings, named []string, files []initFile, dryRun bool, composition *config.Composition, missing []config.Approval, checkErr error) (int, error) {
	changed := false
	for _, file := range files {
		path := config.Printable(file.path)
		switch {
		case file.written:
			a.say("written %s", path)
			changed = true
		case file.exists:
			a.say("kept %s (exists; not changed)", path)
			if file.skipped != "" {
				a.say("%s", config.Printable(file.skipped))
			}
		case file.skipped != "":
			a.say("skipped %s: %s", path, config.Printable(file.skipped))
		case dryRun:
			a.say("would write %s", path)
			for _, line := range strings.Split(strings.TrimSuffix(file.content, "\n"), "\n") {
				fmt.Fprintf(a.Stdout, "    %s\n", line)
			}
		}
	}
	scaffolded := false
	for _, file := range files {
		if file.scaffold && (file.written || (dryRun && !file.exists && file.skipped == "")) {
			a.say("%s grants the sandbox nothing yet (capabilities: [])", config.Printable(file.path))
			scaffolded = true
		}
	}
	if scaffolded {
		a.say("to use it: add the capabilities this repository needs to the kit file; then stage up and run the repository's own build and tests inside with stage exec <slug> -- <command> to see what is still missing")
	}
	if files[0].skipped == "no --agent, --book or --kit given" {
		a.say("a sandbox for this repository would get the base image, stage's own kit and additions from your config")
		a.say("this repository adds no agents, books or kits; add --agent, --book or --kit to ask for more")
	}
	if !changed {
		a.say("nothing changed")
	}
	if len(settings.Books) == 0 {
		a.say("no installed books")
	} else {
		a.say("installed books not named:")
		count := 0
		for _, book := range settings.Books {
			if !slices.Contains(named, book.Name) {
				a.say("  %s - %s", config.Printable(book.Name), config.Printable(book.Description))
				count++
			}
		}
		if count == 0 {
			a.say("  none")
		}
	}
	a.say("known agents: %s", strings.Join(settings.Agents.Names(), ", "))
	if checkErr != nil {
		a.say("check failed")
	} else {
		a.say("check passed")
		a.say("agents: %s", initNames(composition.Agents))
		a.say("books: %s", initNames(composition.Books))
		var handles []string
		for _, kit := range composition.AllKits() {
			handles = append(handles, envfile.Handle(kit))
		}
		a.say("kits: %s", initNames(handles))
		a.say("takes: %s", initNames(composition.Takes))
		if len(composition.AWSProfiles) > 0 {
			a.say("AWS profiles: %s", initNames(composition.AWSProfiles))
		}
		for _, warning := range composition.Warnings {
			a.say("WARNING: %s", config.Printable(warning))
		}
		if len(missing) > 0 {
			printApprovals(a.Stdout, missing)
		} else {
			a.say("nothing needs approving")
		}
	}
	a.say("next: review the files and commit them on the branch sandboxes start from; stage reads the config from that branch")
	a.say("stage ship refuses .scoring-stage/ unless --allow-protected-paths; commit these files directly on that branch")
	a.say("then: stage up %s <slug>", config.Printable(config.ShellQuote(repo)))
	if checkErr != nil {
		return 0, errorf("%s", config.Printable(checkErr.Error()))
	}
	return 0, nil
}

func initNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return config.Printable(strings.Join(names, ", "))
}
