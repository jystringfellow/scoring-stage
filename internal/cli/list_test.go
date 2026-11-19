package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type listedStages struct {
	Format string        `json:"format"`
	ReadAt string        `json:"read_at"`
	Stages []stageRecord `json:"stages"`
}

func readListing(t *testing.T, h *harness) listedStages {
	t.Helper()
	var listed listedStages
	if err := json.Unmarshal(h.stdout.Bytes(), &listed); err != nil {
		t.Fatalf("stdout is not a listing: %v %q", err, h.stdout.String())
	}
	return listed
}

func stageNamed(t *testing.T, listed listedStages, slug string) stageRecord {
	t.Helper()
	for _, stage := range listed.Stages {
		if stage.Slug == slug {
			return stage
		}
	}
	t.Fatalf("no stage %s in %+v", slug, listed.Stages)
	return stageRecord{}
}

func addShip(t *testing.T, h *harness, slug string, ship Ship) {
	t.Helper()
	task, err := loadTask(h.home, slug)
	if err != nil {
		t.Fatal(err)
	}
	task.Ships = append(task.Ships, ship)
	content, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(h.task(slug), "task.json"), string(content))
}

func keepTakeDirs(t *testing.T, h *harness, slug string, names ...string) {
	t.Helper()
	for _, name := range names {
		testutil.WriteFile(t, filepath.Join(h.home, "takes", slug, name, "stage.json"), "{}")
	}
}

