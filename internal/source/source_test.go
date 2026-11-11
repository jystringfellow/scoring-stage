package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestGitHubRepoFromOriginURL(t *testing.T) {
	for _, url := range []string{
		"git@github.com:acme/api.git",
		"ssh://git@github.com/acme/api.git",
		"https://github.com/acme/api.git",
		"https://github.com/acme/api",
		"https://github.com/acme/api/",
	} {
		if got, err := GitHubRepo(url); err != nil || got != "acme/api" {
			t.Errorf("GitHubRepo(%q) = %q, %v", url, got, err)
		}
	}
}

func TestNonGitHubOriginsAreRefused(t *testing.T) {
	for _, url := range []string{"https://gitlab.com/acme/api.git", "git@github.com:acme.git", "/srv/api.git"} {
		if _, err := GitHubRepo(url); err == nil || !strings.Contains(err.Error(), "not a github.com repository") {
			t.Errorf("GitHubRepo(%q): got %v", url, err)
		}
	}
}

func mustGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := Git(repo, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPrepareClonesDetachedAtTheDefaultBranchWithAGitHubOrigin(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	scratch := filepath.Join(t.TempDir(), "task", "src")
	base, head, err := Prepare(remote.Checkout, scratch, "acme/api", "")
	if err != nil {
		t.Fatal(err)
	}
	if base != "main" {
		t.Errorf("base = %q", base)
	}
	if want := mustGit(t, remote.Checkout, "rev-parse", "HEAD"); head != want {
		t.Errorf("head = %q, want %q", head, want)
	}
	if url := mustGit(t, scratch, "config", "--get", "remote.origin.url"); url != "https://github.com/acme/api.git" {
		t.Errorf("origin = %q", url)
	}
	if ref := mustGit(t, scratch, "rev-parse", "--abbrev-ref", "HEAD"); ref != "HEAD" {
		t.Errorf("HEAD = %q, want detached", ref)
	}
}

func TestPrepareFallsBackToTheRemoteHeadWhenOriginHEADIsUnset(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	testutil.RunGit(t, remote.Checkout, "remote", "set-head", "origin", "--delete")
	base, _, err := Prepare(remote.Checkout, filepath.Join(t.TempDir(), "src"), "acme/api", "")
	if err != nil || base != "main" {
		t.Fatalf("base = %q, %v", base, err)
	}
}

func TestPrepareReusesAnExistingScratchClone(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	scratch := filepath.Join(t.TempDir(), "task", "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	notes := testutil.WriteFile(t, filepath.Join(scratch, "agent-notes.txt"), "kept")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	if testutil.ReadFile(t, notes) != "kept" {
		t.Fatal("scratch clone was replaced")
	}
}

func originRefs(t *testing.T, repo string) string {
	t.Helper()
	return testutil.RunGit(t, repo, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin/")
}

func advanceRemoteBranch(t *testing.T, remote testutil.Remote, branch string) string {
	t.Helper()
	checkout := filepath.Join(t.TempDir(), "checkout")
	testutil.RunGit(t, remote.Root, "clone", "--quiet", remote.Bare, checkout)
	testutil.RunGit(t, checkout, "checkout", "--quiet", "-B", branch)
	testutil.WriteFile(t, filepath.Join(checkout, "change.txt"), branch+"\n")
	testutil.RunGit(t, checkout, "add", "change.txt")
	testutil.RunGit(t, checkout, "commit", "--quiet", "-m", "Advance branch")
	testutil.RunGit(t, checkout, "push", "--quiet", "origin", branch)
	return testutil.RunGit(t, checkout, "rev-parse", "HEAD")
}

func TestPrepareGivesTheScratchCloneTheRemotesMainWhenTheLocalCheckoutIsBehind(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	want := advanceRemoteBranch(t, remote, "main")
	scratch := filepath.Join(t.TempDir(), "src")
	_, head, err := Prepare(remote.Checkout, scratch, "acme/api", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, scratch, "rev-parse", "refs/remotes/origin/main"); got != want {
		t.Errorf("origin/main = %q, want %q", got, want)
	}
	if head != want {
		t.Errorf("head = %q, want %q", head, want)
	}
}

func TestPrepareLeavesNoOriginRefForABranchOnlyTheLocalCheckoutHas(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	testutil.RunGit(t, remote.Checkout, "branch", "local-only")
	scratch := filepath.Join(t.TempDir(), "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", ""); err != nil {
		t.Fatal(err)
	}
	if got := originRefs(t, scratch); got != "origin/main" {
		t.Errorf("origin refs = %q, want only origin/main", got)
	}
	if ref, err := Git(scratch, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if _, err := Git(scratch, "rev-parse", "--verify", ref); err != nil {
			t.Errorf("origin/HEAD points at a missing ref: %v", err)
		}
	}
}

func TestPrepareRemovesStaleOriginRefsFromAReusedScratchClone(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	scratch := filepath.Join(t.TempDir(), "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, scratch, "update-ref", "refs/remotes/origin/stale", "HEAD")
	testutil.RunGit(t, scratch, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/stale")
	want := advanceRemoteBranch(t, remote, "main")
	_, head, err := Prepare(remote.Checkout, scratch, "acme/api", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := originRefs(t, scratch); got != "origin/main" {
		t.Errorf("origin refs = %q, want only origin/main", got)
	}
	if got := mustGit(t, scratch, "rev-parse", "refs/remotes/origin/main"); got != want {
		t.Errorf("origin/main = %q, want %q", got, want)
	}
	if head != want {
		t.Errorf("head = %q, want %q", head, want)
	}
	if ref, err := Git(scratch, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		t.Errorf("origin/HEAD remains: %q", ref)
	}
}

func TestPrepareFetchesBothTheBaseAndTheDefaultBranchWhenTheyDiffer(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	release := advanceRemoteBranch(t, remote, "release")
	trunk := advanceRemoteBranch(t, remote, "trunk")
	testutil.RunGit(t, remote.Bare, "symbolic-ref", "HEAD", "refs/heads/trunk")
	scratch := filepath.Join(t.TempDir(), "src")
	base, head, err := Prepare(remote.Checkout, scratch, "acme/api", "release")
	if err != nil {
		t.Fatal(err)
	}
	if base != "release" || head != release {
		t.Errorf("base, head = %q, %q, want release, %q", base, head, release)
	}
	if got := originRefs(t, scratch); got != "origin/release\norigin/trunk" {
		t.Errorf("origin refs = %q, want only origin/release and origin/trunk", got)
	}
	for branch, want := range map[string]string{"release": release, "trunk": trunk} {
		if got := mustGit(t, scratch, "rev-parse", "refs/remotes/origin/"+branch); got != want {
			t.Errorf("origin/%s = %q, want %q", branch, got, want)
		}
	}
	if got := mustGit(t, scratch, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Errorf("HEAD = %q, want detached", got)
	}
}

func TestPrepareRefusesABaseThatCouldWidenTheFetch(t *testing.T) {
	for _, base := range []string{"*", "a:b", "a b", "x..y"} {
		t.Run(base, func(t *testing.T) {
			remote := testutil.GitHubRemote(t)
			scratch := filepath.Join(t.TempDir(), "src")
			if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", base); err == nil {
				t.Error("invalid base was accepted")
			}
			if got := originRefs(t, scratch); got != "" {
				t.Errorf("origin refs = %q, want none", got)
			}
			if _, err := os.Stat(filepath.Join(scratch, ".git", "FETCH_HEAD")); !os.IsNotExist(err) {
				t.Errorf("FETCH_HEAD exists or could not be checked: %v", err)
			}
		})
	}
}

func TestPrepareRemovesADanglingOriginHEADFromAReusedScratchClone(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	scratch := filepath.Join(t.TempDir(), "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, scratch, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/missing")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	if ref, err := Git(scratch, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		t.Errorf("dangling origin/HEAD remains: %q", ref)
	}
}

func TestPrepareLeavesNoStaleOriginRefsWhenFetchingTheBaseFails(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	scratch := filepath.Join(t.TempDir(), "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "main"); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, scratch, "update-ref", "refs/remotes/origin/stale", "HEAD")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "missing"); err == nil || !strings.Contains(err.Error(), "git fetch") {
		t.Errorf("unknown base error = %v, want git fetch failure", err)
	}
	if got := originRefs(t, scratch); got != "" {
		t.Errorf("origin refs = %q, want none", got)
	}
}

func TestPrepareFetchesAnExplicitBaseWhenTheRemoteHasNoDefaultBranch(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	testutil.RunGit(t, remote.Bare, "symbolic-ref", "HEAD", "refs/heads/missing")
	scratch := filepath.Join(t.TempDir(), "src")
	base, head, err := Prepare(remote.Checkout, scratch, "acme/api", "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := testutil.RunGit(t, remote.Bare, "rev-parse", "refs/heads/main"); base != "main" || head != want {
		t.Errorf("base, head = %q, %q, want main, %q", base, head, want)
	}
	if got := originRefs(t, scratch); got != "origin/main" {
		t.Errorf("origin refs = %q, want only origin/main", got)
	}
}

func TestPrepareReportsAnUnknownBase(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	_, _, err := Prepare(remote.Checkout, filepath.Join(t.TempDir(), "src"), "acme/api", "no-such-branch")
	if err == nil || !strings.Contains(err.Error(), "--base no-such-branch must be a branch on acme/api") || !strings.Contains(err.Error(), "git fetch") {
		t.Fatalf("got %v", err)
	}
}

func TestPrepareClonesWithoutRunningHooksTheHostGitConfigNames(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	hook := testutil.WriteFile(t, filepath.Join(dir, "hooks", "post-checkout"), "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	global := testutil.WriteFile(t, filepath.Join(dir, "gitconfig"), "[core]\n\thooksPath = "+filepath.Join(dir, "hooks")+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if _, _, err := Prepare(remote.Checkout, filepath.Join(dir, "task", "src"), "acme/api", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the initial clone ran a hook")
	}
}

func sbxRemote(sandbox, port string) string {
	return "[remote \"sandbox-" + sandbox + "\"]\n" +
		"\turl = git://127.0.0.1:" + port + "/src\n" +
		"\tfetch = +refs/heads/*:refs/remotes/sandbox-" + sandbox + "/*\n" +
		"\tfetch = +refs/heads/*:refs/sandboxes/" + sandbox + "/*\n"
}

func fingerprintWithConfig(t *testing.T, sandbox, config string) string {
	t.Helper()
	repo := t.TempDir()
	testutil.WriteFile(t, filepath.Join(repo, ".git", "config"), config)
	got, err := ConfigFingerprint(repo, sandbox)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

const baseConfig = "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://github.com/acme/api.git\n"

func TestConfigFingerprintIgnoresTheRemoteSbxAddsOnEachStart(t *testing.T) {
	stopped := fingerprintWithConfig(t, "stage-fix", baseConfig)
	for _, config := range []string{
		baseConfig + sbxRemote("stage-fix", "49207"),
		baseConfig + sbxRemote("stage-fix", "49208"),
		"[core]\n\tbare = false\n" + sbxRemote("stage-fix", "1") + "[remote \"origin\"]\n\turl = https://github.com/acme/api.git\n",
	} {
		if got := fingerprintWithConfig(t, "stage-fix", config); got != stopped {
			t.Errorf("fingerprint of\n%s\ndiffers from the stopped sandbox's", config)
		}
	}
}

func TestConfigFingerprintKeepsASandboxRemoteThatSbxDidNotWrite(t *testing.T) {
	stopped := fingerprintWithConfig(t, "stage-fix", baseConfig)
	sbx := sbxRemote("stage-fix", "49207")
	for name, config := range map[string]string{
		"another sandbox's remote": baseConfig + sbxRemote("stage-other", "49207"),
		"a remote host":            baseConfig + strings.Replace(sbx, "127.0.0.1", "203.0.113.9", 1),
		"another transport":        baseConfig + strings.Replace(sbx, "git://127.0.0.1:49207/src", "ext::sh -c touch% /tmp/x", 1),
		"another path":             baseConfig + strings.Replace(sbx, "/src", "/other", 1),
		"an upload-pack command":   baseConfig + sbx + "\tuploadpack = touch /tmp/x; git-upload-pack\n",
		"a changed refspec":        baseConfig + strings.Replace(sbx, "refs/sandboxes/stage-fix/*", "refs/heads/*", 1),
		"a second copy":            baseConfig + sbx + sbx,
		"an indented header":       baseConfig + sbx + " [core]\n\tsshCommand = x\n",
		"CRLF line endings":        baseConfig + strings.ReplaceAll(sbx, "\n", "\r\n"),
		"a continuation":           baseConfig + strings.Replace(sbx, "/src\n", "/src\\\n", 1),
	} {
		if got := fingerprintWithConfig(t, "stage-fix", config); got == stopped {
			t.Errorf("fingerprint ignores %s", name)
		}
	}
}

func TestConfigFingerprintWithoutASandboxHashesTheWholeConfig(t *testing.T) {
	stopped := fingerprintWithConfig(t, "", baseConfig)
	if got := fingerprintWithConfig(t, "", baseConfig+sbxRemote("", "49207")); got == stopped {
		t.Error("an empty sandbox name left a remote out of the hash")
	}
}

func TestPrepareTakesTheDefaultBranchFromGitHubOverTheLocalCheckoutsOriginHEAD(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	advanceRemoteBranch(t, remote, "trunk")
	testutil.RunGit(t, remote.Bare, "symbolic-ref", "HEAD", "refs/heads/trunk")
	scratch := filepath.Join(t.TempDir(), "src")
	base, head, err := Prepare(remote.Checkout, scratch, "acme/api", "")
	if err != nil || base != "trunk" {
		t.Fatalf("base = %q, %v", base, err)
	}
	if want := testutil.RunGit(t, remote.Bare, "rev-parse", "trunk"); head != want {
		t.Errorf("HEAD %s, want trunk %s", head, want)
	}
}

func TestPrepareTreatsALeadingPlusInTheBaseAsPartOfTheBranchName(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	testutil.RunGit(t, remote.Bare, "branch", "+x", "main")
	scratch := filepath.Join(t.TempDir(), "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "+x"); err != nil {
		t.Fatal(err)
	}
	if got := originRefs(t, scratch); !strings.Contains(got, "origin/+x") {
		t.Errorf("origin refs = %q, want origin/+x", got)
	}
}
