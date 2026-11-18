package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func makeDueForGitMaintenance(t *testing.T, repo string) {
	t.Helper()
	testutil.RunGit(t, repo, "config", "gc.autoDetach", "false")
	testutil.RunGit(t, repo, "config", "maintenance.autoDetach", "false")
	blobs := t.TempDir()
	const looseObjects, perCommand = 7500, 500
	for first := 0; first < looseObjects; first += perCommand {
		args := []string{"hash-object", "-w", "--"}
		for n := first; n < first+perCommand; n++ {
			args = append(args, testutil.WriteFile(t, filepath.Join(blobs, strconv.Itoa(n)), fmt.Sprintf("loose %d\n", n)))
		}
		testutil.RunGit(t, repo, args...)
	}
}

func TestASecondShipIsNotRefusedWhenTheFirstShipsFetchFindsTheScratchCloneDueForGitMaintenance(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	scratch := filepath.Join(h.task("fix"), "src")
	makeDueForGitMaintenance(t, scratch)
	box := h.sandboxCommit("fix")

	h.expect(0, "ship", "fix", "--yes")
	boxCommit(t, box, "more.txt", "Handle the expired session")
	h.expect(0, "ship", "fix", "--yes")

	second := testutil.RunGit(t, box, "rev-parse", "HEAD")
	if pushed := testutil.RunGit(t, h.remote.Bare, "rev-parse", "agent/fix/"+second[:8]); pushed != second {
		t.Errorf("pushed %s, want %s", pushed, second)
	}

	written := filepath.Join(scratch, ".git", "info", "refs")
	if _, err := os.Stat(written); err == nil {
		t.Fatalf("%s exists after stage's own fetches", written)
	}
	testutil.RunGit(t, scratch, "fetch", "--quiet", "origin")
	if _, err := os.Stat(written); err != nil {
		t.Fatalf("a fetch without stage's settings did not run maintenance here, so the ships above proved nothing: %v", err)
	}
}

func TestShipStillRefusesAScratchCloneWhoseInfoFilesChangedBetweenShips(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	scratch := filepath.Join(h.task("fix"), "src")
	box := h.sandboxCommit("fix")
	h.expect(0, "ship", "fix", "--yes")
	first := testutil.RunGit(t, box, "rev-parse", "HEAD")

	for name, content := range map[string]string{
		"refs":       first + "\trefs/heads/main\n",
		"attributes": "* filter=run\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := testutil.WriteFile(t, filepath.Join(scratch, ".git", "info", name), content)
			t.Cleanup(func() { os.Remove(path) })
			boxCommit(t, box, name+".txt", "Change "+name)
			h.expect(1, "ship", "fix", "--yes")
			h.expectStderr("the git config in " + scratch + " changed after stage up")
			unshipped := testutil.RunGit(t, box, "rev-parse", "HEAD")
			if out := testutil.RunGit(t, h.remote.Bare, "branch", "--list", "agent/fix/"+unshipped[:8]); out != "" {
				t.Fatalf("pushed from a changed clone: %s", out)
			}
		})
	}
}
