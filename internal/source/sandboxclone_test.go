package source

import (
	"path/filepath"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func cloneAsSbxDoes(t *testing.T, scratch string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "workspace")
	testutil.RunGit(t, filepath.Dir(scratch), "clone", "--quiet", "--reference", scratch, scratch, target)
	return target
}

func expectSandboxStartsFrom(t *testing.T, sandbox, base, commit string) {
	t.Helper()
	if got := testutil.RunGit(t, sandbox, "rev-parse", "--abbrev-ref", "HEAD"); got != base {
		t.Errorf("the sandbox clone is on %q, want %q", got, base)
	}
	if got := testutil.RunGit(t, sandbox, "rev-parse", "HEAD"); got != commit {
		t.Errorf("the sandbox clone starts at %q, want %q", got, commit)
	}
	if got := testutil.RunGit(t, sandbox, "rev-parse", "--abbrev-ref", base+"@{upstream}"); got != "origin/"+base {
		t.Errorf("%s tracks %q, want origin/%s", base, got, base)
	}
	if got := testutil.RunGit(t, sandbox, "rev-parse", "refs/remotes/origin/"+base); got != commit {
		t.Errorf("the sandbox clone's origin/%s = %q, want %q", base, got, commit)
	}
}

func TestACloneOfTheScratchCloneStartsOnTheBaseWithItsRemoteRefWhenTheCheckoutIsOnAFeatureBranch(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	testutil.RunGit(t, remote.Checkout, "checkout", "--quiet", "-b", "feature")
	scratch := filepath.Join(t.TempDir(), "task", "src")
	_, head, err := Prepare(remote.Checkout, scratch, "acme/api", "")
	if err != nil {
		t.Fatal(err)
	}
	sandbox := cloneAsSbxDoes(t, scratch)
	expectSandboxStartsFrom(t, sandbox, "main", head)
	if got := originRefs(t, sandbox); got != "origin\norigin/main" {
		t.Errorf("the sandbox clone's origin refs = %q, want only origin/HEAD and origin/main", got)
	}
}

func TestACloneOfTheScratchCloneHasGitHubsBaseNotTheCheckoutsStaleOne(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	want := advanceRemoteBranch(t, remote, "main")
	scratch := filepath.Join(t.TempDir(), "task", "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", ""); err != nil {
		t.Fatal(err)
	}
	expectSandboxStartsFrom(t, cloneAsSbxDoes(t, scratch), "main", want)
}

func TestACloneOfTheScratchCloneStartsOnAnExplicitBaseAndAlsoHasTheDefaultBranch(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	release := advanceRemoteBranch(t, remote, "release")
	trunk := advanceRemoteBranch(t, remote, "trunk")
	testutil.RunGit(t, remote.Bare, "symbolic-ref", "HEAD", "refs/heads/trunk")
	testutil.RunGit(t, remote.Checkout, "checkout", "--quiet", "-b", "feature")
	scratch := filepath.Join(t.TempDir(), "task", "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "release"); err != nil {
		t.Fatal(err)
	}
	sandbox := cloneAsSbxDoes(t, scratch)
	expectSandboxStartsFrom(t, sandbox, "release", release)
	if got := testutil.RunGit(t, sandbox, "rev-parse", "refs/remotes/origin/trunk"); got != trunk {
		t.Errorf("the sandbox clone's origin/trunk = %q, want %q", got, trunk)
	}
	if got := originRefs(t, sandbox); got != "origin\norigin/release\norigin/trunk" {
		t.Errorf("the sandbox clone's origin refs = %q, want only origin/HEAD, origin/release and origin/trunk", got)
	}
}

func TestAReusedScratchCloneMovesItsBranchesToWhatGitHubHasNow(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	scratch := filepath.Join(t.TempDir(), "task", "src")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", ""); err != nil {
		t.Fatal(err)
	}
	testutil.RunGit(t, scratch, "branch", "left-over")
	want := advanceRemoteBranch(t, remote, "main")
	if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", ""); err != nil {
		t.Fatal(err)
	}
	sandbox := cloneAsSbxDoes(t, scratch)
	expectSandboxStartsFrom(t, sandbox, "main", want)
	if got := originRefs(t, sandbox); got != "origin\norigin/main" {
		t.Errorf("the sandbox clone's origin refs = %q, want only origin/HEAD and origin/main", got)
	}
}

func TestPrepareStartsOnABaseNestedUnderTheCheckoutsBranchName(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	want := advanceRemoteBranch(t, remote, "release/1.0")
	testutil.RunGit(t, remote.Checkout, "checkout", "--quiet", "-b", "release")
	scratch := filepath.Join(t.TempDir(), "task", "src")
	for attempt := 1; attempt <= 2; attempt++ {
		if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "release/1.0"); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	expectSandboxStartsFrom(t, cloneAsSbxDoes(t, scratch), "release/1.0", want)
}

func TestPrepareStartsOnABaseThatIsAPrefixOfTheCheckoutsBranchName(t *testing.T) {
	remote := testutil.GitHubRemote(t)
	want := advanceRemoteBranch(t, remote, "release")
	testutil.RunGit(t, remote.Checkout, "checkout", "--quiet", "-b", "release/1.0")
	scratch := filepath.Join(t.TempDir(), "task", "src")
	for attempt := 1; attempt <= 2; attempt++ {
		if _, _, err := Prepare(remote.Checkout, scratch, "acme/api", "release"); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	expectSandboxStartsFrom(t, cloneAsSbxDoes(t, scratch), "release", want)
}
