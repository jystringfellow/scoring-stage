package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func (r *booksRemote) publishFiles(tag string, files map[string]string) string {
	r.h.t.Helper()
	for path, content := range files {
		testutil.WriteFile(r.h.t, filepath.Join(r.work, path), content)
	}
	testutil.RunGit(r.h.t, r.work, "add", "-A")
	testutil.RunGit(r.h.t, r.work, "commit", "-q", "--allow-empty", "-m", tag)
	testutil.RunGit(r.h.t, r.work, "tag", "-f", tag)
	testutil.RunGit(r.h.t, r.work, "push", "-q", "-f", r.bare, "main", "refs/tags/"+tag)
	return testutil.RunGit(r.h.t, r.work, "rev-parse", "HEAD")
}

func bookToml(name, includes string, extra string) string {
	return "name = \"" + name + "\"\nincludes = [" + includes + "]\n" + extra
}

const serviceRef = "github.com/example-org/books//backend/service@v1"

func TestABooksIncludesApplyWhenItDoesAndEachIsLockedAndApproved(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	commit := books.publishFiles("v1", map[string]string{
		"backend/service/book.toml":   bookToml("service", `"../go", "github.com/example-org/books//shared/observe@v1"`, "takes = [\"~/service\"]\n"),
		"backend/go/book.toml":        bookToml("go", "", "takes = [\"~/go\"]\n"),
		"shared/observe/book.toml":    bookToml("observe", "", "takes = [\"~/observe\"]\n"),
		"backend/unrelated/book.toml": bookToml("unrelated", "", ""),
	})
	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", serviceRef)
	for _, label := range []string{
		"  - book service (" + serviceRef + ")",
		"  - book go (github.com/example-org/books//backend/go@" + commit + ")",
		"  - book observe (github.com/example-org/books//shared/observe@v1)",
	} {
		h.expectStderr(label)
	}
	if strings.Contains(h.stderr.String(), "unrelated") {
		t.Error("a book nothing includes was asked about")
	}
	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "fix", "--book", serviceRef)
	task := testutil.ReadFile(t, filepath.Join(h.task("fix"), "task.json"))
	for _, take := range []string{"~/service", "~/go", "~/observe"} {
		if !strings.Contains(task, take) {
			t.Errorf("task lacks %s from an included book:\n%s", take, task)
		}
	}
	lock := testutil.ReadFile(t, filepath.Join(h.home, "stage.lock"))
	for _, ref := range []string{serviceRef, "github.com/example-org/books//backend/go@" + commit, "github.com/example-org/books//shared/observe@v1"} {
		if !strings.Contains(lock, "ref = \""+ref+"\"") {
			t.Errorf("lock lacks %s:\n%s", ref, lock)
		}
	}
}

func TestARelativeIncludeStaysAtTheCommitOfTheBookThatIncludesIt(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	first := books.publishFiles("v1", map[string]string{
		"backend/service/book.toml": bookToml("service", `"../go"`, ""),
		"backend/go/book.toml":      bookToml("go", "", "description = \"first\"\n"),
	})
	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "one", "--book", serviceRef)
	books.publishFiles("v1", map[string]string{"backend/go/book.toml": bookToml("go", "", "description = \"moved\"\n")})
	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "two", "--book", serviceRef)
	if got := h.lockedCommit(filepath.Join(h.home, "stage.lock"), "github.com/example-org/books//backend/go@"+first); got != first {
		t.Errorf("relative include locked at %q, want the including book's commit %s", got, first)
	}
}

func TestIncludesThatFormACycleAreRefused(t *testing.T) {
	h := setup(t)
	h.booksRemote().publishFiles("v1", map[string]string{
		"backend/service/book.toml": bookToml("service", `"github.com/example-org/books//backend/go@v1"`, ""),
		"backend/go/book.toml":      bookToml("go", `"github.com/example-org/books//backend/service@v1"`, ""),
	})
	h.answerAtATerminal("y\n")
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", serviceRef)
	h.expectStderr("cycle")
	h.expectStderr(serviceRef + " → github.com/example-org/books//backend/go@v1 → " + serviceRef)
}

