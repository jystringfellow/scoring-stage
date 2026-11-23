package bookref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestReferencesNameAGitHubRepositoryABookDirectoryAndAVersion(t *testing.T) {
	ref, err := Parse("github.com/example-org/books//backend/go@v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Owner != "example-org" || ref.Repo != "books" || ref.Path != "backend/go" || ref.Version != "v1.2.0" {
		t.Errorf("parsed %+v", ref)
	}
	if ref.String() != "github.com/example-org/books//backend/go@v1.2.0" {
		t.Errorf("String() = %q", ref.String())
	}
	commit := strings.Repeat("4f2a9c1e", 5)
	if ref, err := Parse("github.com/example-org/books//go@" + commit); err != nil || ref.Version != commit {
		t.Errorf("a full commit: %+v, %v", ref, err)
	}
}

func TestAReferenceRefusesAnythingButATagOrAFullCommitOnGitHub(t *testing.T) {
	for text, want := range map[string]string{
		"gitlab.com/example-org/books//go@v1":           "only github.com",
		"github.com/example-org/books//go":              "needs a version",
		"github.com/example-org/books//go@":             "needs a version",
		"github.com/example-org/books@v1":               "book's directory",
		"github.com/example-org/books//@v1":             "book's directory",
		"github.com/example-org/books//../go@v1":        "book's directory",
		"github.com/example-org/books//go/./x@v1":       "book's directory",
		"github.com/example-org/books//.git/hooks@v1":   "book's directory",
		"github.com/example-org/books//go@4f2a9c1":      "full 40-character commit",
		"github.com/example-org/books//go@v1..2":        "not a tag",
		"github.com/example-org/books//go@-v1":          "not a tag",
		"github.com/example-org/books//go@v1 x":         "not a tag",
		"github.com/example-org//go@v1":                 "owner/repository",
		"github.com/-bad/books//go@v1":                  "owner/repository",
		"github.com/example-org/books/extra//go@v1":     "owner/repository",
		"github.com/example-org/books//go:name@v1":      "book's directory",
		"github.com/example-org/books//go\x1b[31m@v1":   "book's directory",
		"github.com/example-org/books//go@v1\u202e":     "not a tag",
		"github.com/example-org/books//go/@v1":          "book's directory",
		"github.com/example-org/books//go//kits@v1":     "book's directory",
		"github.com/example-org/books//go@refs/tags/v1": "not a tag",
	} {
		if _, err := Parse(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error saying %q", text, err, want)
		}
	}
}

func TestOnlyTextShapedLikeAReferenceIsTakenForOne(t *testing.T) {
	for text, want := range map[string]bool{
		"github.com/example-org/books//go@v1": true,
		"gitlab.com/example-org/books//go@v1": true,
		"~/books/go":                          false,
		"./books/go":                          false,
		"/srv/books/go":                       false,
		"books/go":                            false,
		"go":                                  false,
	} {
		if got := IsRef(text); got != want {
			t.Errorf("IsRef(%q) = %v, want %v", text, got, want)
		}
	}
}

type booksRepo struct {
	t    *testing.T
	bare string
	work string
}

func newBooksRepo(t *testing.T) *booksRepo {
	t.Helper()
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		t.Setenv(key, value)
	}
	root := t.TempDir()
	r := &booksRepo{t: t, bare: filepath.Join(root, "books.git"), work: filepath.Join(root, "work")}
	testutil.RunGit(t, root, "init", "-q", "--bare", "-b", "main", r.bare)
	testutil.RunGit(t, root, "init", "-q", "-b", "main", r.work)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+r.bare+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/example-org/books.git")
	return r
}

func (r *booksRepo) commit(files map[string]string) string {
	r.t.Helper()
	for path, content := range files {
		testutil.WriteFile(r.t, filepath.Join(r.work, path), content)
	}
	testutil.RunGit(r.t, r.work, "add", "-A")
	testutil.RunGit(r.t, r.work, "commit", "-q", "--allow-empty", "-m", "change")
	testutil.RunGit(r.t, r.work, "push", "-q", "-f", r.bare, "main")
	return testutil.RunGit(r.t, r.work, "rev-parse", "HEAD")
}

