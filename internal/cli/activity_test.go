package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const workedExample = `{
  "format": "stage-activity/1",
  "activities": [{
    "id": "run-20261119T1012Z-4f2a",
    "title": "Fix the login redirect",
    "status": "running",
    "reason": "second review round after a failed verify",
    "round": {"current": 2, "max": 3},
    "started_at": "2026-11-19T10:12:04Z",
    "updated_at": "2026-11-19T10:31:40Z",
    "provider": "someone-else",
    "steps": [
      {"name": "plan", "state": "done"},
      {"name": "build", "state": "done"},
      {"name": "verify", "state": "failed"},
      {"name": "review", "state": "active"},
      {"name": "merge-ready", "state": "pending"}
    ],
    "groups": [
      {"name": "builders", "members": [
        {"name": "lead", "agent": "claude", "state": "idle", "session": {"source": "cao", "id": "1a2b3c4d"}}
      ]},
      {"name": "reviewers", "members": [
        {"name": "security", "agent": "codex", "state": "working", "session": {"source": "cao", "id": "5e6f7a8b"}},
        {"name": "style", "agent": "copilot", "state": "waiting", "session": {"source": "cao", "id": "9c0d1e2f"}}
      ]}
    ],
    "counters": {
      "tokens": {"value": 182340, "at_least": true},
      "elapsed_seconds": {"value": 1176, "at_least": false}
    }
  }]
}`

