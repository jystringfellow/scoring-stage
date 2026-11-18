package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func sandboxWorkspace(t *testing.T) (*harness, string) {
	t.Helper()
	h, _ := putReady(t)
	h.sbx.RunExecs()
	return h, filepath.Join(h.task("fix"), "src")
}

func TestDownWarnsAboutFilesInTheRepositoryThatWereNeverCommitted(t *testing.T) {
	h, workspace := sandboxWorkspace(t)
	testutil.WriteFile(t, filepath.Join(workspace, "results", "report.json"), "{}\n")
	testutil.WriteFile(t, filepath.Join(workspace, "notes.txt"), "found it\n")
	testutil.WriteFile(t, filepath.Join(workspace, "README.md"), "changed\n")

	h.expect(0, "down", "fix")
	h.expectStderr("WARNING: stage-fix is being removed with 3 uncommitted or untracked files in the repository")
	h.expectStderr("stage ship")
	if !strings.Contains(h.stdout.String(), "stage-fix removed") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	if _, err := os.Stat(h.task("fix")); !os.IsNotExist(err) {
		t.Error("task dir remains")
	}
}

func TestDownWarnsAboutACommitThatWasNeverShipped(t *testing.T) {
	h, workspace := sandboxWorkspace(t)
	boxCommit(t, workspace, "change.txt", "Fix the login redirect")
	sha := testutil.RunGit(t, workspace, "rev-parse", "HEAD")
	started := testutil.RunGit(t, workspace, "rev-parse", "HEAD~1")
	original := startingCommit
	startingCommit = func(*Task) (string, error) { return started, nil }
	t.Cleanup(func() { startingCommit = original })

	h.expect(0, "down", "fix")
	h.expectStderr("WARNING: stage-fix is being removed with commit " + sha[:8] + " that was never shipped")
}

func TestDownSaysNothingAboutACommitThatWasShipped(t *testing.T) {
	h, workspace := sandboxWorkspace(t)
	boxCommit(t, workspace, "change.txt", "Fix the login redirect")
	sha := testutil.RunGit(t, workspace, "rev-parse", "HEAD")
	started := testutil.RunGit(t, workspace, "rev-parse", "HEAD~1")
	original := startingCommit
	startingCommit = func(*Task) (string, error) { return started, nil }
	t.Cleanup(func() { startingCommit = original })
	task, err := loadTask(h.home, "fix")
	if err != nil {
		t.Fatal(err)
	}
	task.Ships = []Ship{{SHA: sha, Branch: "agent/fix/" + sha[:8], GitHubRepo: "acme/api", At: "2026-01-01T00:00:00Z"}}
	if err := writeTask(*task, h.task("fix")); err != nil {
		t.Fatal(err)
	}

	h.expect(0, "down", "fix")
	if strings.Contains(h.stderr.String(), "WARNING") {
		t.Errorf("warned about shipped work: %s", h.stderr.String())
	}
}

func TestDownSaysNothingWhenTheRepositoryIsCleanAndWhereItStarted(t *testing.T) {
	h, _ := sandboxWorkspace(t)
	h.expect(0, "down", "fix")
	if strings.Contains(h.stderr.String(), "WARNING") {
		t.Errorf("warned about a clean repository: %s", h.stderr.String())
	}
}

func TestDownIgnoresAnAnswerFromTheSandboxThatIsNotACountAndACommit(t *testing.T) {
	h, workspace := sandboxWorkspace(t)
	testutil.WriteFile(t, filepath.Join(workspace, "notes.txt"), "found it\n")
	original := unkeptScript
	unkeptScript = `printf '\033[2J9999 files\nrm -rf everything\n'`
	t.Cleanup(func() { unkeptScript = original })

	h.expect(0, "down", "fix")
	if strings.Contains(h.stderr.String(), "\033") || strings.Contains(h.stderr.String(), "9999") {
		t.Errorf("printed what the sandbox sent: %q", h.stderr.String())
	}
	h.expectStderr("could not check stage-fix for work that was not shipped")
}
