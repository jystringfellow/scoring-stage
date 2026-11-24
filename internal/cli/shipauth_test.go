package cli

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func (h *harness) githubNeedingALogin() {
	h.t.Helper()
	h.t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	h.t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		h.t.Fatal(err)
	}
	backend := &cgi.Handler{
		Path: filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(h.remote.Bare), "GIT_HTTP_EXPORT_ALL=1"},
	}
	testutil.RunGit(h.t, h.remote.Bare, "config", "http.receivepack", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "x-access-token" || password != "host-login" {
			w.Header().Set("WWW-Authenticate", `Basic realm="github"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	h.t.Cleanup(server.Close)
	h.t.Setenv("GIT_CONFIG_KEY_0", "url."+server.URL+"/"+filepath.Base(h.remote.Bare)+".insteadOf")
	h.t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

func (h *harness) hostGitLogin(answer bool) string {
	h.t.Helper()
	dir := h.t.TempDir()
	log := filepath.Join(dir, "asked")
	body := "#!/bin/sh\necho \"$1\" >> " + log + "\n"
	if answer {
		body += "[ \"$1\" = get ] && printf 'username=x-access-token\\npassword=host-login\\n'\nexit 0\n"
	}
	helper := testutil.WriteFile(h.t, filepath.Join(dir, "helper"), body)
	if err := os.Chmod(helper, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.t.Setenv("GIT_CONFIG_GLOBAL", testutil.WriteFile(h.t, filepath.Join(dir, "gitconfig"), "[credential]\n\thelper = "+helper+"\n"))
	return log
}

func TestShipReadsTheRemoteWithTheSameGitCredentialsItPushesWith(t *testing.T) {
	h := setup(t)
	h.githubNeedingALogin()
	h.hostGitLogin(true)
	h.expect(0, "up", h.remote.Checkout, "fix")
	box := h.sandboxCommit("fix")
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	asked := h.hostGitLogin(true)

	d := readyMcp(t, h)
	data := toolData(t, d.tool("ship", map[string]any{"slug": "fix"}))
	if !strings.Contains(data["diffstat"].(string), "change.txt") {
		t.Fatalf("mcp review %v", data)
	}
	reads := strings.Count(testutil.ReadFile(t, asked), "get")
	if reads == 0 {
		t.Fatal("the mcp review read the remote without asking the git credential helper")
	}

	h.expect(0, "ship", "fix", "--yes")
	h.expectPushed("agent/fix/"+sha[:8], sha)
	h.expectStdout("compared with main at ")
	if total := strings.Count(testutil.ReadFile(t, asked), "get"); total < reads+2 {
		t.Errorf("ship asked the credential helper %d times, want its reads and its push to ask", total-reads)
	}
}

func TestShipWithoutAGitLoginForTheRemoteSaysSoBeforeItsSummary(t *testing.T) {
	h := setup(t)
	h.githubNeedingALogin()
	h.hostGitLogin(true)
	h.expect(0, "up", h.remote.Checkout, "fix")
	box := h.sandboxCommit("fix")
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.hostGitLogin(false)

	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("could not read acme/api from GitHub to compare the shipment with")
	h.expectStderr("ship reads with the git credentials it pushes with")
	if strings.Contains(h.stdout.String(), "shipping to") {
		t.Errorf("showed a summary it could not compare: %s", h.stdout.String())
	}
	h.expectNotPushed("agent/fix/" + sha[:8])

	d := readyMcp(t, h)
	toolError(t, d.tool("ship", map[string]any{"slug": "fix"}), "could not read acme/api from GitHub")
}
