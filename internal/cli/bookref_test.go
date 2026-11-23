package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const goBookRef = "github.com/example-org/books//backend/go@v1"

type booksRemote struct {
	h    *harness
	bare string
	work string
}

func (h *harness) booksRemote() *booksRemote {
	h.t.Helper()
	root := h.t.TempDir()
	r := &booksRemote{h: h, bare: filepath.Join(root, "books.git"), work: filepath.Join(root, "work")}
	testutil.RunGit(h.t, root, "init", "-q", "--bare", "-b", "main", r.bare)
	testutil.RunGit(h.t, root, "init", "-q", "-b", "main", r.work)
	h.t.Setenv("GIT_CONFIG_COUNT", "2")
	h.t.Setenv("GIT_CONFIG_KEY_1", "url.file://"+r.bare+".insteadOf")
	h.t.Setenv("GIT_CONFIG_VALUE_1", "https://github.com/example-org/books.git")
	h.t.Cleanup(func() { makeWritable(h.home) })
	return r
}

func makeWritable(root string) {
	filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			os.Chmod(path, 0o755)
		}
		return nil
	})
}

func (r *booksRemote) release(tag, description string) string {
	r.h.t.Helper()
	files := map[string]string{
		"backend/go/book.toml":       "name = \"go\"\ndescription = \"" + description + "\"\nkits = [\"./kits/go\"]\n[secrets.feed]\ncommand = \"{book}/bin/token\"\n",
		"backend/go/kits/go/go.yaml": "allow: [proxy.golang.org]\n",
		"backend/go/bin/token":       "#!/bin/sh\necho stage-probe-dummy\n",
	}
	for path, content := range files {
		testutil.WriteFile(r.h.t, filepath.Join(r.work, path), content)
	}
	testutil.RunGit(r.h.t, r.work, "add", "-A")
	testutil.RunGit(r.h.t, r.work, "commit", "-q", "--allow-empty", "-m", description)
	testutil.RunGit(r.h.t, r.work, "tag", "-f", tag)
	testutil.RunGit(r.h.t, r.work, "push", "-q", "-f", r.bare, "main", "refs/tags/"+tag)
	return testutil.RunGit(r.h.t, r.work, "rev-parse", "HEAD")
}

func (h *harness) lockedCommit(path, ref string) string {
	h.t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_, after, found := strings.Cut(string(content), "ref = \""+ref+"\"\ncommit = \"")
	if !found {
		return ""
	}
	return after[:40]
}

func TestABookYouNameByReferenceIsFetchedLockedAndApprovedBeforeItApplies(t *testing.T) {
	h := setup(t)
	commit := h.booksRemote().release("v1", "first")
	writeGitHubSetup(t, h.home, "[books]\ngo = \""+goBookRef+"\"\n")
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "books = [\"go\"]\n"})

	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("  - book go (" + goBookRef + ")")
	h.expectStderr("host command for secret feed")
	if h.anyCall("env create", "env plan") {
		t.Fatalf("an unapproved fetched book reached sbx: %v", h.sbx.Calls())
	}
	h.expectNoApprovalsRecorded()
	if got := h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef); got != commit {
		t.Fatalf("lock holds %q, want the tag's commit %s", got, commit)
	}

	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	if !strings.Contains(env, filepath.Join(h.home, "books")) {
		t.Errorf("env file does not use the cached book:\n%s", env)
	}

	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "again")
}

func TestABookYouTurnOnYourselfByReferenceIsApprovedToo(t *testing.T) {
	h := setup(t)
	h.booksRemote().release("v1", "first")
	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", goBookRef)
	h.expectStderr("fetched by reference")
	h.expectStderr("  - book go (" + goBookRef + ")")
	if h.anyCall("env create") {
		t.Fatal("created a sandbox with an unapproved book")
	}
	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "fix", "--book", goBookRef)
}

func TestARepositoryMayNameABookByReferenceYouNeverInstalledAndYouAreAsked(t *testing.T) {
	h := setup(t)
	h.booksRemote().release("v1", "first")
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "books = [\"" + goBookRef + "\"]\n"})
	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("the repository's " + ".scoring-stage/config.toml asks for kits, books or takes you have not approved")
	h.expectStderr("  - book go (" + goBookRef + ")")
	h.expectNoApprovalsRecorded()
}

