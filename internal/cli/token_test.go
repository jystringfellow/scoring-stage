package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type fakeGitHub struct {
	transport *testutil.HandlerTransport
	mu        sync.Mutex
	status    int
	expiry    string
	requests  []string
	tokens    []string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	fake := &fakeGitHub{status: http.StatusOK}
	fake.transport = &testutil.HandlerTransport{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.requests = append(fake.requests, r.Method+" "+r.URL.Path)
		fake.tokens = append(fake.tokens, r.Header.Get("Authorization"))
		if fake.expiry != "" {
			w.Header().Set(config.GitHubTokenExpiryHeader, fake.expiry)
		}
		w.WriteHeader(fake.status)
	})}
	return fake
}

func (f *fakeGitHub) api() config.GitHubAPI {
	return config.GitHubAPI{BaseURL: config.GitHubAPIURL, Client: f.transport.Client()}
}

func (f *fakeGitHub) answer(status int, expiry string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.expiry = status, expiry
}

func (f *fakeGitHub) lastRequest() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return "", ""
	}
	return f.requests[len(f.requests)-1], f.tokens[len(f.tokens)-1]
}

func (f *fakeGitHub) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func expiryIn(d time.Duration) string {
	return time.Now().Add(d).UTC().Format("2006-01-02 15:04:05") + " UTC"
}

func (h *harness) tokenFile(owner string) string {
	return filepath.Join(h.home, "secrets", "github", owner+".token")
}

func (h *harness) expectOutputLacks(secret string) {
	h.t.Helper()
	if strings.Contains(h.stdout.String()+h.stderr.String(), secret) {
		h.t.Fatalf("the token was printed:\nstdout: %s\nstderr: %s", h.stdout.String(), h.stderr.String())
	}
}

func TestTokenSetSavesATokenPipedOnStdinWithMode600(t *testing.T) {
	h := setup(t)
	h.stdin = "github_pat_globexsecret\n"
	h.expect(0, "token", "set", "Globex")
	file := h.tokenFile("globex")
	if got := testutil.ReadFile(t, file); got != "github_pat_globexsecret\n" {
		t.Fatalf("token file = %q", got)
	}
	for path, mode := range map[string]os.FileMode{file: 0o600, filepath.Dir(file): 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s mode %v, want %v", path, info.Mode().Perm(), mode)
		}
	}
	if !strings.Contains(h.stdout.String(), "saved the GitHub token for globex at "+file) {
		t.Errorf("stdout %q", h.stdout.String())
	}
	if request, auth := h.github.lastRequest(); request != "GET /rate_limit" || auth != "Bearer github_pat_globexsecret" {
		t.Errorf("checked the token with %q (%q)", request, auth)
	}
	h.expectOutputLacks("globexsecret")
	entries, _ := os.ReadDir(filepath.Dir(file))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".token-") {
			t.Errorf("left a temporary file %s", entry.Name())
		}
	}
}

func TestTokenSetReadsWithoutEchoWhenOnlyStdinIsATerminal(t *testing.T) {
	h := setup(t)
	h.stdinTerminal = true
	h.terminal = false
	h.stdin = "github_pat_fromstdin\n"
	h.typed = "github_pat_typedsecret"
	h.expect(0, "token", "set", "globex")
	if got := testutil.ReadFile(t, h.tokenFile("globex")); got != "github_pat_typedsecret\n" {
		t.Fatalf("token file = %q", got)
	}
	h.expectStderr("(input hidden)")
	h.expectOutputLacks("typedsecret")
}

func TestTokenSetReadsPipedStdinEvenWhenStdoutIsATerminal(t *testing.T) {
	h := setup(t)
	h.terminal = true
	h.stdin = "github_pat_piped\n"
	h.typed = "github_pat_typed"
	h.expect(0, "token", "set", "globex")
	if got := testutil.ReadFile(t, h.tokenFile("globex")); got != "github_pat_piped\n" {
		t.Fatalf("token file = %q", got)
	}
	if strings.Contains(h.stderr.String(), "(input hidden)") {
		t.Error("prompted for a hidden token while stdin was a pipe")
	}
}