func execCalls(h *harness) []string {
	var calls []string
	for _, call := range h.sbx.Calls() {
		if strings.HasPrefix(call, "exec") {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestListJSONJoinsTasksWithSandboxesAndSaysWhichAreStoppedGoneOrWithoutATask(t *testing.T) {
	h, _ := putReady(t)
	h.expect(0, "up", h.remote.Checkout, "docs")
	h.expect(0, "up", h.remote.Checkout, "old")
	addShip(t, h, "fix", Ship{SHA: "abc1234", Branch: "agent/fix/abc1234", GitHubRepo: "acme/api", At: "2026-11-19T12:00:00Z", PullRequest: "https://github.com/acme/api/pull/7"})
	keepTakeDirs(t, h, "fix", "20261119T100000Z", "20261119T120000Z", ".partial-123")
	h.sbx.SetSandboxes("stage-fix\nstage-orphan\nclaude-other\n")
	h.sbx.SetStopped("stage-docs\n")

	h.expect(0, "list", "--json", "--shallow")
	listed := readListing(t, h)
	if listed.Format != "stage-list/1" {
		t.Errorf("format %q", listed.Format)
	}
	if _, err := time.Parse(time.RFC3339, listed.ReadAt); err != nil {
		t.Errorf("read_at %q: %v", listed.ReadAt, err)
	}
	var slugs, states []string
	for _, stage := range listed.Stages {
		slugs, states = append(slugs, stage.Slug), append(states, stage.State)
	}
	if want := []string{"docs", "fix", "old", "orphan"}; !slices.Equal(slugs, want) {
		t.Errorf("slugs %v, want %v", slugs, want)
	}
	if want := []string{"stopped", "running", "gone", "no_task"}; !slices.Equal(states, want) {
		t.Errorf("states %v, want %v", states, want)
	}

	fix := stageNamed(t, listed, "fix")
	if fix.Sandbox != "stage-fix" || fix.Repository != "acme/api" || fix.BaseBranch != "main" {
		t.Errorf("fix %+v", fix)
	}
	if fix.CreatedAt != "2026-11-19T10:00:00Z" || fix.LastUsedAt != "2026-11-19T11:30:00.25Z" {
		t.Errorf("fix times %q %q", fix.CreatedAt, fix.LastUsedAt)
	}
	if len(fix.Ships) != 1 || fix.Ships[0].PullRequest != "https://github.com/acme/api/pull/7" {
		t.Errorf("fix ships %+v", fix.Ships)
	}
	if fix.Takes.Count != 2 || fix.Takes.Latest != "20261119T120000Z" {
		t.Errorf("fix takes %+v", fix.Takes)
	}
	if old := stageNamed(t, listed, "old"); old.CreatedAt != "" || old.Repository != "acme/api" {
		t.Errorf("old %+v", old)
	}
	if orphan := stageNamed(t, listed, "orphan"); orphan.Sandbox != "stage-orphan" || orphan.Repository != "" || orphan.Ships == nil {
		t.Errorf("orphan %+v", orphan)
	}
}

func TestListCountsAStagesRunningJobs(t *testing.T) {
	h, _ := jobsReady(t)
	gate, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 })

	h.expect(0, "list", "--json", "--shallow")
	if fix := stageNamed(t, readListing(t, h), "fix"); fix.RunningJobs != 1 {
		t.Errorf("running jobs %d", fix.RunningJobs)
	}
	testutil.WriteFile(t, gate, "")
}

func TestListReadsOnlyRunningSandboxesItHasATaskFor(t *testing.T) {
	h, _ := putReady(t)
	h.expect(0, "up", h.remote.Checkout, "docs")
	h.sbx.RunExecs()
	fakeDf(t, nearlyFullDf(h))
	h.sbx.SetSandboxes("stage-fix\nstage-orphan\n")
	h.sbx.SetStopped("stage-docs\n")
	before := len(execCalls(h))

	h.expect(0, "list", "--json")
	calls := execCalls(h)[before:]
	if len(calls) != 1 || !strings.Contains(calls[0], " stage-fix ") {
		t.Errorf("exec calls %v, want one into stage-fix", calls)
	}
	listed := readListing(t, h)
	if fix := stageNamed(t, listed, "fix"); len(fix.Disk) != 2 || fix.Disk[0].Mount != "/" || fix.Disk[0].UsedPercent != 95 {
		t.Errorf("fix disk %+v", fix.Disk)
	}
	if docs := stageNamed(t, listed, "docs"); docs.Disk != nil {
		t.Errorf("a stopped stage has disk %+v", docs.Disk)
	}
}

func TestListShallowRunsNothingInsideAnySandbox(t *testing.T) {
	h, _ := putReady(t)
	h.sbx.RunExecs()
	fakeDf(t, nearlyFullDf(h))
	before := len(execCalls(h))
	for _, args := range [][]string{{"list", "--shallow"}, {"list", "--json", "--shallow"}} {
		h.expect(0, args...)
	}
	if calls := execCalls(h)[before:]; len(calls) != 0 {
		t.Errorf("shallow list ran %v", calls)
	}
	if strings.Contains(h.stderr.String(), "WARNING") {
		t.Errorf("shallow list warned about a disk it did not read: %q", h.stderr.String())
	}
}

func TestListPrintsOneLinePerStage(t *testing.T) {
	h, _ := putReady(t)
	h.expect(0, "up", h.remote.Checkout, "docs")
	h.expect(0, "up", h.remote.Checkout, "old")
	addShip(t, h, "fix", Ship{SHA: "abc1234", Branch: "agent/fix/abc1234", GitHubRepo: "acme/api", At: "2026-11-19T12:00:00Z"})
	h.sbx.RunExecs()
	fakeDf(t, nearlyFullDf(h))
	now := time.Now().UTC()
	h.sbx.SetListing(fmt.Sprintf(`[{"name":"stage-fix","status":"running","created_at":%q},
		{"name":"stage-docs","status":"stopped","created_at":%q},
		{"name":"stage-orphan","status":"running","created_at":%q},
		{"name":"claude-other","status":"running"}]`,
		now.Add(-2*time.Hour-5*time.Minute).Format(time.RFC3339), now.Add(-74*time.Hour).Format(time.RFC3339), now.Add(-90*time.Second).Format(time.RFC3339)))

	h.expect(0, "list")
	lines := strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n")
	want := [][]string{
		{"SLUG", "STATE", "REPOSITORY", "AGE", "DISK", "SESSIONS", "SHIPS"},
		{"docs", "stopped", "acme/api@main", "3d", "-", "-", "0"},
		{"fix", "running", "acme/api@main", "2h", "95%", "no", "CAO", "1"},
		{"old", "gone", "acme/api@main", "-", "-", "-", "0"},
		{"orphan", "no", "task", "-", "1m", "-", "-", "0"},
	}
	if len(lines) != len(want) {
		t.Fatalf("stdout %q", h.stdout.String())
	}
	for i, line := range lines {
		if got := strings.Fields(line); !slices.Equal(got, want[i]) {
			t.Errorf("line %d %q, want %v", i, line, want[i])
		}
	}
	h.expectStderr("WARNING: stage-fix has 1.0 GiB left at /")
}

func TestListEscapesWhatSbxReports(t *testing.T) {
	h := setup(t)
	h.sbx.SetListing("[{\"name\":\"stage-x\\u001b[2J\",\"status\":\"running\\u001b]0;owned\\u0007\",\"created_at\":\"\\u001b[31m\"}]")
	for _, args := range [][]string{{"list"}, {"list", "--json"}} {
		h.expect(0, args...)
		if strings.ContainsAny(h.stdout.String(), "\x1b\x07") {
			t.Errorf("stage %v printed control characters: %q", args, h.stdout.String())
		}
	}
}

func TestListRefusesAListingItCannotRead(t *testing.T) {
	h := setup(t)
	for _, listing := range []string{"not json", `{"name":"stage-fix"}`, `[{"status":"running"}]`} {
		h.sbx.SetListing(listing)
		h.expect(1, "list")
		h.expectStderr("cannot read sbx ls --json")
	}
}

func TestListShowsATaskItCannotReadWithoutReadingItsSandbox(t *testing.T) {
	h, _ := putReady(t)
	h.sbx.RunExecs()
	fakeDf(t, nearlyFullDf(h))
	if err := os.WriteFile(filepath.Join(h.task("fix"), "task.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(execCalls(h))
	h.expect(0, "list", "--json")
	fix := stageNamed(t, readListing(t, h), "fix")
	if fix.State != "running" || fix.TaskError == "" || fix.Repository != "" {
		t.Errorf("fix %+v", fix)
	}
	if calls := execCalls(h)[before:]; len(calls) != 0 {
		t.Errorf("read a sandbox whose task is unreadable: %v", calls)
	}
}
