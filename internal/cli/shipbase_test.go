package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func (h *harness) sandboxOnNewerMain(slug string, upstream map[string]string) string {
	h.t.Helper()
	h.expect(0, "up", h.remote.Checkout, slug)
	h.commitToMain(upstream)
	box := filepath.Join(h.t.TempDir(), "box")
	testutil.RunGit(h.t, h.remote.Root, "clone", "-q", h.remote.Checkout, box)
	testutil.RunGit(h.t, filepath.Join(h.task(slug), "src"), "remote", "add", "sandbox-stage-"+slug, box)
	h.pinGitConfig(slug)
	return box
}

func (h *harness) expectPushed(branch, sha string) {
	h.t.Helper()
	if pushed := testutil.RunGit(h.t, h.remote.Bare, "rev-parse", "--verify", "-q", "refs/heads/"+branch); pushed != sha {
		h.t.Fatalf("%s on the remote is %q, want %s", branch, pushed, sha)
	}
}

func (h *harness) expectNotPushed(branch string) {
	h.t.Helper()
	if out := testutil.RunGit(h.t, h.remote.Bare, "branch", "--list", branch); out != "" {
		h.t.Fatalf("pushed %s", out)
	}
}

func TestShipComparesWithTheRemoteBranchSoWorkflowsMergedAfterUpAreNotTheSandboxsChange(t *testing.T) {
	for _, branch := range []string{"", "agent/fix/tests"} {
		h := setup(t)
		box := h.sandboxOnNewerMain("fix", map[string]string{".github/workflows/ci.yml": "on: push\n"})
		boxCommit(t, box, "scripts/check_test.sh", "Test the check script")
		sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
		args := []string{"ship", "fix", "--yes"}
		if branch != "" {
			args = append(args, "--branch", branch)
		} else {
			branch = "agent/fix/" + sha[:8]
		}
		h.expect(0, args...)
		h.expectPushed(branch, sha)
		h.expectStdout("Test the check script", "scripts/check_test.sh | 1 +", "compared with main at ")
		for _, unwanted := range []string{".github", "Change the repository"} {
			if strings.Contains(h.stdout.String(), unwanted) {
				t.Errorf("the summary shows %q, which main already has:\n%s", unwanted, h.stdout.String())
			}
		}
	}
}

func TestShipStillRefusesAProtectedChangeTheSandboxMadeOnTopOfANewerRemoteBranch(t *testing.T) {
	for _, path := range []string{".github/workflows/ci.yml", ".envrc"} {
		h := setup(t)
		box := h.sandboxOnNewerMain("fix", map[string]string{".github/workflows/ci.yml": "on: push\n"})
		boxCommit(t, box, path, "Touch "+path)
		sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
		h.expect(1, "ship", "fix", "--yes")
		h.expectStderr("refusing to ship changes to CI, workflow or sandbox config files: " + path + ".")
		h.expectNotPushed("agent/fix/" + sha[:8])
		h.expect(0, "ship", "fix", "--yes", "--allow-protected-paths")
		h.expectStderr("shipping changes to protected files: " + path)
		h.expectPushed("agent/fix/"+sha[:8], sha)
	}
}

func TestShipOntoAnExistingBranchComparesWithThatBranchsRemoteHead(t *testing.T) {
	h, box := shippedTask(t)
	boxCommit(t, box, ".github/workflows/ci.yml", "Change CI")
	h.expect(0, "ship", "fix", "--yes", "--branch", "agent/fix/work", "--allow-protected-paths")

	boxCommit(t, box, "scripts/check_test.sh", "Test the check script")
	tests := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(0, "ship", "fix", "--yes", "--branch", "agent/fix/work")
	h.expectPushed("agent/fix/work", tests)
	h.expectStdout("compared with agent/fix/work at ")
	if strings.Contains(h.stdout.String(), "Change CI") {
		t.Errorf("the summary shows a commit agent/fix/work already has:\n%s", h.stdout.String())
	}

	boxCommit(t, box, ".envrc", "Load the environment")
	h.expect(1, "ship", "fix", "--yes", "--branch", "agent/fix/work")
	h.expectStderr("refusing to ship changes to CI, workflow or sandbox config files: .envrc.")
	h.expectPushed("agent/fix/work", tests)
}

func TestShipFindsNothingToShipWhenTheRemoteBranchAlreadyHasTheSandboxsCommit(t *testing.T) {
	h := setup(t)
	box := h.sandboxOnNewerMain("fix", map[string]string{"notes.txt": "upstream\n"})
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("nothing to ship")
	h.expectNotPushed("agent/fix/" + sha[:8])
}

func TestShipComparesANewBranchWithTheBaseBranchTheSandboxStartedFrom(t *testing.T) {
	h := setup(t)
	testutil.RunGit(t, h.remote.Checkout, "checkout", "-q", "-b", "release")
	testutil.WriteFile(t, filepath.Join(h.remote.Checkout, ".github", "workflows", "release.yml"), "on: push\n")
	testutil.RunGit(t, h.remote.Checkout, "add", ".")
	testutil.RunGit(t, h.remote.Checkout, "commit", "-q", "-m", "Release workflow")
	testutil.RunGit(t, h.remote.Checkout, "push", "-q", h.remote.Bare, "HEAD:refs/heads/release")
	h.expect(0, "up", h.remote.Checkout, "fix", "--base", "release")
	box := h.sandboxCommit("fix")
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(0, "ship", "fix", "--yes")
	h.expectPushed("agent/fix/"+sha[:8], sha)
	h.expectStdout("compared with release at ")
}

func TestShipRefusesACommitWithNoHistoryInCommonWithTheRemoteBranch(t *testing.T) {
	h, box := shippedTask(t)
	testutil.RunGit(t, box, "checkout", "-q", "--orphan", "unrelated")
	boxCommit(t, box, "scripts/check_test.sh", "Start over")
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("has no history in common with main on acme/api")
	h.expectNotPushed("agent/fix/" + sha[:8])
}