func providerProgram(t *testing.T, name, script string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "providers")
	path := testutil.WriteFile(t, filepath.Join(bin, name), "#!/bin/sh\n"+script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func printsDocument(t *testing.T, name, document string) {
	t.Helper()
	file := testutil.WriteFile(t, filepath.Join(t.TempDir(), name+".json"), document)
	providerProgram(t, name, "cat '"+file+"'\n")
}

func userConfig(t *testing.T, h *harness, hall string) {
	t.Helper()
	writeGitHubSetup(t, os.Getenv("STAGE_HOME"), hall)
}

func TestAProviderInTheUsersConfigSuppliesActivities(t *testing.T) {
	h := readyToRead(t)
	printsDocument(t, "loop-status", workedExample)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop-status\", \"--format\", \"stage-activity\"]\n")
	before := len(execCalls(h))

	fix := sessionsOf(t, h, "fix")
	if len(fix.ProviderErrors) != 0 || len(fix.Activities) != 1 {
		t.Fatalf("activities %+v errors %+v", fix.Activities, fix.ProviderErrors)
	}
	activity := fix.Activities[0]
	if activity.Provider != "loop" || activity.ID != "run-20261119T1012Z-4f2a" || activity.Status != "running" || activity.Round.Current != 2 || *activity.Round.Max != 3 {
		t.Errorf("activity %+v", activity)
	}
	if len(activity.Steps) != 5 || activity.Steps[3].State != "active" || len(activity.Groups) != 2 || activity.Groups[1].Members[1].Session.ID != "9c0d1e2f" {
		t.Errorf("steps %+v groups %+v", activity.Steps, activity.Groups)
	}
	if tokens := activity.Counters.Tokens; tokens.Value != 182340 || !tokens.AtLeast || activity.Counters.Cost != nil {
		t.Errorf("counters %+v", activity.Counters)
	}
	if calls := execCalls(h)[before:]; len(calls) != 1 || strings.Contains(calls[0], "loop-status") {
		t.Errorf("exec calls %q, want one with the provider only on stdin", calls)
	}

	h.expect(0, "list")
	if !strings.Contains(h.stdout.String(), "Fix the login redirect · round 2/3 · review · 1 waiting · ≥182k tokens") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestEveryProviderThatAppliesRunsInOrder(t *testing.T) {
	h := readyToRead(t)
	for _, name := range []string{"alpha", "beta"} {
		printsDocument(t, name, `{"format":"stage-activity/1","activities":[{"id":"`+name+`","title":"`+name+`","status":"running"}]}`)
	}
	userConfig(t, h, `[hall]
providers = ["beta", "missing", "alpha"]
[hall.provider.alpha]
command = ["alpha"]
[hall.provider.beta]
command = ["beta"]
[hall.provider.missing]
command = ["not-installed-in-this-stage"]
`)
	fix := sessionsOf(t, h, "fix")
	var order []string
	for _, activity := range fix.Activities {
		order = append(order, activity.Provider+"/"+activity.ID)
	}
	if !slices.Equal(order, []string{"beta/beta", "alpha/alpha"}) || len(fix.ProviderErrors) != 0 {
		t.Errorf("order %v errors %+v", order, fix.ProviderErrors)
	}
	h.expect(0, "list")
	if !strings.Contains(h.stdout.String(), "beta · +1 more") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestAProviderDocumentIsValidatedStrictly(t *testing.T) {
	activity := func(fields string) string {
		return `{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":"running"` + fields + `}]}`
	}
	many := strings.Repeat(`{"id":"x","title":"t","status":"running"},`, 17)
	h := readyToRead(t)
	serveCAO(h, "9889")
	badDocument := filepath.Join(t.TempDir(), "bad.json")
	providerProgram(t, "bad", "cat '"+badDocument+"'\n")
	printsDocument(t, "good", activity(""))
	userConfig(t, h, "[hall]\nproviders = [\"bad\", \"good\"]\n[hall.provider.bad]\ncommand = [\"bad\"]\n[hall.provider.good]\ncommand = [\"good\"]\n")
	for document, reason := range map[string]string{
		"not json":          "not a stage-activity/1 document",
		`{"activities":[]}`: "not a stage-activity/1 document",
		`{"format":"stage-activity/2","activities":[]}`:                                                                                    "speaks stage-activity/2",
		`{"format":"stage-activity/2.0","activities":[]}`:                                                                                  "speaks stage-activity/2.0",
		`{"format":"stage-activity/1.x","activities":[]}`:                                                                                  "speaks stage-activity/1.x",
		`{"format":"stage-activity/1","activities":[{"id":"a","Title":"t","status":"running"}]}`:                                           "only in case",
		`{"Format":"stage-activity/1","activities":[]}`:                                                                                    "only in case",
		activity(`,"groups":[{"name":"g"}]`):                                                                                               "no members",
		`{"format":"stage-activity/1"}`:                                                                                                    "no activities",
		`{"format":"stage-activity/1","activities":[` + strings.TrimSuffix(many, ",") + `]}`:                                               "more than 16 activities",
		`{"format":"stage-activity/1","activities":[{"title":"t","status":"running"}]}`:                                                    "needs an id",
		`{"format":"stage-activity/1","activities":[{"id":"a b","title":"t","status":"running"}]}`:                                         "needs an id",
		`{"format":"stage-activity/1","activities":[{"id":"a","status":"running"}]}`:                                                       "needs a title",
		`{"format":"stage-activity/1","activities":[{"id":"a","title":"t"}]}`:                                                              "needs a status",
		`{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":7}]}`:                                                   "shape",
		activity(`,"title2":1,"reason":"` + strings.Repeat("r", 501) + `"`):                                                                "reason over 500",
		activity(`,"round":{"current":-1}`):                                                                                                "round",
		activity(`,"round":{"current":3,"max":2}`):                                                                                         "round",
		activity(`,"round":{"current":1.5}`):                                                                                               "shape",
		activity(`,"started_at":"yesterday"`):                                                                                              "started_at",
		activity(`,"cao_port":0`):                                                                                                          "cao_port",
		activity(`,"steps":[{"name":"plan"}]`):                                                                                             "step with no state",
		activity(`,"groups":[{"name":"g","members":[{"state":"idle"}]}]`):                                                                  "member name",
		activity(`,"groups":[{"name":"g","members":[{"name":"m","state":"idle","session":{"source":"web","id":"1"}}]}]`):                   "session",
		activity(`,"counters":{"tokens":{"value":-1}}`):                                                                                    "tokens counter",
		activity(`,"counters":{"cost":{"value":1.5}}`):                                                                                     "currency",
		`{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":"running"},{"id":"a","title":"t","status":"running"}]}`: "repeats the id",
		"{\"format\":\"stage-activity/1\",\"activities\":[{\"id\":\"a\",\"title\":\"\xff\",\"status\":\"running\"}]}":                      "UTF-8",
	} {
		testutil.WriteFile(t, badDocument, document)
		fix := sessionsOf(t, h, "fix")
		if len(fix.ProviderErrors) != 1 || fix.ProviderErrors[0].Provider != "bad" || !strings.Contains(fix.ProviderErrors[0].Error, reason) {
			t.Errorf("errors %+v, want bad refused for %q", fix.ProviderErrors, reason)
		}
		if len(fix.Activities) != 1 || fix.Activities[0].Provider != "good" || len(fix.Sessions.Items) != 4 {
			t.Errorf("refusing bad for %q reached the others: %+v %+v", reason, fix.Activities, fix.Sessions)
		}
	}
}

func TestValuesALaterVersionAddsShowAsUnknownWithTheRawValue(t *testing.T) {
	h := readyToRead(t)
	printsDocument(t, "loop", `{"format":"stage-activity/1.1","future":true,"activities":[{"id":"a","title":"t","status":"paused","colour":"red",
		"steps":[{"name":"s","state":"queued"}],"groups":[{"name":"g","members":[{"name":"m","state":"thinking\u001b[2J"}]}]}]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	activity := sessionsOf(t, h, "fix").Activities[0]
	member := activity.Groups[0].Members[0]
	if activity.Status != "unknown" || activity.RawStatus != "paused" || activity.Steps[0].State != "unknown" || activity.Steps[0].RawState != "queued" ||
		member.State != "unknown" || member.RawState != `thinking\x1b[2J` {
		t.Errorf("activity %+v", activity)
	}
	if activity.Counters != nil {
		t.Errorf("a document without counters has counters %+v", activity.Counters)
	}
}

func TestAProviderThatFailsIsReportedAndTheOthersStillRun(t *testing.T) {
	h := readyToRead(t)
	providerProgram(t, "fails", "echo partial; exit 3\n")
	providerProgram(t, "floods", "head -c 300000 /dev/zero | tr '\\0' x\n")
	providerProgram(t, "reads", "cat\n")
	printsDocument(t, "good", `{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":"running"}]}`)
	userConfig(t, h, `[hall]
providers = ["fails", "floods", "reads", "good"]
[hall.provider.fails]
command = ["fails"]
[hall.provider.floods]
command = ["floods"]
[hall.provider.reads]
command = ["reads"]
[hall.provider.good]
command = ["good"]
`)
	fix := sessionsOf(t, h, "fix")
	reasons := map[string]string{}
	for _, failure := range fix.ProviderErrors {
		reasons[failure.Provider] = failure.Error
	}
	if !strings.Contains(reasons["fails"], "exited with status 3") || !strings.Contains(reasons["floods"], "over 256 KiB") || !strings.Contains(reasons["reads"], "not a stage-activity/1 document") {
		t.Errorf("errors %+v", fix.ProviderErrors)
	}
	if len(fix.Activities) != 1 || fix.Activities[0].Provider != "good" || len(fix.Disk) != 2 {
		t.Errorf("activities %+v disk %+v", fix.Activities, fix.Disk)
	}
	h.expect(0, "list", "--json")
	if strings.Contains(h.stdout.String(), "provider ") && strings.Contains(h.stdout.String(), "stage-read-") {
		t.Errorf("a provider read the script: %q", h.stdout.String())
	}
}

func TestProvidersDoNotRunInAStageWithoutTimeout(t *testing.T) {
	h := readyToRead(t)
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	tools := t.TempDir()
	for _, name := range []string{"sh", "grep", "sed", "head", "date", "sort", "cat", "tr", "df", "curl", "tmux", "loop"} {
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
	if len(fix.ProviderErrors) != 1 || !strings.Contains(fix.ProviderErrors[0].Error, "no timeout command") {
		t.Errorf("errors %+v", fix.ProviderErrors)
	}
}

func TestCAOIsReadOnTheConfiguredPortsAndOnPortsAProviderReports(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9890")
	serveCAO(h, "9891")
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":"running","cao_port":9891}]}`)
	userConfig(t, h, "[hall]\ncao_ports = [9890]\n[hall.provider.loop]\ncommand = [\"loop\"]\n")
	before := len(execCalls(h))
	fix := sessionsOf(t, h, "fix")
	servers := map[string]int{}
	for _, item := range fix.Sessions.Items {
		servers[item.Server]++
	}
	if servers["127.0.0.1:9890"] != 4 || servers["127.0.0.1:9891"] != 4 || servers["127.0.0.1:9889"] != 0 || fix.Sessions.Status != "ok" {
		t.Errorf("servers %v status %s", servers, fix.Sessions.Status)
	}
	if calls := execCalls(h)[before:]; len(calls) != 2 {
		t.Errorf("exec calls %q, want a second read for the reported port", calls)
	}
}

func TestShallowListRunsNoProvider(t *testing.T) {
	h := readyToRead(t)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	if fix := sessionsOf(t, h, "fix", "--shallow"); len(fix.Activities) != 0 || len(fix.ProviderErrors) != 0 || fix.Activities == nil {
		t.Errorf("fix %+v", fix)
	}
}

func TestFourFloodingProvidersFailAloneAndTheReadStillCounts(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	hall := "[hall]\nproviders = [\"f1\", \"f2\", \"f3\", \"f4\"]\n"
	for _, name := range []string{"f1", "f2", "f3", "f4"} {
		providerProgram(t, name, "head -c 300000 /dev/zero | tr '\\0' x\n")
		hall += "[hall.provider." + name + "]\ncommand = [\"" + name + "\"]\n"
	}
	userConfig(t, h, hall)
	fix := sessionsOf(t, h, "fix")
	if fix.Read == nil || fix.Read.Error != "" || fix.Sessions.Status != "ok" || len(fix.Sessions.Items) != 4 {
		t.Fatalf("read %+v sessions %+v", fix.Read, fix.Sessions)
	}
	if len(fix.ProviderErrors) != 4 {
		t.Fatalf("errors %+v", fix.ProviderErrors)
	}
	for _, failure := range fix.ProviderErrors {
		if !strings.Contains(failure.Error, "over 256 KiB") {
			t.Errorf("%s: %s", failure.Provider, failure.Error)
		}
	}
}

func TestHangingProvidersShareOneBudgetAndLeaveTimeForCAO(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	providerProgram(t, "hangs", "exec sleep 30\n")
	providerProgram(t, "also-hangs", "exec sleep 30\n")
	userConfig(t, h, "[hall]\nproviders = [\"hangs\", \"also-hangs\"]\n[hall.provider.hangs]\ncommand = [\"hangs\"]\n[hall.provider.also-hangs]\ncommand = [\"also-hangs\"]\n")
	started := time.Now()
	fix := sessionsOf(t, h, "fix")
	if took := time.Since(started); took > 8*time.Second {
		t.Errorf("the read took %s", took)
	}
	if fix.Read == nil || fix.Read.Error != "" || fix.Sessions.Status != "ok" || len(fix.Sessions.Items) != 4 {
		t.Fatalf("read %+v sessions %+v", fix.Read, fix.Sessions)
	}
	if len(fix.ProviderErrors) != 2 || !strings.Contains(fix.ProviderErrors[0].Error, "did not finish") || !strings.Contains(fix.ProviderErrors[1].Error, "ran out of time") {
		t.Errorf("errors %+v", fix.ProviderErrors)
	}
}

func TestAtMostEightReportedPortsAreReadAndRepeatsCollapse(t *testing.T) {
	h := readyToRead(t)
	var activities []string
	for i := range 16 {
		port := 9900 + i%10
		serveCAO(h, strconv.Itoa(port))
		activities = append(activities, fmt.Sprintf(`{"id":"a%d","title":"t","status":"running","cao_port":%d}`, i, port))
	}
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[`+strings.Join(activities, ",")+`]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	before := len(execCalls(h))
	fix := sessionsOf(t, h, "fix")
	servers := map[string]int{}
	for _, item := range fix.Sessions.Items {
		servers[item.Server]++
	}
	if len(servers) != 8 {
		t.Errorf("servers %v, want 8", servers)
	}
	for server, terminals := range servers {
		if terminals != 4 {
			t.Errorf("%s has %d terminals, want each port read once", server, terminals)
		}
	}
	if calls := execCalls(h)[before:]; len(calls) != 2 {
		t.Errorf("exec calls %q, want one second read", calls)
	}
}

func TestTheActivityCellStaysShort(t *testing.T) {
	h := readyToRead(t)
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[{"id":"a","title":"Fix   the     login redirect","status":"running",
		"round":{"current":123456789012,"max":999999999999},
		"steps":[{"name":"`+strings.Repeat("step ", 20)+`","state":"active"}],
		"groups":[{"name":"g","members":[`+strings.TrimSuffix(strings.Repeat(`{"name":"m","state":"waiting"},`, 64), ",")+`]}],
		"counters":{"tokens":{"value":1e300}}}]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	h.expect(0, "list")
	line := strings.Split(h.stdout.String(), "\n")[1]
	if !strings.Contains(line, "Fix the login redirect · round ") {
		t.Errorf("line %q, want the title's spaces collapsed", line)
	}
	if width := utf8.RuneCountInString(line); width > 160 {
		t.Errorf("line is %d columns: %q", width, line)
	}
}
