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

func TestPrepareReportsAnUnknownBase(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	_, _, err := Prepare(remote.Checkout, filepath.Join(t.TempDir(), "src"), "acme/api", "no-such-branch")
	if err == nil || !strings.Contains(err.Error(), "git fetch") {
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