func TestRepoKitsDenyRefusesABookARepositoryNamesByReferenceBeforeFetchingIt(t *testing.T) {
	h := setup(t)
	h.booksRemote().release("v1", "first")
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "books = [\"" + goBookRef + "\"]\n"})
	testutil.WriteFile(t, h.managed, "repo_kits = \"deny\"\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("repo_kits = \"deny\"")
	for _, path := range []string{"stage.lock", "books"} {
		if _, err := os.Stat(filepath.Join(h.home, path)); err == nil {
			t.Errorf("fetched a book the managed settings refuse: %s exists", path)
		}
	}
}

func TestARepositorysLockPinsTheCommitOfTheBookItNames(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	pinned := books.release("v1", "pinned")
	team := "[[book]]\nref = \"" + goBookRef + "\"\ncommit = \"" + pinned + "\"\n"
	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "first", "--book", goBookRef)
	lock := testutil.ReadFile(t, filepath.Join(h.home, "stage.lock"))
	_, sum, _ := strings.Cut(lock, "sha256 = \"")
	team += "sha256 = \"" + sum[:64] + "\"\nscheme = \"book-tree/1\"\n"
	os.Remove(filepath.Join(h.home, "stage.lock"))
	moved := books.release("v1", "moved")

	h.commitToMain(map[string]string{
		".scoring-stage/config.toml": "books = [\"" + goBookRef + "\"]\n",
		".scoring-stage/stage.lock":  team,
	})
	h.expect(0, "up", h.remote.Checkout, "fix")
	if got := h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef); got != pinned {
		t.Errorf("your lock holds %s, want the repository's %s rather than the moved tag's %s", got, pinned, moved)
	}
}

func TestLocksThatDisagreeStopTheRunAndSayHowToSettleIt(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	pinned := books.release("v1", "pinned")
	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "first", "--book", goBookRef)
	team := strings.Replace(testutil.ReadFile(t, filepath.Join(h.home, "stage.lock")), pinned, strings.Repeat("e", 40), 1)
	h.commitToMain(map[string]string{
		".scoring-stage/config.toml": "books = [\"" + goBookRef + "\"]\n",
		".scoring-stage/stage.lock":  team,
	})
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr(filepath.Join(h.home, "stage.lock"))
	h.expectStderr(".scoring-stage/stage.lock")
	h.expectStderr("stage book update " + goBookRef + " --from <repo>")
	h.expectStderr("stage book lock <repo>")
	if _, err := os.Stat(filepath.Join(h.task("fix"), "sbxenv.yaml")); err == nil {
		t.Fatal("wrote an env file while the locks disagreed")
	}
}

func TestABookTableValueThatIsADirectoryIsUsedAsTodayAndNeverLocked(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "books", "mine", "book.toml"), "name = \"mine\"\ntakes = [\"~/notes\"]\n")
	writeGitHubSetup(t, h.home, "[books]\nteam = \"books/mine\"\n")
	h.expect(0, "up", h.remote.Checkout, "fix", "--book", "team")
	h.expect(0, "up", h.remote.Checkout, "other", "--book", "mine")
	if _, err := os.Stat(filepath.Join(h.home, "stage.lock")); err == nil {
		t.Error("a local book was locked")
	}
}

func TestAShortNameThatIsAnotherBooksNameIsRefused(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "books", "one", "book.toml"), "name = \"one\"\n")
	testutil.WriteFile(t, filepath.Join(h.home, "books", "two", "book.toml"), "name = \"two\"\n")
	writeGitHubSetup(t, h.home, "[books]\none = \"books/two\"\nzzz = \"books/one\"\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("one")
	h.expectStderr("another book's name")
}

func TestReadingConfigNeverFetchesABook(t *testing.T) {
	h := setup(t)
	h.booksRemote()
	writeGitHubSetup(t, h.home, "[books]\ngo = \""+goBookRef+"\"\n")
	h.expect(0, "list")
	if _, err := os.Stat(filepath.Join(h.home, "stage.lock")); err == nil {
		t.Error("stage list resolved a book")
	}
	if _, err := os.Stat(filepath.Join(h.home, "books")); err == nil {
		t.Error("stage list fetched a book")
	}
}

func TestMcpCannotApproveAFetchedBook(t *testing.T) {
	h := setup(t)
	h.booksRemote().release("v1", "first")
	writeGitHubSetup(t, h.home, "[books]\ngo = \""+goBookRef+"\"\n")
	d := readyMcp(t, h)
	toolError(t, d.tool("up", map[string]any{"slug": "fix", "repo": h.remote.Checkout, "books": []string{"go"}}), "not approved")
	d.close()
	h.expectNoApprovalsRecorded()
}
