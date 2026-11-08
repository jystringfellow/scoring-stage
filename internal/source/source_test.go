package source

import (
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
