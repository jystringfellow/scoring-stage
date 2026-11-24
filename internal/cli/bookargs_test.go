package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestARepositorysArgumentsForABookByReferenceAreApprovedThenReachTheEnvFileAndTheLockKeepsThePlainReference(t *testing.T) {
	h := setup(t)
	commit := h.booksRemote().publishFiles("v1", map[string]string{
		"tools/dotnet/book.toml":               "name = \"dotnet\"\nkits = [\"./kits/dotnet\"]\n",
		"tools/dotnet/kits/dotnet/dotnet.yaml": "args:\n  runtimes:\n    default: \"\"\n    pattern: '^([a-z]+:[0-9]+\\.[0-9]+)?$'\ncapabilities: []\n",
	})
	ref := "github.com/example-org/books//tools/dotnet@v1"
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "books = [{source = \"" + ref + "\", args = {dotnet = {runtimes = \"aspnetcore:8.0\"}}}]\n"})

	h.noTerminal()
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("  - book dotnet (" + ref + ")")
	h.expectStderr("argument dotnet.runtimes = aspnetcore:8.0 (from ")
	if h.anyCall("env create", "env plan") {
		t.Fatalf("a book's unapproved arguments reached sbx: %v", h.sbx.Calls())
	}

	h.answerAtATerminal("y\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	if !strings.Contains(env, `runtimes: "aspnetcore:8.0"`) {
		t.Errorf("env file lacks the argument:\n%s", env)
	}
	if got := h.lockedCommit(filepath.Join(h.home, "stage.lock"), ref); got != commit {
		t.Errorf("lock holds %q for the plain reference, want %s", got, commit)
	}
}
