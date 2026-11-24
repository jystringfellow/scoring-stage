package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func argsBook(t *testing.T, dir string) string {
	t.Helper()
	path := book(t, dir, "tool", "kits = [\"./kits/tool\", {source = \"./kits/other\", args = {keep = \"1\"}}]\n")
	testutil.WriteFile(t, filepath.Join(path, "kits", "tool", "tool.yaml"), dedent(`
		args:
		  channel:
		    default: "10.0"
		    pattern: '^[0-9]+\.[0-9]+$'
		  version:
		    default: "1"
		    pattern: '[0-9]+'
		  loose:
		    default: ""
		capabilities: []
	`))
	testutil.WriteFile(t, filepath.Join(path, "kits", "other", "other.yaml"), dedent(`
		args:
		  keep:
		    default: "0"
		    pattern: '^[0-9]$'
		capabilities: []
	`))
	return path
}

func kitArgs(t *testing.T, composition *Composition, name string) map[string]any {
	t.Helper()
	for _, kit := range composition.Kits {
		if filepath.Base(kit.Source) == name {
			return kit.Args
		}
	}
	t.Fatalf("no kit %s in %v", name, sources(composition.Kits))
	return nil
}

func TestABookNamedWithArgumentsGivesThemToItsKitAndAPlainlyNamedBookKeepsItsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := argsBook(t, dir)
	for _, config := range []string{
		fmt.Sprintf("books = [{source = %q, args = {tool = {channel = \"11.0\"}}}]\n", path),
		fmt.Sprintf("[books]\nmine = {source = %q, args = {tool = {channel = \"11.0\"}}}\n", path),
	} {
		home := filepath.Join(t.TempDir(), "home")
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), config)
		composition := composeWith(t, load(t, home), Selection{Books: []string{"tool"}})
		if got := kitArgs(t, composition, "tool"); !reflect.DeepEqual(got, map[string]any{"channel": "11.0"}) {
			t.Errorf("%s: tool kit args = %v", config, got)
		}
		if got := kitArgs(t, composition, "other"); !reflect.DeepEqual(got, map[string]any{"keep": "1"}) {
			t.Errorf("%s: the book's own args for another kit changed: %v", config, got)
		}
	}
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [%q]\n", path))
	composition := composeWith(t, load(t, home), Selection{Books: []string{"tool"}})
	if got := kitArgs(t, composition, "tool"); got != nil {
		t.Errorf("a plainly named book's kit got args %v", got)
	}
}

