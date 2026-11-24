package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const observeRef = "github.com/example-org/books//shared/observe@v1"

func (h *harness) goBookThatIncludesObserve(t *testing.T, books *booksRemote, tag string) string {
	t.Helper()
	books.publishFiles("v1", map[string]string{"shared/observe/book.toml": bookToml("observe", "", "takes = [\"~/observe\"]\n")})
	return books.publishFiles(tag, map[string]string{"backend/go/book.toml": bookToml("go", `"`+observeRef+`", "../tools"`, "takes = [\"~/go\"]\n"),
		"backend/tools/book.toml": bookToml("tools", "", "takes = [\"~/tools\"]\n")})
}

func (h *harness) withoutLockEntry(t *testing.T, ref string) {
	t.Helper()
	path := filepath.Join(h.home, "stage.lock")
	lock := testutil.ReadFile(t, path)
	start := strings.Index(lock, "[[book]]\nref = \""+ref+"\"")
	if start < 0 {
		t.Fatalf("lock has no %s:\n%s", ref, lock)
	}
	end := strings.Index(lock[start+1:], "[[book]]")
	if end < 0 {
		lock = lock[:start]
	} else {
		lock = lock[:start] + lock[start+1+end:]
	}
	testutil.WriteFile(t, path, lock)
}

func TestBookUpdateToAVersionThatIncludesBooksFetchesLocksAndAsksAboutEachInOneRun(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	two := h.goBookThatIncludesObserve(t, books, "v2")
	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update", "go", "--to", "v2")
	h.expectStdout("book observe ("+observeRef+")", "~/observe", "book tools (github.com/example-org/books//backend/tools@"+two+")", "~/tools")
	if got := strings.Count(h.stderr.String()+h.stdout.String(), "[y/N]"); got != 1 {
		t.Errorf("asked %d times, want once:\n%s", got, h.stdout.String()+h.stderr.String())
	}
	lock := filepath.Join(h.home, "stage.lock")
	for _, ref := range []string{observeRef, "github.com/example-org/books//backend/tools@" + two} {
		if h.lockedCommit(lock, ref) == "" {
			t.Errorf("lock lacks %s:\n%s", ref, testutil.ReadFile(t, lock))
		}
	}
	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "after", "--book", "go")
	task := testutil.ReadFile(t, filepath.Join(h.task("after"), "task.json"))
	for _, take := range []string{"~/go", "~/observe", "~/tools"} {
		if !strings.Contains(task, take) {
			t.Errorf("task lacks %s:\n%s", take, task)
		}
	}
}

func TestADeclinedUpdateLocksAndApprovesNoneOfTheBooksItIncludes(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	h.goBookThatIncludesObserve(t, books, "v2")
	before := h.bookState()
	h.answerAtATerminal("n\n")
	h.expect(1, "book", "update", "go", "--to", "v2")
	if h.bookState() != before {
		t.Fatal("a declined update wrote something")
	}
}

func TestABareBookUpdateLocksAndAsksAboutIncludedBooksEvenWhenTheBookIsUnchanged(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	h.goBookThatIncludesObserve(t, books, "v1")
	writeGitHubSetup(t, h.home, "[books]\ngo = \""+goBookRef+"\"\n")
	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "first", "--book", "go")

	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update")
	h.expectStdout(goBookRef, "unchanged", "book observe ("+observeRef+")")
	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "after", "--book", "go")

	h.withoutLockEntry(t, observeRef)
	h.answerAtATerminal("")
	h.expect(0, "book", "update")
	if strings.Contains(h.stderr.String()+h.stdout.String(), "[y/N]") {
		t.Errorf("asked about an approved include:\n%s", h.stdout.String()+h.stderr.String())
	}
	if h.lockedCommit(filepath.Join(h.home, "stage.lock"), observeRef) == "" {
		t.Errorf("the include was not locked again:\n%s", testutil.ReadFile(t, filepath.Join(h.home, "stage.lock")))
	}
	before := h.bookState()
	h.expect(0, "book", "update")
	h.expectStdout("unchanged")
	if h.bookState() != before {
		t.Error("an update that changed nothing wrote something")
	}
}