func TestTokenSetReplacesAnExistingTokenAndSaysSo(t *testing.T) {
	h := setup(t)
	h.stdin = "github_pat_newsecret"
	h.expect(0, "token", "set", "acme")
	if got := testutil.ReadFile(t, h.tokenFile("acme")); got != "github_pat_newsecret\n" {
		t.Fatalf("token file = %q", got)
	}
	if !strings.Contains(h.stdout.String(), "replaced the GitHub token for acme") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	if info, err := os.Stat(h.tokenFile("acme")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("replaced token mode %v", info.Mode().Perm())
	}
}

func TestTokenSetRefusesATokenThatIsNotFineGrainedAndWritesNothing(t *testing.T) {
	h := setup(t)
	for _, token := range []string{"gho_fromghauth", "ghp_classic", "", "github_pat_one\ngithub_pat_two"} {
		h.stdin = token
		h.expect(1, "token", "set", "globex")
		if _, err := os.Stat(h.tokenFile("globex")); !os.IsNotExist(err) {
			t.Fatalf("wrote a token file for %q", token)
		}
	}
	h.stdin = "gho_fromghauth"
	h.expect(1, "token", "set", "acme")
	h.expectStderr("gh CLI login token (gho_)")
	if got := testutil.ReadFile(t, h.tokenFile("acme")); got != "github_pat_readonly\n" {
		t.Fatalf("overwrote the acme token with a refused one: %q", got)
	}
	if h.github.requestCount() != 0 {
		t.Error("sent a refused token to GitHub")
	}
}

func TestTokenSetRefusesATokenGitHubRejects(t *testing.T) {
	h := setup(t)
	h.github.answer(http.StatusUnauthorized, "")
	h.stdin = "github_pat_revoked"
	h.expect(1, "token", "set", "globex")
	h.expectStderr("GitHub rejected the token (401")
	h.expectStderr("nothing was saved")
	if _, err := os.Stat(h.tokenFile("globex")); !os.IsNotExist(err) {
		t.Fatal("saved a token GitHub rejected")
	}
}

func TestTokenSetRefusesABadOwner(t *testing.T) {
	h := setup(t)
	for _, owner := range []string{"../acme", "acme/api", "acme-", "ac me"} {
		h.stdin = "github_pat_secret"
		h.expect(1, "token", "set", owner)
		h.expectStderr("is not a GitHub owner name")
	}
	if entries, _ := os.ReadDir(filepath.Join(h.home, "secrets", "github")); len(entries) != 1 {
		t.Errorf("token dir holds %d entries", len(entries))
	}
	h.expect(2, "token", "get", "acme")
	h.expectStderr("choose from set")
}

func TestTokenSetSavesWhenGitHubIsUnreachableAndReportsTheExpiry(t *testing.T) {
	h := setup(t)
	h.github.answer(http.StatusOK, expiryIn(30*24*time.Hour))
	h.stdin = "github_pat_secret"
	h.expect(0, "token", "set", "globex")
	if !strings.Contains(h.stdout.String(), "github token for globex expires") || !strings.Contains(h.stdout.String(), "(in 29 days)") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	h.github.transport.Unreachable()
	h.stdin = "github_pat_other"
	h.expect(0, "token", "set", "initech")
	h.expectStderr("could not check the token with GitHub")
	if _, err := os.Stat(h.tokenFile("initech")); err != nil {
		t.Fatal("did not save the token while GitHub was unreachable")
	}
}

func TestTokenSetWarnsWhenTheModeIsNotFineGrained(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"none\"\n")
	h.stdin = "github_pat_secret"
	h.expect(0, "token", "set", "globex")
	h.expectStderr("only with [github] mode = \"fine-grained\"")
}

func TestUpChecksTheTokenCanSeeTheRepository(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	if request, auth := h.github.lastRequest(); request != "GET /repos/acme/api" || auth != "Bearer github_pat_readonly" {
		t.Errorf("checked with %q (%q)", request, auth)
	}
	if strings.Contains(h.stderr.String(), "token") {
		t.Errorf("warned about a good token: %s", h.stderr.String())
	}
}

func TestUpRefusesATokenGitHubRejects(t *testing.T) {
	h := setup(t)
	h.github.answer(http.StatusUnauthorized, "")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("GitHub rejected the token for acme")
	h.expectStderr("run `stage token set acme`")
	h.expectOutputLacks("github_pat_readonly")
	if h.anyCall("env create") {
		t.Error("created a sandbox with a rejected token")
	}
}

