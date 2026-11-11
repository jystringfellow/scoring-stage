package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type recordedShip struct {
	SHA         string `json:"sha"`
	Branch      string `json:"branch"`
	GitHubRepo  string `json:"github_repo"`
	At          string `json:"at"`
	PullRequest string `json:"pull_request"`
}

type recordedTask struct {
	BaseBranch string         `json:"base_branch"`
	Ships      []recordedShip `json:"ships"`
}

func (h *harness) recorded(slug string) recordedTask {
	h.t.Helper()
	var task recordedTask
	if err := json.Unmarshal([]byte(testutil.ReadFile(h.t, filepath.Join(h.task(slug), "task.json"))), &task); err != nil {
		h.t.Fatal(err)
	}
	return task
}

func expectRecent(t *testing.T, stamp string) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatalf("time %q is not RFC3339: %v", stamp, err)
	}
	if !strings.HasSuffix(stamp, "Z") {
		t.Errorf("time %q is not UTC", stamp)
	}
	if age := time.Since(at); age < -time.Minute || age > time.Minute {
		t.Errorf("time %q is %v from now", stamp, age)
	}
}

func (h *harness) ghAnswering(output string, exitCode int) string {
	h.t.Helper()
	dir := h.t.TempDir()
	log := filepath.Join(dir, "gh.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncat '" + filepath.Join(dir, "answer") + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	testutil.WriteFile(h.t, filepath.Join(dir, "answer"), output)
	if err := os.Chmod(testutil.WriteFile(h.t, filepath.Join(dir, "gh"), script), 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestShipRecordsEachPushOnTheTask(t *testing.T) {
	h, box := shippedTask(t)
	first := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(0, "ship", "fix", "--yes")
	boxCommit(t, box, "more.txt", "Handle the expired session")
	second := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.expect(0, "ship", "fix", "--yes", "--branch", "agent/fix/again")

	ships := h.recorded("fix").Ships
	if len(ships) != 2 {
		t.Fatalf("recorded %+v, want two ships", ships)
	}
	for index, want := range []recordedShip{
		{SHA: first, Branch: "agent/fix/" + first[:8], GitHubRepo: "acme/api"},
		{SHA: second, Branch: "agent/fix/again", GitHubRepo: "acme/api"},
	} {
		got := ships[index]
		expectRecent(t, got.At)
		got.At = ""
		if got != want {
			t.Errorf("ship %d = %+v, want %+v", index, got, want)
		}
	}
	if content := testutil.ReadFile(t, filepath.Join(h.task("fix"), "task.json")); strings.Contains(content, "pull_request") {
		t.Errorf("recorded a pull request that was never opened:\n%s", content)
	}
}

func TestShipThatIsNotPushedRecordsNothing(t *testing.T) {
	h, _ := shippedTask(t)
	h.expect(1, "ship", "fix")
	if ships := h.recorded("fix").Ships; len(ships) != 0 {
		t.Fatalf("recorded %+v without a push", ships)
	}
}

func TestShipWithPRRecordsThePullRequestURLGhPrints(t *testing.T) {
	h, box := shippedTask(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	log := h.ghAnswering("https://github.com/acme/api/pull/7\n", 0)

	h.expect(0, "ship", "fix", "--yes", "--pr")

	ships := h.recorded("fix").Ships
	if len(ships) != 1 || ships[0].PullRequest != "https://github.com/acme/api/pull/7" || ships[0].SHA != sha {
		t.Fatalf("recorded %+v", ships)
	}
	if !strings.Contains(h.stdout.String(), "https://github.com/acme/api/pull/7") {
		t.Errorf("stdout %q does not show the pull request", h.stdout.String())
	}
	if call := testutil.ReadFile(t, log); !strings.HasPrefix(call, "pr create --draft --repo acme/api --head agent/fix/"+sha[:8]+" --title Fix the login redirect --body ") {
		t.Errorf("gh was asked %q", call)
	}
}

func TestShipKeepsThePushOnRecordWhenThePullRequestFails(t *testing.T) {
	h, box := shippedTask(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.ghAnswering("https://github.com/acme/api/pull/7\n", 1)

	h.expect(1, "ship", "fix", "--yes", "--pr")

	ships := h.recorded("fix").Ships
	if len(ships) != 1 || ships[0].SHA != sha || ships[0].PullRequest != "" {
		t.Fatalf("recorded %+v, want the push without a pull request", ships)
	}
}

func TestShipRecordsNoPullRequestWhenGhPrintsNoPullRequestURL(t *testing.T) {
	for _, answer := range []string{"", "created\n", "https://example.com/acme/api/pull/7\n", "https://github.com/acme/api/pull/7/files\n", "https://github.com/acme/api/issues/7\n"} {
		h, _ := shippedTask(t)
		h.ghAnswering(answer, 0)

		h.expect(0, "ship", "fix", "--yes", "--pr")

		ships := h.recorded("fix").Ships
		if len(ships) != 1 || ships[0].PullRequest != "" {
			t.Fatalf("after gh printed %q, recorded %+v", answer, ships)
		}
		h.expectStderr("recorded without a pull request")
	}
}

func TestShipWarnsAndExitsAsThePushDidWhenItCannotRecordIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only file")
	}
	h, box := shippedTask(t)
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	record := filepath.Join(h.task("fix"), "task.json")
	if err := os.Chmod(record, 0o444); err != nil {
		t.Fatal(err)
	}

	h.expect(0, "ship", "fix", "--yes")

	if pushed := testutil.RunGit(t, h.remote.Bare, "rev-parse", "agent/fix/"+sha[:8]); pushed != sha {
		t.Errorf("pushed %s, want %s", pushed, sha)
	}
	h.expectStderr("could not record it")
	if ships := h.recorded("fix").Ships; len(ships) != 0 {
		t.Errorf("recorded %+v in a file that cannot be written", ships)
	}
}

func TestUpRecordsTheBaseBranchTheSandboxStartedFrom(t *testing.T) {
	h := setup(t)
	testutil.RunGit(t, h.remote.Checkout, "push", "-q", h.remote.Bare, "HEAD:refs/heads/release")

	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expect(0, "up", h.remote.Checkout, "backport", "--base", "release")

	if base := h.recorded("fix").BaseBranch; base != "main" {
		t.Errorf("recorded base %q, want the default branch main", base)
	}
	if base := h.recorded("backport").BaseBranch; base != "release" {
		t.Errorf("recorded base %q, want release", base)
	}
}

func (h *harness) forgetNewerTaskFields(slug string) {
	h.t.Helper()
	path := filepath.Join(h.task(slug), "task.json")
	var fields map[string]any
	if err := json.Unmarshal([]byte(testutil.ReadFile(h.t, path)), &fields); err != nil {
		h.t.Fatal(err)
	}
	delete(fields, "base_branch")
	delete(fields, "ships")
	content, err := json.Marshal(fields)
	if err != nil {
		h.t.Fatal(err)
	}
	testutil.WriteFile(h.t, path, string(content))
}

func TestATaskRecordedBeforeBaseBranchAndShipsStillShipsAndTakes(t *testing.T) {
	h := takingSandbox(t, "fix")
	box := h.sandboxCommit("fix")
	sha := testutil.RunGit(t, box, "rev-parse", "HEAD")
	h.forgetNewerTaskFields("fix")
	h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/.tool/runs/result.json", body: "{}"}))

	h.expect(0, "ship", "fix", "--yes")
	h.expect(0, "take", "fix")

	if ships := h.recorded("fix").Ships; len(ships) != 1 || ships[0].SHA != sha {
		t.Fatalf("recorded %+v", ships)
	}
	manifest := testutil.ReadFile(t, filepath.Join(h.home, "takes", "fix", h.kept("fix")[0], "stage.json"))
	if strings.Contains(manifest, "base_branch") || !strings.Contains(manifest, sha) {
		t.Errorf("manifest = %s", manifest)
	}
}