func TestBookListShowsWhatIncludesEachBookAndAnIncludeNotFetchedYet(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	h.goBookThatIncludesObserve(t, books, "v2")
	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update", "go", "--to", "v2")
	goV2 := strings.Replace(goBookRef, "@v1", "@v2", 1)

	h.expect(0, "book", "list")
	h.expectStdout("observe  "+observeRef, "included by go ("+goV2+")")

	h.withoutLockEntry(t, observeRef)
	h.stdout.Reset()
	h.expect(0, "book", "list")
	h.expectStdout(observeRef + "  included by go (" + goV2 + "); not fetched yet")
}

func TestBookListSaysALockedBookIsCachedEvenWhenItCannotDescribeIt(t *testing.T) {
	h := setup(t)
	h.booksRemote().publishFiles("v1", map[string]string{"backend/go/book.toml": "name = 3\n"})
	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "fix", "--book", goBookRef)
	if h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef) == "" {
		t.Fatalf("stage up did not lock the book:\n%s", testutil.ReadFile(t, filepath.Join(h.home, "stage.lock")))
	}
	h.stdout.Reset()
	h.expect(0, "book", "list")
	h.expectStdout("?  " + goBookRef)
	if !strings.Contains(h.stdout.String(), "  cached") || strings.Contains(h.stdout.String(), "not cached") {
		t.Errorf("a cached book it cannot describe is not listed as cached:\n%s", h.stdout.String())
	}
}

func TestBookUpdateFromTakesTheRepositorysEntryForAnIncludedBookAndAsksAboutIt(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	h.goBookThatIncludesObserve(t, books, "v1")
	writeGitHubSetup(t, h.home, "[books]\ngo = \""+goBookRef+"\"\n")
	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update")
	lockPath, trustPath := filepath.Join(h.home, "stage.lock"), filepath.Join(h.home, "trusted-kits")
	yours, approved := testutil.ReadFile(t, lockPath), testutil.ReadFile(t, trustPath)
	first := h.lockedCommit(lockPath, observeRef)

	moved := books.publishFiles("v1", map[string]string{"shared/observe/book.toml": bookToml("observe", "", "takes = [\"~/observe-moved\"]\n")})
	h.expect(0, "book", "update", observeRef)
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "stage.lock"), testutil.ReadFile(t, lockPath))
	testutil.WriteFile(t, lockPath, yours)
	testutil.WriteFile(t, trustPath, approved)

	h.answerAtATerminal("n\n")
	h.expect(1, "book", "update", "go", "--from", h.remote.Checkout)
	h.expectStdout("book observe ("+observeRef+")", "~/observe-moved")
	if got := h.lockedCommit(lockPath, observeRef); got != first {
		t.Fatalf("a declined update moved the include to %s, want your %s", got, first)
	}

	h.stdout.Reset()
	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update", "go", "--from", h.remote.Checkout)
	h.expectStdout("book observe ("+observeRef+")", "~/observe-moved")
	if got := h.lockedCommit(lockPath, observeRef); got != moved {
		t.Errorf("the include is at %s, want the repository's %s", got, moved)
	}
	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "after", "--book", "go")
}

func TestBookLockLocksAndAsksAboutABookIncludedByReference(t *testing.T) {
	h := setup(t)
	books := h.booksRemote()
	h.goBookThatIncludesObserve(t, books, "v1")
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "config.toml"), "books = [\""+goBookRef+"\"]\n")
	h.answerAtATerminal("y\n")
	h.expect(0, "book", "lock", h.remote.Checkout)
	h.expectStderr("  - book observe (" + observeRef + ")")
	written := testutil.ReadFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "stage.lock"))
	if !strings.Contains(written, "ref = \""+observeRef+"\"") {
		t.Errorf("repository lock lacks the include:\n%s", written)
	}
	if _, err := os.Stat(filepath.Join(h.home, "stage.lock")); err != nil {
		t.Error(err)
	}
}