func TestABookArgumentOverridesTheBooksOwnValueForThatArgument(t *testing.T) {
	path := argsBook(t, t.TempDir())
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [{source = %q, args = {other = {keep = \"7\"}}}]\n", path))
	composition := composeWith(t, load(t, home), Selection{Books: []string{"tool"}})
	if got := kitArgs(t, composition, "other"); !reflect.DeepEqual(got, map[string]any{"keep": "7"}) {
		t.Errorf("other kit args = %v", got)
	}
}

func TestBookArgumentsMustBeArgumentsTheKitDeclaresWithAPatternTheWholeValueMatches(t *testing.T) {
	for args, message := range map[string]string{
		`{tool = {channel = "11.0; id"}}`: `tool.channel = "11.0; id" does not match`,
		`{tool = {version = "1 && id"}}`:  `tool.version = "1 && id" does not match`,
		`{tool = {missing = "1"}}`:        "kit tool has no argument missing",
		`{tool = {loose = "x"}}`:          "tool.loose has no pattern",
		`{nope = {channel = "1.0"}}`:      "has no kit named nope",
		`{tool = {channel = 11}}`:         "tool.channel must be a string",
		`{tool = "11.0"}`:                 "args = { <kit> = { <argument> = \"<value>\" } }",
		`["tool"]`:                        "args = { <kit> = { <argument> = \"<value>\" } }",
	} {
		path := argsBook(t, t.TempDir())
		home := filepath.Join(t.TempDir(), "home")
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [{source = %q, args = %s}]\n", path, args))
		settings, err := LoadSettings(home)
		if err == nil {
			_, err = Compose(settings, Selection{Books: []string{"tool"}})
		}
		expectError(t, err, message)
	}
}

func TestABookEntryIsAStringOrSourceAndArgs(t *testing.T) {
	path := argsBook(t, t.TempDir())
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [{source = %q, kits = []}]\n", path))
	_, err := LoadSettings(home)
	expectError(t, err, "a book is named by a string or {source, args}")
}

func repoNaming(t *testing.T, books string) *RepoConfig {
	t.Helper()
	repoDir := filepath.Join(t.TempDir(), "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "books = "+books+"\n")
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func bookApproval(t *testing.T, composition *Composition, name string) Approval {
	t.Helper()
	approvals, err := Approvals(composition, "example-org/service")
	if err != nil {
		t.Fatal(err)
	}
	for _, approval := range approvals {
		if strings.HasPrefix(approval.Label, "book "+name+" ") {
			return approval
		}
	}
	t.Fatalf("no approval for book %s in %+v", name, approvals)
	return Approval{}
}

func TestARepositorySetsArgumentsForABookItNamesAndItsApprovalCoversThem(t *testing.T) {
	dir := t.TempDir()
	path := argsBook(t, dir)
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [%q]\n", path))
	settings := load(t, home)

	plain := composeWith(t, settings, Selection{Repo: repoNaming(t, `["tool"]`)})
	plainApproval := bookApproval(t, plain, "tool")
	unchanged, err := hashTree("book\x00tool", path, true)
	if err != nil {
		t.Fatal(err)
	}
	if plainApproval.Fingerprint != unchanged {
		t.Error("a plainly named book's approval fingerprint changed, so earlier approvals would ask again")
	}

	repo := repoNaming(t, `[{source = "tool", args = {tool = {channel = "11.0"}}}]`)
	composition := composeWith(t, settings, Selection{Repo: repo})
	if got := kitArgs(t, composition, "tool"); !reflect.DeepEqual(got, map[string]any{"channel": "11.0"}) {
		t.Errorf("tool kit args = %v", got)
	}
	approval := bookApproval(t, composition, "tool")
	if approval.Fingerprint == plainApproval.Fingerprint {
		t.Error("an approval of the book with defaults covers it with a repository's arguments")
	}
	if details := strings.Join(approval.Details, "\n"); !strings.Contains(details, "argument tool.channel = 11.0 (from "+repo.Path+")") {
		t.Errorf("approval does not show the argument:\n%s", details)
	}
	other := composeWith(t, settings, Selection{Repo: repoNaming(t, `[{source = "tool", args = {tool = {channel = "12.0"}}}]`)})
	if bookApproval(t, other, "tool").Fingerprint == approval.Fingerprint {
		t.Error("one approval covers two different arguments")
	}
}

func TestAnIncludeSetsArgumentsForTheBookItIncludes(t *testing.T) {
	dir := t.TempDir()
	argsBook(t, dir)
	outer := book(t, dir, "outer", "includes = [{source = \"../tool\", args = {tool = {channel = \"11.0\"}}}]\n")
	home := filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [%q]\n", outer))
	settings := load(t, home)
	composition := composeWith(t, settings, Selection{Books: []string{"outer"}})
	if got := kitArgs(t, composition, "tool"); !reflect.DeepEqual(got, map[string]any{"channel": "11.0"}) {
		t.Errorf("tool kit args = %v", got)
	}
	_, details, err := DescribeBook(settings.Agents, outer)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(details, "\n"); !strings.Contains(got, "includes book ../tool with argument tool.channel = 11.0") {
		t.Errorf("the including book's summary does not show the argument:\n%s", got)
	}
}

func TestArgumentsForOneBookFromTwoPlacesMergeAndOneArgumentGivenTwoValuesIsAnErrorNamingBoth(t *testing.T) {
	dir := t.TempDir()
	path := argsBook(t, dir)
	outer := book(t, dir, "outer", "includes = [{source = \"../tool\", args = {tool = {channel = \"11.0\"}}}]\n")
	home := filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [%q, %q]\n", outer, path))
	settings := load(t, home)

	merged := composeWith(t, settings, Selection{Books: []string{"outer"}, Repo: repoNaming(t, `[{source = "tool", args = {tool = {version = "3"}}}]`)})
	if got := kitArgs(t, merged, "tool"); !reflect.DeepEqual(got, map[string]any{"channel": "11.0", "version": "3"}) {
		t.Errorf("tool kit args = %v", got)
	}
	plain := composeWith(t, settings, Selection{Books: []string{"outer"}, Repo: repoNaming(t, `["tool"]`)})
	if got := kitArgs(t, plain, "tool"); !reflect.DeepEqual(got, map[string]any{"channel": "11.0"}) {
		t.Errorf("a plain naming dropped the include's argument: %v", got)
	}

	repo := repoNaming(t, `[{source = "tool", args = {tool = {channel = "12.0"}}}]`)
	_, err := Compose(settings, Selection{Books: []string{"outer"}, Repo: repo})
	expectError(t, err, "book tool: argument tool.channel is")
	for _, want := range []string{`"11.0"`, `"12.0"`, repo.Path, outer} {
		if err != nil && !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
}

func TestARepositoryCanNameABookWithArgumentsOnlyByNameOrReference(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "books = [{source = \"../tool\", args = {tool = {channel = \"11.0\"}}}]\n")
	_, err := LoadRepoConfig(repoDir)
	expectError(t, err, "books are named, not located")
}

func TestARepositoryInitAsksForABookTheConfigNamesWithArguments(t *testing.T) {
	repo := repoNaming(t, `[{source = "tool", args = {tool = {channel = "11.0"}}}]`)
	if missing := repo.MissingRequests(nil, []string{"tool", "other"}, nil); !reflect.DeepEqual(missing, []string{"--book other"}) {
		t.Errorf("missing = %v", missing)
	}
}

func argsBookTurnedOnTwoWays(t *testing.T) (*Settings, map[string]Selection) {
	t.Helper()
	dir := t.TempDir()
	path := argsBook(t, dir)
	testutil.WriteFile(t, filepath.Join(path, "book.toml"), "name = \"tool\"\nkits = [\"./kits/tool\"]\n[[repos]]\nmatch = [\"example-org/service\"]\n")
	home := filepath.Join(dir, "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("books = [%q]\n", path))
	repo := repoNaming(t, `[{source = "tool", args = {tool = {channel = "11.0"}}}]`)
	return load(t, home), map[string]Selection{
		"--book":     {Books: []string{"tool"}, Repo: repo},
		"repos rule": {RepoNames: []string{"example-org/service"}, Repo: repo},
	}
}

func TestARepositorysArgumentsNeedApprovalEvenForABookYouAlsoTurnedOn(t *testing.T) {
	settings, selections := argsBookTurnedOnTwoWays(t)
	for how, selection := range selections {
		composition := composeWith(t, settings, selection)
		if got := kitArgs(t, composition, "tool"); !reflect.DeepEqual(got, map[string]any{"channel": "11.0"}) {
			t.Fatalf("%s: tool kit args = %v", how, got)
		}
		approval := bookApproval(t, composition, "tool")
		if !approval.FromRepository || !strings.Contains(strings.Join(approval.Details, "\n"), "argument tool.channel = 11.0 (from "+selection.Repo.Path+")") {
			t.Errorf("%s: approval does not ask about the repository's argument: %+v", how, approval)
		}
	}
}

func TestRepoKitsDenyRefusesARepositorysArgumentsForABookYouAlsoTurnedOn(t *testing.T) {
	managedFiles(t, "repo_kits = \"deny\"\n")
	settings, selections := argsBookTurnedOnTwoWays(t)
	for how, selection := range selections {
		_, err := Compose(settings, selection)
		if err == nil || !strings.Contains(err.Error(), "refused by") {
			t.Errorf("%s: error %v, want the repository's arguments refused", how, err)
		}
		plain := selection
		plain.Repo = repoNaming(t, `["tool"]`)
		if _, err := Compose(settings, plain); err != nil {
			t.Errorf("%s: a plain naming of a book you turned on is refused: %v", how, err)
		}
	}
}
