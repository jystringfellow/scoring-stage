package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const nearlyFullDf = `Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/vda1         20511312 19485747   1025565      95% /
/dev/vdb          41022624  4102262  36920362      10% /work
`

func fakeDf(t *testing.T, output string) {
	t.Helper()
	bin := t.TempDir()
	printed := testutil.WriteFile(t, filepath.Join(bin, "df.out"), output)
	path := testutil.WriteFile(t, filepath.Join(bin, "df"), "#!/bin/sh\ncat '"+printed+"'\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestJobWaitReportsTheSandboxsDiskUseAndWarnsWhenItIsNearlyFull(t *testing.T) {
	h, _ := jobsReady(t)
	fakeDf(t, nearlyFullDf)
	_, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 })

	h.expect(124, "job", "wait", "fix", "1", "--timeout", "200ms", "--json")
	h.expectStderr("stage: disk in stage-fix: / 95% used (1.0 GiB free), /work 10% used (35.2 GiB free)")
	h.expectStderr("WARNING: stage-fix has 1.0 GiB left at /; a full disk fails the job")
	if strings.Contains(h.stderr.String(), "left at /work") {
		t.Errorf("warned about a disk with room: %s", h.stderr.String())
	}
	var job jobStatus
	if err := json.Unmarshal(h.stdout.Bytes(), &job); err != nil || job.State != "running" {
		t.Fatalf("stdout is not the job alone: %v %q", err, h.stdout.String())
	}
	if want := "exec -u agent stage-fix df -Pk / " + filepath.Join(h.task("fix"), "src"); !h.anyCall(want) {
		t.Errorf("no call %q in %v", want, h.sbx.Calls())
	}
}

func TestListShowsDiskUseForRunningSandboxesItHasATaskFor(t *testing.T) {
	h, _ := putReady(t)
	h.sbx.RunExecs()
	fakeDf(t, nearlyFullDf)
	h.sbx.SetSandboxes("stage-fix\nstage-unknown\nclaude-other\n")
	h.expect(0, "list")
	want := "SANDBOX   STATUS\n" +
		"stage-fix   running   disk: / 95% used (1.0 GiB free), /work 10% used (35.2 GiB free)\n" +
		"stage-unknown   running\n"
	if got := h.stdout.String(); got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
	h.expectStderr("WARNING: stage-fix has 1.0 GiB left at /")
}

func TestDiskUseShowsOnlyWhatReadsAsDfOutput(t *testing.T) {
	h, _ := putReady(t)
	h.sbx.RunExecs()
	fakeDf(t, "Filesystem 1024-blocks Used Available Capacity Mounted on\n\033[2Jtmpfs 10 9 1 999% /\nrm -rf /\n/dev/vda1 100 50 50 50% /mnt/\033[31mred\n")
	h.expect(0, "list")
	got := h.stdout.String() + h.stderr.String()
	if strings.Contains(got, "\033") || strings.Contains(got, "999") || strings.Contains(got, "rm -rf") {
		t.Errorf("printed what the sandbox sent: %q", got)
	}
	if !strings.Contains(h.stdout.String(), `disk: /mnt/\x1b[31mred 50% used`) {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestAFinishedJobsWaitStillExitsWithItsStatusWhenDiskUseCannotBeRead(t *testing.T) {
	h, _ := jobsReady(t)
	fakeDf(t, "")
	gate, args := gated(t, h)
	h.expect(0, args...)
	testutil.WriteFile(t, gate, "")
	h.expect(3, "job", "wait", "fix", "1", "--timeout", "5s")
	if strings.Contains(h.stderr.String(), "disk") {
		t.Errorf("stderr %q", h.stderr.String())
	}
}