func (r *booksRepo) tag(name string) {
	r.t.Helper()
	testutil.RunGit(r.t, r.work, "tag", "-f", "-a", "-m", name, name)
	testutil.RunGit(r.t, r.work, "push", "-q", "-f", r.bare, "refs/tags/"+name)
}

func goBook(version string) map[string]string {
	return map[string]string{
		"backend/go/book.toml":            "name = \"go\"\ndescription = \"" + version + "\"\n",
		"backend/go/kits/go/go.yaml":      "allow: [proxy.golang.org]\n",
		"backend/go/bin/token.sh":         "#!/bin/sh\necho token\n",
		"backend/python/book.toml":        "name = \"python\"\n",
		"unrelated/notes-nobody-reviewed": "keep out of the cache\n",
	}
}

func mustParse(t *testing.T, text string) Ref {
	t.Helper()
	ref, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func readBook(t *testing.T, book Book) string {
	t.Helper()
	return testutil.ReadFile(t, filepath.Join(book.Dir, "book.toml"))
}

func TestFirstUseFetchesOnlyTheBookIntoAReadOnlyCacheAndLocksIt(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	os.Chmod(filepath.Join(repo.work, "backend/go/bin/token.sh"), 0o755)
	commit := repo.commit(nil)
	repo.tag("v1.0.0")
	home := stageHome(t)

	book, err := Library{Home: home}.Open(mustParse(t, "github.com/example-org/books//backend/go@v1.0.0"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if book.Commit != commit {
		t.Errorf("commit %s, want the tag's commit %s", book.Commit, commit)
	}
	if book.Dir != filepath.Join(home, "books", book.Fingerprint) {
		t.Errorf("cached at %s, want under books/ by fingerprint", book.Dir)
	}
	var files []string
	filepath.WalkDir(book.Dir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			relative, _ := filepath.Rel(book.Dir, path)
			files = append(files, relative)
		}
		return nil
	})
	if strings.Join(files, " ") != "bin/token.sh book.toml kits/go/go.yaml" {
		t.Errorf("cached files %q, want only the book's directory", files)
	}
	for path, want := range map[string]os.FileMode{"book.toml": 0o444, "bin/token.sh": 0o555, "kits": 0o555, ".": 0o555} {
		info, err := os.Stat(filepath.Join(book.Dir, path))
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: mode %v (%v), want %v", path, info.Mode().Perm(), err, want)
		}
	}
	lock := testutil.ReadFile(t, filepath.Join(home, "stage.lock"))
	for _, want := range []string{`ref = "github.com/example-org/books//backend/go@v1.0.0"`, `commit = "` + commit + `"`, `sha256 = "` + book.Fingerprint + `"`, `scheme = "` + Scheme + `"`} {
		if !strings.Contains(lock, want) {
			t.Errorf("lock lacks %s:\n%s", want, lock)
		}
	}
}

