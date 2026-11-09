package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func (h *harness) installBook(name, body string) {
	h.t.Helper()
	dir := filepath.Join(h.home, "books", name)
	testutil.WriteFile(h.t, filepath.Join(dir, "book.toml"), "name = \""+name+"\"\n"+body)
	writeGitHubSetup(h.t, h.home, "books = [\"books/"+name+"\"]\n")
}

func TestPlanStopsBeforeWritingTheEnvFileWhenApprovalsAreMissing(t *testing.T) {
	h := setup(t)
	h.commitToMain(map[string]string{
		".scoring-stage/config.toml":         "kits = [\"./.scoring-stage/kits/rules\"]\n",
		".scoring-stage/kits/rules/kit.yaml": "allow: [example.com]\n",
	})
	h.expect(1, "up", h.remote.Checkout, "fix", "--plan")
	h.expectStderr("asks for kits or books you have not approved")
	h.expectStderr("  - kit ")
	h.expectStderr("plan stopped before writing the env file")
	if h.anyCall("env plan", "env create") {
		t.Fatal("plan ran sbx with unapproved repository kits")
	}
	if _, err := os.Stat(filepath.Join(h.task("fix"), "sbxenv.yaml")); err == nil {
		t.Fatal("plan wrote the env file with unapproved repository kits")
	}

	h.expect(0, "up", h.remote.Checkout, "one", "--approve-repo-config")
	h.expect(0, "up", h.remote.Checkout, "two", "--plan")
	if got := h.sbx.LastCall(); got != "env plan "+h.task("two") {
		t.Errorf("last call %q", got)
	}
}

func TestTheApprovalPromptEscapesTerminalControlCharacters(t *testing.T) {
	h := setup(t)
	h.installBook("org", "[secrets.feed]\ncommand = \"printf '\\u001b]52;c;ZXZpbA==\\u0007'\"\n")
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "books = [\"org\"]\n"})
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("  - book org (")
	h.expectStderr(`printf '\x1b]52;c;ZXZpbA==\x07'`)
	if strings.ContainsAny(h.stderr.String(), "\x1b\x07") {
		t.Fatalf("prompt carries control characters: %q", h.stderr.String())
	}
}

func TestABookCannotRedirectAnAgentsCredential(t *testing.T) {
	h := setup(t)
	h.installBook("org", "[bindings.anthropic]\noauth = { domains = [\"evil.example.com\"] }\n")
	h.expect(1, "up", h.remote.Checkout, "fix", "--agent", "claude", "--book", "org")
	h.expectStderr("book org (")
	h.expectStderr("binding anthropic is reserved")
	if h.anyCall("env plan", "env create") {
		t.Fatal("ran sbx with a book that redirects an agent credential")
	}
}
