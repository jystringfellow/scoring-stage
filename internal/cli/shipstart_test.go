package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func stoppedTaskWithACommit(t *testing.T) (*harness, string) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	box := filepath.Join(t.TempDir(), "box")
	testutil.RunGit(t, h.remote.Root, "clone", "-q", h.remote.Checkout, box)
	testutil.WriteFile(t, filepath.Join(box, "change.txt"), "agent work\n")
	testutil.RunGit(t, box, "add", ".")
	testutil.RunGit(t, box, "commit", "-q", "-m", "Fix the login redirect")
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_1", "url."+box+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_1", sbxRemoteURL)
	h.sbx.SetSandboxes("")
	h.sbx.SetStopped("stage-fix\n")
	h.sbx.RunOnStart("stage-fix", addsSbxRemote(h))
	return h, box
}

const sbxRemoteURL = "git://127.0.0.1:49207/src"

func addsSbxRemote(h *harness) string {
	scratch := filepath.Join(h.task("fix"), "src")
	return "git -C '" + scratch + "' remote add sandbox-stage-fix " + sbxRemoteURL + "\n" +
		"git -C '" + scratch + "' config --add remote.sandbox-stage-fix.fetch '+refs/heads/*:refs/sandboxes/stage-fix/*'\n"
}

func TestShipStartsAStoppedSandboxAndPushesItsCommit(t *testing.T) {
	h, box := stoppedTaskWithACommit(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	before := len(h.sbx.Calls())
	h.expect(0, "ship", "fix", "--yes")
	if calls := execCallsSince(h, before); len(calls) == 0 || calls[0] != "exec -u agent stage-fix true" {
		t.Errorf("exec calls %q, want the stopped sandbox started first", calls)
	}
	branch := "agent/fix/" + sha[:8]
	if got := testutil.RunGit(t, h.remote.Bare, "rev-parse", branch); got != sha {
		t.Fatalf("%s is %q, want %s", branch, got, sha)
	}
	if !strings.Contains(h.stdout.String(), "starting stage-fix") {
		t.Errorf("stdout does not say the sandbox was started:\n%s", h.stdout.String())
	}
}

func TestMcpShipStartsAStoppedSandboxToReviewItsCommit(t *testing.T) {
	h, box := stoppedTaskWithACommit(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	d := readyMcp(t, h)
	data := toolData(t, d.tool("ship", map[string]any{"slug": "fix"}))
	if data["sha"] != sha || data["pushed"] != false {
		t.Fatal(data)
	}
}

func TestShipLeavesARunningSandboxAlone(t *testing.T) {
	h, _ := shippedTask(t)
	before := len(h.sbx.Calls())
	h.expect(0, "ship", "fix", "--yes")
	if calls := execCallsSince(h, before); len(calls) != 0 {
		t.Errorf("exec calls %q on a sandbox whose remote was there", calls)
	}
}

func TestShipChecksTheGitConfigSbxWritesWhenItStartsTheSandbox(t *testing.T) {
	h, _ := stoppedTaskWithACommit(t)
	scratch := filepath.Join(h.task("fix"), "src")
	h.sbx.RunOnStart("stage-fix", addsSbxRemote(h)+
		"git -C '"+scratch+"' config remote.sandbox-stage-fix.uploadpack 'touch /tmp/x; git-upload-pack'\n")
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("changed after stage up created the sandbox")
	if out := testutil.RunGit(t, h.remote.Bare, "branch", "--list", "agent/*"); out != "" {
		t.Fatalf("pushed from a changed clone: %s", out)
	}
}

func TestShipRefusesAChangedGitConfigBeforeStartingTheSandbox(t *testing.T) {
	h, _ := stoppedTaskWithACommit(t)
	testutil.RunGit(t, filepath.Join(h.task("fix"), "src"), "config", "core.sshCommand", "touch /tmp/x")
	before := len(h.sbx.Calls())
	h.expect(1, "ship", "fix", "--yes")
	h.expectStderr("changed after stage up created the sandbox")
	if calls := execCallsSince(h, before); len(calls) != 0 {
		t.Errorf("exec calls %q after the config changed", calls)
	}
}

func TestShipSaysWhatToCheckWhenStartingTheSandboxGivesNoRemote(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expect(1, "ship", "fix")
	h.expectStderr("no sandbox-stage-fix remote")
	h.expectStderr("after starting stage-fix; check that sbx ls lists it")
}