func TestUpWarnsWhenTheTokenExpiresWithinAWeek(t *testing.T) {
	h := setup(t)
	h.github.answer(http.StatusOK, expiryIn(3*24*time.Hour+time.Hour))
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("the GitHub token for acme expires")
	h.expectStderr("(in 3 days)")
	h.github.answer(http.StatusOK, expiryIn(30*24*time.Hour))
	h.expect(0, "up", h.remote.Checkout, "later", "--plan")
	if strings.Contains(h.stderr.String(), "expires") {
		t.Errorf("warned about a token a month from expiry: %s", h.stderr.String())
	}
}

func TestUpWarnsWhenTheTokenCannotSeeTheRepository(t *testing.T) {
	h := setup(t)
	h.github.answer(http.StatusNotFound, "")
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("the GitHub token for acme cannot see acme/api (GitHub answered 404)")
	h.expectStderr("resource owner")
	h.github.answer(http.StatusForbidden, "")
	h.expect(0, "up", h.remote.Checkout, "other", "--plan")
	h.expectStderr("the GitHub token for acme cannot see acme/api (GitHub answered 403)")
	h.expectStderr("SAML SSO")
	h.expectStderr("rate limit")
}

func TestUpWarnsButContinuesWhenGitHubAcceptsATokenWhoseExpiryReadsAsPast(t *testing.T) {
	h := setup(t)
	h.github.answer(http.StatusOK, expiryIn(-time.Hour))
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("GitHub accepted the token for acme, but its expiry header (")
	h.expectStderr("check this machine's clock")
	if !h.anyCall("env create") {
		t.Error("did not create the sandbox for a token GitHub accepted")
	}
}

func TestUpDoesNotCallATokenAcceptedWhenGitHubAnsweredAnErrorWithAPastExpiry(t *testing.T) {
	h := setup(t)
	h.github.answer(http.StatusForbidden, expiryIn(-time.Hour))
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("GitHub answered 403 for the token for acme, and its expiry header (")
	h.expectStderr("run `stage token set acme`")
	if strings.Contains(h.stderr.String(), "GitHub accepted") {
		t.Errorf("said GitHub accepted a token it answered 403 for: %s", h.stderr.String())
	}
}

func TestUpContinuesWhenGitHubIsUnreachable(t *testing.T) {
	h := setup(t)
	h.github.transport.Unreachable()
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expectStderr("could not check the GitHub token for acme")
	if !h.anyCall("env create") {
		t.Error("did not create the sandbox")
	}
}

func TestUpSkipsTheTokenCheckOutsideFineGrainedMode(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"none\"\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	if h.github.requestCount() != 0 {
		t.Error("checked a token in none mode")
	}
}

func TestSetupReportsEachTokensExpiry(t *testing.T) {
	h := setup(t)
	writeToken(t, h.tokenFile("globex"), "github_pat_globex\n")
	h.github.answer(http.StatusOK, "")
	h.expect(0, "setup")
	if !strings.Contains(h.stdout.String(), "github token for acme has no expiry") || !strings.Contains(h.stdout.String(), "github token for globex has no expiry") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	h.github.answer(http.StatusOK, expiryIn(60*24*time.Hour))
	h.expect(0, "setup")
	if !strings.Contains(h.stdout.String(), "github token for acme expires") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	h.github.answer(http.StatusOK, expiryIn(2*24*time.Hour))
	h.expect(0, "setup")
	h.expectStderr("the GitHub token for acme expires")
	h.github.answer(http.StatusOK, expiryIn(-time.Hour))
	h.expect(0, "setup")
	h.expectStderr("GitHub accepted the token for acme, but its expiry header (")
	h.expectStderr("check this machine's clock")
	h.github.answer(http.StatusUnauthorized, "")
	h.expect(0, "setup")
	h.expectStderr("GitHub rejected the token for globex")
	h.github.transport.Unreachable()
	h.expect(0, "setup")
	h.expectStderr("could not check the GitHub token for acme")
	h.expectOutputLacks("github_pat_readonly")
	h.expectOutputLacks("github_pat_globex")
}