func TestALockedReferenceKeepsItsCommitWhenTheTagMoves(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	repo.tag("v1")
	home := stageHome(t)
	ref := mustParse(t, "github.com/example-org/books//backend/go@v1")
	first, err := Library{Home: home}.Open(ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	repo.commit(goBook("moved"))
	repo.tag("v1")
	makeWritable(t, filepath.Join(home, "books"))
	os.RemoveAll(filepath.Join(home, "books"))

	again, err := Library{Home: home}.Open(ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Commit != first.Commit || !strings.Contains(readBook(t, again), "one") {
		t.Errorf("after the tag moved: commit %s, book %q; want the locked commit %s", again.Commit, readBook(t, again), first.Commit)
	}
}

func TestContentThatDoesNotMatchTheLockIsRefusedAndTheLockIsLeftAlone(t *testing.T) {
	repo := newBooksRepo(t)
	commit := repo.commit(goBook("one"))
	repo.tag("v1")
	home := stageHome(t)
	lock := "[[book]]\nref = \"github.com/example-org/books//backend/go@v1\"\ncommit = \"" + commit + "\"\nsha256 = \"" + strings.Repeat("0", 64) + "\"\nscheme = \"" + Scheme + "\"\n"
	testutil.WriteFile(t, filepath.Join(home, "stage.lock"), lock)

	_, err := Library{Home: home}.Open(mustParse(t, "github.com/example-org/books//backend/go@v1"), nil)
	if err == nil || !strings.Contains(err.Error(), "does not match") || !strings.Contains(err.Error(), "github.com/example-org/books//backend/go@v1") {
		t.Fatalf("got %v, want a refusal naming the reference", err)
	}
	if got := testutil.ReadFile(t, filepath.Join(home, "stage.lock")); got != lock {
		t.Errorf("lock rewritten:\n%s", got)
	}
}

func TestATeamLockPinsTheCommitWithoutResolvingTheTagAndIsCopiedToYours(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("team"))
	repo.tag("v1")
	ref := mustParse(t, "github.com/example-org/books//backend/go@v1")
	teamHome := stageHome(t)
	pinned, err := Library{Home: teamHome}.Open(ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	repo.commit(goBook("moved"))
	repo.tag("v1")
	team, err := ReadLock(filepath.Join(teamHome, "stage.lock"))
	if err != nil {
		t.Fatal(err)
	}

	home := stageHome(t)
	book, err := Library{Home: home}.Open(ref, team)
	if err != nil {
		t.Fatal(err)
	}
	if book.Commit != pinned.Commit || !strings.Contains(readBook(t, book), "team") {
		t.Errorf("got commit %s with %q, want the team's %s", book.Commit, readBook(t, book), pinned.Commit)
	}
	mine := testutil.ReadFile(t, filepath.Join(home, "stage.lock"))
	if !strings.Contains(mine, pinned.Commit) {
		t.Errorf("your lock does not record the team's commit:\n%s", mine)
	}
}

func TestLocksThatDisagreeAreRefusedNamingBothAndHowToSettleIt(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	repo.tag("v1")
	ref := mustParse(t, "github.com/example-org/books//backend/go@v1")
	teamHome, home := stageHome(t), stageHome(t)
	if _, err := (Library{Home: teamHome}).Open(ref, nil); err != nil {
		t.Fatal(err)
	}
	repo.commit(goBook("two"))
	repo.tag("v1")
	if _, err := (Library{Home: home}).Open(ref, nil); err != nil {
		t.Fatal(err)
	}
	team, err := ReadLock(filepath.Join(teamHome, "stage.lock"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Library{Home: home}.Open(ref, team)
	if err == nil {
		t.Fatal("disagreeing locks were accepted")
	}
	for _, want := range []string{filepath.Join(home, "stage.lock"), filepath.Join(teamHome, "stage.lock"), ref.String(), "stage book update " + ref.String() + " --from"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestABranchIsNotAVersion(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	testutil.RunGit(t, repo.work, "push", "-q", repo.bare, "main:refs/heads/latest")
	_, err := Library{Home: stageHome(t)}.Open(mustParse(t, "github.com/example-org/books//backend/go@latest"), nil)
	if err == nil || !strings.Contains(err.Error(), "is a branch") {
		t.Fatalf("got %v, want a branch refused", err)
	}
	_, err = Library{Home: stageHome(t)}.Open(mustParse(t, "github.com/example-org/books//backend/go@v9"), nil)
	if err == nil || !strings.Contains(err.Error(), "no tag v9") {
		t.Fatalf("got %v, want a missing tag named", err)
	}
}

func TestAFullCommitIsUsedAsItIs(t *testing.T) {
	repo := newBooksRepo(t)
	commit := repo.commit(goBook("pinned"))
	repo.commit(goBook("later"))
	book, err := Library{Home: stageHome(t)}.Open(mustParse(t, "github.com/example-org/books//backend/go@"+commit), nil)
	if err != nil {
		t.Fatal(err)
	}
	if book.Commit != commit || !strings.Contains(readBook(t, book), "pinned") {
		t.Errorf("got %s with %q", book.Commit, readBook(t, book))
	}
}

func TestABookWithALinkOrASubmoduleIsRefusedRatherThanHashed(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	os.Symlink("../../unrelated/notes-nobody-reviewed", filepath.Join(repo.work, "backend/go/notes"))
	repo.commit(nil)
	repo.tag("link")
	_, err := Library{Home: stageHome(t)}.Open(mustParse(t, "github.com/example-org/books//backend/go@link"), nil)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("link: got %v", err)
	}

	os.Remove(filepath.Join(repo.work, "backend/go/notes"))
	testutil.RunGit(t, repo.work, "add", "-A")
	testutil.RunGit(t, repo.work, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+",backend/go/vendor")
	testutil.RunGit(t, repo.work, "commit", "-q", "-m", "submodule")
	testutil.RunGit(t, repo.work, "push", "-q", repo.bare, "main")
	repo.tag("submodule")
	_, err = Library{Home: stageHome(t)}.Open(mustParse(t, "github.com/example-org/books//backend/go@submodule"), nil)
	if err == nil || !strings.Contains(err.Error(), "submodule") {
		t.Errorf("submodule: got %v", err)
	}
}

func TestABookOverTheSizeLimitIsRefused(t *testing.T) {
	repo := newBooksRepo(t)
	files := goBook("one")
	files["backend/go/big.bin"] = strings.Repeat("x", MaxFileBytes+1)
	repo.commit(files)
	repo.tag("v1")
	home := stageHome(t)
	_, err := Library{Home: home}.Open(mustParse(t, "github.com/example-org/books//backend/go@v1"), nil)
	if err == nil || !strings.Contains(err.Error(), "big.bin") || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("got %v, want the large file named", err)
	}
	if _, err := os.Stat(filepath.Join(home, "stage.lock")); err == nil {
		t.Error("a refused book was locked")
	}
}

func TestAMissingDirectoryIsNamed(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	repo.tag("v1")
	_, err := Library{Home: stageHome(t)}.Open(mustParse(t, "github.com/example-org/books//backend/rust@v1"), nil)
	if err == nil || !strings.Contains(err.Error(), "no directory backend/rust") {
		t.Fatalf("got %v", err)
	}
}

func TestAFailedFetchNamesTheReferenceAndChangesNothing(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	repo.tag("v1")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/example-org/elsewhere.git")
	home := stageHome(t)
	_, err := Library{Home: home}.Open(mustParse(t, "github.com/example-org/books//backend/go@v1"), nil)
	if err == nil || !strings.Contains(err.Error(), "github.com/example-org/books//backend/go@v1") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("got %v", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("left %d entries in the stage home", len(entries))
	}
}

func TestACachedBookIsUsedWithoutTheNetworkAndCheckedEachTime(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	repo.tag("v1")
	home := stageHome(t)
	ref := mustParse(t, "github.com/example-org/books//backend/go@v1")
	book, err := Library{Home: home}.Open(ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/example-org/elsewhere.git")
	if _, err := (Library{Home: home}).Open(ref, nil); err != nil {
		t.Fatalf("a cached book needed the network: %v", err)
	}

	makeWritable(t, book.Dir)
	testutil.WriteFile(t, filepath.Join(book.Dir, "book.toml"), "name = \"go\"\n[secrets.x]\ncommand = \"curl evil\"\n")
	_, err = Library{Home: home}.Open(ref, nil)
	if err == nil || !strings.Contains(err.Error(), "changed") || !strings.Contains(err.Error(), book.Dir) {
		t.Fatalf("got %v, want the altered cache refused", err)
	}
}

func TestALockFromAnotherFingerprintSchemeIsNotTakenForAMovedTag(t *testing.T) {
	repo := newBooksRepo(t)
	commit := repo.commit(goBook("one"))
	repo.tag("v1")
	home := stageHome(t)
	testutil.WriteFile(t, filepath.Join(home, "stage.lock"), "[[book]]\nref = \"github.com/example-org/books//backend/go@v1\"\ncommit = \""+commit+"\"\nsha256 = \""+strings.Repeat("0", 64)+"\"\nscheme = \"book-tree/9\"\n")
	_, err := Library{Home: home}.Open(mustParse(t, "github.com/example-org/books//backend/go@v1"), nil)
	if err == nil || !strings.Contains(err.Error(), "book-tree/9") || strings.Contains(err.Error(), "does not match") {
		t.Fatalf("got %v, want the unknown scheme named rather than a mismatch", err)
	}
}

func TestALockFileIsReadStrictly(t *testing.T) {
	commit := strings.Repeat("a", 40)
	sum := strings.Repeat("b", 64)
	entry := func(extra string) string {
		return "[[book]]\nref = \"github.com/example-org/books//go@v1\"\ncommit = \"" + commit + "\"\nsha256 = \"" + sum + "\"\nscheme = \"" + Scheme + "\"\n" + extra
	}
	for content, want := range map[string]string{
		entry("unknown = 1\n"):                       "unknown",
		entry("") + entry(""):                        "twice",
		strings.Replace(entry(""), commit, "abc", 1): "commit",
		strings.Replace(entry(""), sum, "xyz", 1):    "sha256",
		strings.Replace(entry(""), "@v1", "", 1):     "needs a version",
		"version = 2\n":                              "unknown",
	} {
		path := testutil.WriteFile(t, filepath.Join(t.TempDir(), "stage.lock"), content)
		if _, err := ReadLock(path); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), path) {
			t.Errorf("ReadLock(%q) = %v, want an error naming the file and %q", content, err, want)
		}
	}
	if lock, err := ReadLock(filepath.Join(t.TempDir(), "stage.lock")); err != nil || lock == nil {
		t.Errorf("a missing lock: %v, %v", lock, err)
	}
}

func makeWritable(t *testing.T, root string) {
	t.Helper()
	filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil {
			mode := os.FileMode(0o644)
			if entry.IsDir() {
				mode = 0o755
			}
			os.Chmod(path, mode)
		}
		return nil
	})
}

func stageHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Cleanup(func() { makeWritable(t, home) })
	return home
}

func TestALockThatIsNotARegularFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	target := testutil.WriteFile(t, filepath.Join(dir, "elsewhere"), "")
	os.Symlink(target, filepath.Join(dir, "stage.lock"))
	if _, err := ReadLock(filepath.Join(dir, "stage.lock")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("got %v", err)
	}
}

func TestPathsThatDifferOnlyByCaseAreRefusedBeforeAnythingIsCached(t *testing.T) {
	repo := newBooksRepo(t)
	repo.commit(goBook("one"))
	for _, path := range []string{"backend/go/kits/A.yaml", "backend/go/kits/a.yaml", "backend/go/Bin/x", "backend/go/bin/y"} {
		blob := testutil.RunGit(t, repo.work, "hash-object", "-w", "--stdin")
		testutil.RunGit(t, repo.work, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	testutil.RunGit(t, repo.work, "commit", "-q", "-m", "cases")
	testutil.RunGit(t, repo.work, "push", "-q", repo.bare, "main")
	repo.tag("cases")
	home := stageHome(t)
	_, err := Library{Home: home}.Open(mustParse(t, "github.com/example-org/books//backend/go@cases"), nil)
	if err == nil || !strings.Contains(err.Error(), "differ only by case") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "books")); err == nil {
		t.Error("cached a book whose paths collide")
	}
}
