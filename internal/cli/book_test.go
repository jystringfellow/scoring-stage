package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func (r *booksRemote) publish(tag, book string) string {
	r.h.t.Helper()
	testutil.WriteFile(r.h.t, filepath.Join(r.work, "backend/go/book.toml"), book)
	testutil.RunGit(r.h.t, r.work, "add", "-A")
	testutil.RunGit(r.h.t, r.work, "commit", "-q", "--allow-empty", "-m", tag)
	testutil.RunGit(r.h.t, r.work, "tag", "-f", tag)
	testutil.RunGit(r.h.t, r.work, "push", "-q", "-f", r.bare, "main", "refs/tags/"+tag)
	return testutil.RunGit(r.h.t, r.work, "rev-parse", "HEAD")
}

const (
	bookWithFeed  = "name = \"go\"\n[secrets.feed]\ncommand = \"echo stage-probe-dummy\"\n"
	bookWithToken = "name = \"go\"\n[secrets.feed]\ncommand = \"cat ~/.feed-token\"\n"
)

func (h *harness) withGoBook(t *testing.T) *booksRemote {
	t.Helper()
	books := h.booksRemote()
	books.publish("v1", bookWithFeed)
	writeGitHubSetup(t, h.home, "# team books\n[books]\ngo = \""+goBookRef+"\"   # pinned\n")
	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "first", "--book", "go")
	return books
}

type bookState struct{ lock, config, trust string }

func (h *harness) bookState() bookState {
	read := func(name string) string {
		content, _ := os.ReadFile(filepath.Join(h.home, name))
		return string(content)
	}
	return bookState{read("stage.lock"), read("config.toml"), read("trusted-kits")}
}

func TestBookListShowsCachedBooksWhatNamesThemAndWhatIsNotFetched(t *testing.T) {
	h := setup(t)
	h.withGoBook(t)
	writeGitHubSetup(t, h.home, "[books]\ngo = \""+goBookRef+"\"\nlater = \"github.com/example-org/books//backend/rust@v2\"\n")
	h.expect(0, "book", "list")
	h.expectStdout("go", goBookRef, "short name go", "cached")
	h.expectStdout("github.com/example-org/books//backend/rust@v2", "not fetched")
}

func TestBookPruneRemovesOnlyWhatYourLockDoesNotNameAndSaysSoFirst(t *testing.T) {
	h := setup(t)
	h.withGoBook(t)
	kept, err := os.ReadDir(filepath.Join(h.home, "books"))
	if err != nil || len(kept) != 1 {
		t.Fatalf("cache %v, %v", kept, err)
	}
	stale := filepath.Join(h.home, "books", strings.Repeat("d", 64))
	testutil.WriteFile(t, filepath.Join(stale, "book.toml"), "name = \"old\"\n")
	os.Chmod(stale, 0o555)
	partial := filepath.Join(h.home, "books", ".fetch-123")
	testutil.WriteFile(t, filepath.Join(partial, "book.toml"), "name = \"partial\"\n")

	h.expect(0, "book", "prune")
	h.expectStdout("removing "+stale, "removing "+partial)
	for _, path := range []string{stale, partial} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s is still there", path)
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "books", kept[0].Name())); err != nil {
		t.Errorf("removed the locked book: %v", err)
	}
	h.expect(0, "book", "prune")
	h.expectStdout("nothing to prune")
}

func TestBookUpdateSaysTheTagMovedShowsWhatChangedAndAsks(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	before := h.bookState()
	old := h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef)
	moved := books.publish("v1", bookWithToken)

	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update", "go")
	h.expectStdout("tag v1 moved", old[:12], moved[:12])
	h.expectStdout("- host command for secret feed", "echo stage-probe-dummy")
	h.expectStdout("+ host command for secret feed", "cat ~/.feed-token")
	if got := h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef); got != moved {
		t.Errorf("lock holds %s, want %s", got, moved)
	}
	if after := h.bookState(); after.config != before.config || after.trust == before.trust {
		t.Errorf("config changed or no approval recorded")
	}
	h.noTerminal()
	h.expect(0, "up", h.remote.Checkout, "after", "--book", "go")
}

func TestBookUpdateWithNothingChangedAsksNothing(t *testing.T) {
	h := setup(t)
	h.withGoBook(t)
	before := h.bookState()
	h.answerAtATerminal("")
	h.expect(0, "book", "update")
	h.expectStdout(goBookRef, "unchanged")
	if strings.Contains(h.stderr.String(), "[y/N]") {
		t.Errorf("asked a question: %s", h.stderr.String())
	}
	if h.bookState() != before {
		t.Error("an update that changed nothing wrote something")
	}
}

func TestADeclinedOrFailedUpdateLeavesLockApprovalAndConfigAsTheyWere(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	books.publish("v1", bookWithToken)
	books.publish("v2", bookWithToken+"description = \"two\"\n")
	before := h.bookState()

	h.answerAtATerminal("n\n")
	h.expect(1, "book", "update", "go")
	if h.bookState() != before {
		t.Fatal("a declined update wrote something")
	}

	h.noTerminal()
	h.expect(1, "book", "update", "go")
	h.expectStderr("in a terminal")
	if h.bookState() != before {
		t.Fatal("an update without a terminal wrote something")
	}

	guard := filepath.Join(h.home, "stage.lock.lock")
	os.Remove(guard)
	if err := os.Mkdir(guard, 0o755); err != nil {
		t.Fatal(err)
	}
	h.answerAtATerminal("y\n")
	h.expect(1, "book", "update", "go", "--to", "v2")
	if after := h.bookState(); after != before {
		t.Fatalf("a failed update left changes:\nlock %q\nconfig %q\ntrust %q", after.lock, after.config, after.trust)
	}
}