func TestABookReachedTwiceAtOneVersionAppliesOnce(t *testing.T) {
	h := setup(t)
	h.booksRemote().publishFiles("v1", map[string]string{
		"backend/service/book.toml": bookToml("service", `"github.com/example-org/books//backend/go@v1"`, ""),
		"backend/worker/book.toml":  bookToml("worker", `"../go"`, ""),
		"backend/go/book.toml":      bookToml("go", "", ""),
	})
	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", serviceRef, "--book", "github.com/example-org/books//backend/worker@v1")
	if got := strings.Count(h.stderr.String(), "  - book go ("); got != 1 {
		t.Errorf("go was asked about %d times:\n%s", got, h.stderr.String())
	}
}

func TestTwoVersionsOfOneBookAreRefusedNamingBothPaths(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	books.publishFiles("v1", map[string]string{"backend/go/book.toml": bookToml("go", "", "")})
	books.publishFiles("v2", map[string]string{
		"backend/go/book.toml":      bookToml("go", "", "description = \"two\"\n"),
		"backend/service/book.toml": bookToml("service", `"github.com/example-org/books//backend/go@v1"`, ""),
		"backend/worker/book.toml":  bookToml("worker", `"../go"`, ""),
	})
	h.answerAtATerminal("y\n")
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", "github.com/example-org/books//backend/service@v2", "--book", "github.com/example-org/books//backend/worker@v2")
	h.expectStderr("two versions")
	h.expectStderr("github.com/example-org/books//backend/service@v2 → github.com/example-org/books//backend/go@v1")
	h.expectStderr("github.com/example-org/books//backend/worker@v2 → github.com/example-org/books//backend/go@")
}

func TestAnIncludeThatLeavesItsRepositoryIsRefused(t *testing.T) {
	h := setup(t)
	h.booksRemote().publishFiles("v1", map[string]string{"backend/service/book.toml": bookToml("service", `"../../../elsewhere"`, "")})
	h.answerAtATerminal("y\n")
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", serviceRef)
	h.expectStderr("leaves the repository")
}

func TestALocalBookIncludesALocalDirectory(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "books", "team", "book.toml"), bookToml("team", `"../common"`, ""))
	testutil.WriteFile(t, filepath.Join(h.home, "books", "common", "book.toml"), bookToml("common", "", "takes = [\"~/common\"]\n"))
	writeGitHubSetup(t, h.home, "books = [\"books/team\"]\n")
	h.expect(0, "up", h.remote.Checkout, "fix", "--book", "team")
	if task := testutil.ReadFile(t, filepath.Join(h.task("fix"), "task.json")); !strings.Contains(task, "~/common") {
		t.Errorf("the included local book did not apply:\n%s", task)
	}
}

func TestBookLockAlsoLocksWhatTheRepositorysBooksInclude(t *testing.T) {
	h := setup(t)
	commit := h.booksRemote().publishFiles("v1", map[string]string{
		"backend/service/book.toml": bookToml("service", `"../go"`, ""),
		"backend/go/book.toml":      bookToml("go", "", ""),
	})
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "config.toml"), "books = [\""+serviceRef+"\"]\n")
	h.answerAtATerminal("y\ny\n")
	h.expect(0, "book", "lock", h.remote.Checkout)
	written := testutil.ReadFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "stage.lock"))
	if !strings.Contains(written, "github.com/example-org/books//backend/go@"+commit) {
		t.Errorf("repository lock lacks the include:\n%s", written)
	}
	if _, err := os.Stat(filepath.Join(h.home, "stage.lock")); err != nil {
		t.Error(err)
	}
}

func TestIncludedBooksThatSetTheSameSecretDifferentlyAreAnError(t *testing.T) {
	h := setup(t)
	h.booksRemote().publishFiles("v1", map[string]string{
		"backend/service/book.toml": bookToml("service", `"../go"`, "[secrets.feed]\ncommand = \"echo one\"\n"),
		"backend/go/book.toml":      bookToml("go", "", "[secrets.feed]\ncommand = \"echo two\"\n"),
	})
	h.answerAtATerminal("y\n")
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", serviceRef)
	h.expectStderr("feed")
	if h.anyCall("env create") {
		t.Fatal("created a sandbox with conflicting secrets")
	}
}
