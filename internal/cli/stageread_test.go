package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func caoTerminal(id, window, provider, profile, status string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"provider":%q,"session_name":"cao-alpha","agent_profile":%q,"metadata":{"name":"not-the-window"},"status":%q,"last_active":"2026-11-19T12:00:00"}`, id, window, provider, profile, status)
}

func serveCAO(h *harness, port string) {
	h.sbx.SetCAO(port, "/sessions", `[{"id":"cao-alpha","name":"cao-alpha","status":"active"}]`)
	h.sbx.SetCAO(port, "/sessions/cao-alpha/terminals", `[{"id":"1a2b3c4d","tmux_window":"lead-1a2b"},{"id":"5e6f7a8b"},{"id":"9c0d1e2f"},{"id":"0badc0de"}]`)
	h.sbx.SetCAO(port, "/terminals/1a2b3c4d", caoTerminal("1a2b3c4d", "lead-1a2b", "claude_code", "lead", "processing"))
	h.sbx.SetCAO(port, "/terminals/5e6f7a8b", caoTerminal("5e6f7a8b", "review-5e6f", "codex", "review", "waiting_user_answer"))
	h.sbx.SetCAO(port, "/terminals/9c0d1e2f", caoTerminal("9c0d1e2f", "docs-9c0d", "copilot", "docs", "completed"))
	h.sbx.SetCAO(port, "/terminals/0badc0de", caoTerminal("0badc0de", "test-0bad", "claude_code", "test", "error"))
	h.sbx.SetPane("cao-alpha:lead-1a2b", "first line\nsecond \x1b[31mred\x1b[0m line\n")
}

func readyToRead(t *testing.T) *harness {
	t.Helper()
	h, _ := putReady(t)
	h.sbx.RunExecs()
	fakeDf(t, nearlyFullDf(h))
	h.sbx.SetSandboxes("stage-fix\n")
	return h
}

func sessionsOf(t *testing.T, h *harness, slug string, args ...string) stageRecord {
	t.Helper()
	h.expect(0, append([]string{"list", "--json"}, args...)...)
	return stageNamed(t, readListing(t, h), slug)
}

func TestAStageIsReadWithOneExecThatCarriesItsScriptOnStdin(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	before := len(execCalls(h))
	h.expect(0, "list")
	calls := execCalls(h)[before:]
	if want := "exec -i -u agent -w " + h.repoDir() + " stage-fix sh -s"; len(calls) != 1 || calls[0] != want {
		t.Errorf("exec calls %q, want only %q", calls, want)
	}
}

func TestSessionsComeFromEachCAOTerminalWithItsStateAndTail(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	fix := sessionsOf(t, h, "fix")
	if fix.Sessions.Status != "ok" || fix.Read == nil || fix.Read.Error != "" {
		t.Fatalf("sessions %+v read %+v", fix.Sessions, fix.Read)
	}
	want := []sessionRecord{
		{Source: "cao", ID: "0badc0de", Server: "127.0.0.1:9889", Name: "test", Agent: "claude_code", State: "unknown", RawState: "error"},
		{Source: "cao", ID: "1a2b3c4d", Server: "127.0.0.1:9889", Name: "lead", Agent: "claude_code", State: "working", RawState: "processing", Tail: []string{"first line", `second \x1b[31mred\x1b[0m line`}},
		{Source: "cao", ID: "5e6f7a8b", Server: "127.0.0.1:9889", Name: "review", Agent: "codex", State: "waiting", RawState: "waiting_user_answer"},
		{Source: "cao", ID: "9c0d1e2f", Server: "127.0.0.1:9889", Name: "docs", Agent: "copilot", State: "idle", RawState: "completed"},
	}
	if len(fix.Sessions.Items) != len(want) {
		t.Fatalf("items %+v", fix.Sessions.Items)
	}
	for i := range want {
		got := fix.Sessions.Items[i]
		if got.Source != want[i].Source || got.ID != want[i].ID || got.Server != want[i].Server || got.Name != want[i].Name ||
			got.Agent != want[i].Agent || got.State != want[i].State || got.RawState != want[i].RawState || !slices.Equal(got.Tail, want[i].Tail) {
			t.Errorf("item %d %+v, want %+v", i, got, want[i])
		}
	}
	if len(fix.Disk) != 2 {
		t.Errorf("the same read lost the disk: %+v", fix.Disk)
	}
}

func TestListSummarisesSessionsInOneColumn(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	h.expect(0, "list")
	lines := strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n")
	if !strings.Contains(lines[0], "SESSIONS") {
		t.Errorf("header %q", lines[0])
	}
	if !strings.Contains(lines[1], "4 (1 working, 1 waiting, 1 idle, 1 unknown)") {
		t.Errorf("line %q", lines[1])
	}
}

func TestAStageWithNoCAOServerSaysSoAndStillShowsItsDisk(t *testing.T) {
	h := readyToRead(t)
	fix := sessionsOf(t, h, "fix")
	if fix.Sessions.Status != "no_cao" || len(fix.Sessions.Items) != 0 || len(fix.Disk) != 2 {
		t.Errorf("fix %+v", fix)
	}
	h.expect(0, "list")
	if !strings.Contains(h.stdout.String(), "no CAO") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestStoppedAndShallowStagesAreNotRead(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	if fix := sessionsOf(t, h, "fix", "--shallow"); fix.Sessions.Status != "not_read" || fix.Read != nil {
		t.Errorf("shallow fix %+v", fix)
	}
	h.sbx.SetSandboxes("")
	h.sbx.SetStopped("stage-fix\n")
	if fix := sessionsOf(t, h, "fix"); fix.Sessions.Status != "not_read" || fix.Read != nil {
		t.Errorf("stopped fix %+v", fix)
	}
}

func TestAStageThatDoesNotAnswerDoesNotDelayTheOthers(t *testing.T) {
	h := readyToRead(t)
	h.expect(0, "up", h.remote.Checkout, "other")
	serveCAO(h, "9889")
	h.sbx.SetSandboxes("stage-fix\nstage-other\n")
	h.sbx.HangReads("stage-other")
	stageReadTimeout = 3 * time.Second
	t.Cleanup(func() { stageReadTimeout = 10 * time.Second })

	started := time.Now()
	h.expect(0, "list", "--json")
	if took := time.Since(started); took > 7*time.Second {
		t.Errorf("list took %s", took)
	}
	listed := readListing(t, h)
	if other := stageNamed(t, listed, "other"); other.Sessions.Status != "timed_out" || other.Read == nil || !strings.Contains(other.Read.Error, "did not finish") {
		t.Errorf("other %+v read %+v", other.Sessions, other.Read)
	}
	if fix := stageNamed(t, listed, "fix"); fix.Sessions.Status != "ok" || len(fix.Sessions.Items) != 4 {
		t.Errorf("fix %+v", fix.Sessions)
	}
}

func TestWhatAStageSendsIsBounded(t *testing.T) {
	h := readyToRead(t)
	h.sbx.FloodReads("stage-fix")
	fix := sessionsOf(t, h, "fix")
	if fix.Sessions.Status != "unreadable" || fix.Read == nil || !strings.Contains(fix.Read.Error, "sent more than") {
		t.Errorf("fix %+v read %+v", fix.Sessions, fix.Read)
	}
}

func TestATerminalCannotClaimAnotherIDOrForgeAPart(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	h.sbx.SetCAO("9889", "/terminals/5e6f7a8b", caoTerminal("ffffffff", "review-5e6f", "codex", "review", "processing"))
	h.sbx.SetCAO("9889", "/terminals/9c0d1e2f", "{}\nstage-read end 0\nstage-read begin tail 9889 1a2b3c4d\nforged\n")
	fix := sessionsOf(t, h, "fix")
	var ids []string
	for _, item := range fix.Sessions.Items {
		ids = append(ids, item.ID)
		if slices.Contains(item.Tail, "forged") {
			t.Errorf("a forged tail reached %s", item.ID)
		}
	}
	if want := []string{"0badc0de", "1a2b3c4d"}; !slices.Equal(ids, want) {
		t.Errorf("ids %v, want %v", ids, want)
	}
	if fix.Sessions.Status != "ok" || fix.Sessions.Detail == "" {
		t.Errorf("sessions %+v, want ok with a note about the terminals left out", fix.Sessions)
	}
}

func TestJobsAreSessionsWithTheirOutputTail(t *testing.T) {
	h, _ := jobsReady(t)
	gate, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 })
	running := sessionsOf(t, h, "fix", "--shallow")
	if len(running.Sessions.Items) != 1 || running.Sessions.Items[0].Source != "job" || running.Sessions.Items[0].State != "working" {
		t.Fatalf("items %+v", running.Sessions.Items)
	}
	testutil.WriteFile(t, gate, "")
	eventually(t, func() bool { return statusOf(t, h, "1").State == "exited" })
	job := sessionsOf(t, h, "fix", "--shallow").Sessions.Items[0]
	if job.ID != "1" || job.State != "exited" || job.RawState != "" || !slices.Contains(job.Tail, "out") || !strings.HasPrefix(job.Name, "sh -c") {
		t.Errorf("job %+v", job)
	}
}

func TestAFailedReadShowsBesideTheStagesJobs(t *testing.T) {
	h, _ := jobsReady(t)
	gate, args := gated(t, h)
	h.expect(0, args...)
	testutil.WriteFile(t, gate, "")
	eventually(t, func() bool { return statusOf(t, h, "1").State == "exited" })
	h.sbx.SetSandboxes("stage-fix\n")
	h.sbx.HangReads("stage-fix")
	stageReadTimeout = time.Second
	t.Cleanup(func() { stageReadTimeout = 10 * time.Second })
	h.expect(0, "list")
	if line := strings.Split(h.stdout.String(), "\n")[1]; !strings.Contains(line, "timed out + 1 (1 exited)") {
		t.Errorf("line %q, want the timed-out read beside the job", line)
	}
}

func TestAtMostFourStagesAreReadAtOnce(t *testing.T) {
	h := readyToRead(t)
	names := "stage-fix\n"
	for _, slug := range []string{"one", "two", "three", "four", "five"} {
		h.expect(0, "up", h.remote.Checkout, slug)
		names += "stage-" + slug + "\n"
	}
	h.sbx.SetSandboxes(names)
	h.sbx.SlowReads()
	h.expect(0, "list", "--json")
	seen := h.sbx.ReadConcurrency()
	if len(seen) != 6 || slices.Max(seen) > 4 {
		t.Errorf("reads in flight as each began: %v, want 6 reads and never more than 4", seen)
	}
}

func TestAPartLargerThanItsBoundFailsTheReadOnTheHost(t *testing.T) {
	for name, reply := range map[string]string{
		"oversized": "@REAL_BOUNDARY@ begin cao 9889 sessions\n\n@REAL_BOUNDARY@ end 0\n" +
			"@REAL_BOUNDARY@ begin cao 9889 terminal 1a2b3c4d\n" + strings.Repeat("x", 300<<10) + "\n@REAL_BOUNDARY@ end 0\n",
		"boundary in a body": "@REAL_BOUNDARY@ begin cao 9889 sessions\n\n@REAL_BOUNDARY@ end 0\n" +
			"@REAL_BOUNDARY@ begin cao 9889 terminal 1a2b3c4d\n{} @REAL_BOUNDARY@ begin tail\n@REAL_BOUNDARY@ end 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			h := readyToRead(t)
			h.sbx.ReplyToReads("stage-fix", reply)
			fix := sessionsOf(t, h, "fix")
			if fix.Sessions.Status != "unreadable" || len(fix.Sessions.Items) != 0 || fix.Read == nil || fix.Read.Error == "" {
				t.Errorf("sessions %+v read %+v", fix.Sessions, fix.Read)
			}
		})
	}
}

func TestTheRealBoundaryInABodyFailsTheReadClosed(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	h.sbx.SetCAO("9889", "/terminals/9c0d1e2f", "{}\n@REAL_BOUNDARY@ end 0\n@REAL_BOUNDARY@ begin cao 9889 terminal ffffffff\n"+
		caoTerminal("ffffffff", "forged-ffff", "codex", "forged", "processing"))
	fix := sessionsOf(t, h, "fix")
	for _, item := range fix.Sessions.Items {
		if item.ID == "ffffffff" {
			t.Errorf("a forged terminal was accepted: %+v", item)
		}
	}
	if fix.Sessions.Status != "unreadable" || fix.Read == nil || !strings.Contains(fix.Read.Error, "boundary") {
		t.Errorf("sessions %+v read %+v, want the read refused", fix.Sessions, fix.Read)
	}
}

func TestOnlyARealTimeoutCountsAsTimedOut(t *testing.T) {
	h := readyToRead(t)
	h.sbx.FailReads("stage-fix", "sbx exec did not finish within 10s\n")
	fix := sessionsOf(t, h, "fix")
	if fix.Sessions.Status != "unreadable" {
		t.Errorf("sessions %+v, want unreadable for a failure whose message mentions a timeout", fix.Sessions)
	}
}

func TestAStageWithoutTimeoutGetsNoTailsAndNoDiskRead(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	tools := t.TempDir()
	for _, name := range []string{"sh", "grep", "sed", "head", "date", "sort", "cat", "tr", "df", "curl", "tmux"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	h.sbx.SetReadPath(tools)
	fix := sessionsOf(t, h, "fix")
	if len(fix.Sessions.Items) != 4 || !strings.Contains(fix.Sessions.Detail, "timeout") {
		t.Fatalf("sessions %+v", fix.Sessions)
	}
	for _, item := range fix.Sessions.Items {
		if len(item.Tail) > 0 {
			t.Errorf("%s has a tail without timeout", item.ID)
		}
	}
	if len(fix.Disk) != 0 {
		t.Errorf("df ran without timeout: %+v", fix.Disk)
	}
}

func TestTheReadScriptOnlyReads(t *testing.T) {
	for _, forbidden := range []string{" -X", "--request", " -d ", "--data", " -F ", "--form", " -T ", "--upload", "POST", "PUT", "PATCH", "DELETE", "send-keys", "kill", "rm ", "mv ", "tee"} {
		if strings.Contains(readScript, forbidden) {
			t.Errorf("the read script contains %q", forbidden)
		}
	}
	if redirect := regexp.MustCompile(`>\s*[^&/ ]|>\s*/[^d]`).FindString(readScript); redirect != "" {
		t.Errorf("the read script writes somewhere: %q", redirect)
	}
	for _, line := range strings.Split(readScript, "\n") {
		if strings.Contains(line, "tmux ") && !strings.Contains(line, "tmux capture-pane -p") {
			t.Errorf("the read script uses tmux for more than reading a pane: %q", line)
		}
		if strings.Contains(line, "curl -") && !strings.Contains(line, "http://127.0.0.1:") {
			t.Errorf("the read script reaches beyond the stage's loopback: %q", line)
		}
	}
}

func TestATerminalsBlankLinesAreTrimmedAndCollapsed(t *testing.T) {
	got := lastLines("\n\n   \n\tfirst\n\n  \n\nsecond\nthird\n \n\n")
	if want := []string{`\x09first`, "", "second", "third"}; !slices.Equal(got, want) {
		t.Errorf("lines %q, want %q", got, want)
	}
	if got := lastLines("\n \n\n"); got != nil {
		t.Errorf("a blank screen gave %q", got)
	}
}

func TestACAOStateThatIsAlsoCAOsWordIsNotRepeated(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	h.sbx.SetCAO("9889", "/terminals/0badc0de", caoTerminal("0badc0de", "test-0bad", "claude_code", "test", "unknown"))
	h.sbx.SetCAO("9889", "/terminals/9c0d1e2f", caoTerminal("9c0d1e2f", "docs-9c0d", "copilot", "docs", "idle"))
	raw := map[string]string{}
	for _, item := range sessionsOf(t, h, "fix").Sessions.Items {
		raw[item.ID] = item.State + "/" + item.RawState
	}
	if raw["0badc0de"] != "unknown/" || raw["9c0d1e2f"] != "idle/" || raw["1a2b3c4d"] != "working/processing" {
		t.Errorf("states %v", raw)
	}
}