func TestBookUpdateToChangesOnlyThatValueInYourConfig(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	two := books.publish("v2", bookWithFeed)
	before := h.bookState()
	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update", "go", "--to", "v2")
	newRef := strings.Replace(goBookRef, "@v1", "@v2", 1)
	if want := strings.Replace(before.config, goBookRef, newRef, 1); h.bookState().config != want {
		t.Errorf("config:\n%s\nwant:\n%s", h.bookState().config, want)
	}
	if got := h.lockedCommit(filepath.Join(h.home, "stage.lock"), newRef); got != two {
		t.Errorf("lock holds %q for %s, want %s", got, newRef, two)
	}
}

func TestBookUpdateToChangesNothingWhenTheValueIsNotFoundExactlyOnce(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	books.publish("v2", bookWithFeed)
	writeGitHubSetup(t, h.home, "# was \""+goBookRef+"\"\n[books]\ngo = \""+goBookRef+"\"\n")
	before := h.bookState()
	h.answerAtATerminal("y\n")
	h.expect(1, "book", "update", "go", "--to", "v2")
	h.expectStderr("edit it by hand")
	h.expectStderr(strings.Replace(goBookRef, "@v1", "@v2", 1))
	if h.bookState() != before {
		t.Fatal("an ambiguous --to wrote something")
	}

	other := "github.com/example-org/books//backend/go@v9"
	h.expect(1, "book", "update", other, "--to", "v2")
	h.expectStderr("not in your config")
}

func (h *harness) repoLockWith(t *testing.T, commit string) string {
	t.Helper()
	mine := testutil.ReadFile(t, filepath.Join(h.home, "stage.lock"))
	old := h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef)
	return strings.Replace(mine, old, commit, 1)
}

func TestBookUpdateFromTakesTheRepositorysEntry(t *testing.T) {
	h := setup(t)
	books := h.withGoBook(t)
	pinned := h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef)
	team := testutil.ReadFile(t, filepath.Join(h.home, "stage.lock"))
	books.publish("v1", bookWithToken)
	h.answerAtATerminal("y\n")
	h.expect(0, "book", "update", "go")
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "stage.lock"), team)

	h.expect(0, "book", "update", goBookRef, "--from", h.remote.Checkout)
	h.expectStdout("+ host command for secret feed", "echo stage-probe-dummy")
	if got := h.lockedCommit(filepath.Join(h.home, "stage.lock"), goBookRef); got != pinned {
		t.Errorf("lock holds %s, want the repository's %s", got, pinned)
	}
}

func TestBookLockWritesTheRepositorysLockFromYours(t *testing.T) {
	h := setup(t)
	h.withGoBook(t)
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "config.toml"), "books = [\""+goBookRef+"\"]\n")
	h.answerAtATerminal("")
	h.expect(0, "book", "lock", h.remote.Checkout)
	h.expectStdout("added " + goBookRef)
	written := testutil.ReadFile(t, filepath.Join(h.remote.Checkout, ".scoring-stage", "stage.lock"))
	if mine := testutil.ReadFile(t, filepath.Join(h.home, "stage.lock")); written != mine {
		t.Errorf("repository lock:\n%s\nwant yours:\n%s", written, mine)
	}
	if _, err := os.Stat(filepath.Join(h.remote.Checkout, ".scoring-stage", "stage.lock.lock")); err == nil {
		t.Error("left a guard file in the repository")
	}
	h.expect(0, "book", "lock", h.remote.Checkout)
	h.expectStdout("unchanged")

	h.expect(1, "book", "lock", filepath.Join(h.remote.Checkout, ".scoring-stage"))
	h.expectStderr("top level of a git checkout")
	h.noTerminal()
	h.expect(1, "book", "lock", h.remote.Checkout)
	h.expectStderr("in a terminal")
}

func TestBookLockChangesAnEntryThatDiffersFromYours(t *testing.T) {
	h := setup(t)
	h.withGoBook(t)
	dir := filepath.Join(h.remote.Checkout, ".scoring-stage")
	testutil.WriteFile(t, filepath.Join(dir, "config.toml"), "books = [\""+goBookRef+"\"]\n")
	testutil.WriteFile(t, filepath.Join(dir, "stage.lock"), h.repoLockWith(t, strings.Repeat("e", 40)))
	h.answerAtATerminal("")
	h.expect(0, "book", "lock", h.remote.Checkout)
	h.expectStdout("changed "+goBookRef, "eeeeeeeeeeee")
	if written, mine := testutil.ReadFile(t, filepath.Join(dir, "stage.lock")), testutil.ReadFile(t, filepath.Join(h.home, "stage.lock")); written != mine {
		t.Errorf("repository lock not made to match yours:\n%s", written)
	}
}
