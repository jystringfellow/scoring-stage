package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func kmPrints(t *testing.T, h *harness, document string) {
	t.Helper()
	file := testutil.WriteFile(t, filepath.Join(t.TempDir(), "status.json"), document)
	path := testutil.WriteFile(t, filepath.Join(h.sbx.AgentHome(), ".local", "bin", "km"),
		"#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/km-calls.log\"\ncase \"$1 $2\" in\n\"status -json\") cat '"+file+"' ;;\n\"report -json\") cat \"$HOME/km-report.json\" 2>/dev/null || echo '{\"runs\":[]}' ;;\n*) exit 64 ;;\nesac\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func kmReports(t *testing.T, h *harness, document string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(h.sbx.AgentHome(), "km-report.json"), document)
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheKapellmeisterProviderTranslatesKmStatus(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9890")
	kmPrints(t, h, fixture(t, "km-status-schema-1.json"))
	fix := sessionsOf(t, h, "fix")
	if len(fix.ProviderErrors) != 0 || len(fix.Activities) != 2 {
		t.Fatalf("activities %+v errors %+v", fix.Activities, fix.ProviderErrors)
	}
	run := fix.Activities[0]
	if run.Provider != "kapellmeister" || run.ID != "20261119T221504Z-7c1e" || run.Title != "km run · thorough" || run.Status != "waiting" ||
		run.Reason != "lead needs input: asks whether to keep the old redirect" || run.Round.Current != 2 || *run.Round.Max != 3 ||
		run.StartedAt != "2026-11-19T22:15:04Z" || run.UpdatedAt != "2026-11-19T22:33:02Z" || run.CAOPort != 9890 {
		t.Errorf("run %+v", run)
	}
	var steps []string
	for _, step := range run.Steps {
		steps = append(steps, step.Name+"="+step.State)
	}
	if want := []string{"verify=done", "plan=done", "implement=done", "review=active", "merge-ready=pending"}; !slices.Equal(steps, want) {
		t.Errorf("steps %v, want %v", steps, want)
	}
	var members []string
	for _, group := range run.Groups {
		for _, member := range group.Members {
			session := "-"
			if member.Session != nil {
				session = member.Session.Source + ":" + member.Session.ID
			}
			members = append(members, group.Name+"/"+member.Name+"/"+member.Agent+"/"+member.State+"/"+session)
		}
	}
	if want := []string{
		"leads/planner/claude/exited/-",
		"leads/implementer/codex/idle/cao:1a2b3c4d",
		"review/lead/claude/waiting/cao:5e6f7a8b",
		"review/security/copilot/working/cao:9c0d1e2f",
	}; !slices.Equal(members, want) {
		t.Errorf("members %v, want %v", members, want)
	}
	if tokens := run.Counters.Tokens; tokens == nil || tokens.Value != 182340 || !tokens.AtLeast || run.Counters.Cost != nil {
		t.Errorf("counters %+v", run.Counters)
	}
	abandoned := fix.Activities[1]
	if abandoned.Status != "stopped" || abandoned.Title != "km tune · quick" || abandoned.CAOPort != 9889 || abandoned.Counters != nil && abandoned.Counters.Tokens != nil {
		t.Errorf("abandoned run %+v", abandoned)
	}

	servers := map[string]int{}
	for _, item := range fix.Sessions.Items {
		servers[item.Server]++
	}
	if servers["127.0.0.1:9890"] != 4 || fix.Sessions.Status != "ok" {
		t.Errorf("servers %v status %s, want the run's own CAO server read", servers, fix.Sessions.Status)
	}

	h.expect(0, "list")
	if !strings.Contains(h.stdout.String(), "km run · thorough · round 2/3 · review · 1 waiting · ≥182k tokens · +1 more") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestKmStatusFromBeforeTheSchemaStillShowsItsRuns(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, fixture(t, "km-status-before-schema.json"))
	fix := sessionsOf(t, h, "fix")
	if len(fix.ProviderErrors) != 0 || len(fix.Activities) != 1 {
		t.Fatalf("activities %+v errors %+v", fix.Activities, fix.ProviderErrors)
	}
	run := fix.Activities[0]
	if run.Title != "km run · thorough" || run.Status != "running" || run.Round.Current != 2 || run.CAOPort != 0 ||
		len(run.Steps) != 1 || run.Steps[0].Name != "review" || run.Steps[0].State != "active" ||
		len(run.Groups) != 1 || run.Groups[0].Members[0].Name != "review.lead" || run.Groups[0].Members[0].State != "working" {
		t.Errorf("run %+v", run)
	}
}

func TestKmStatusThatCannotBeReadIsReportedForKapellmeisterAlone(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	for document, reason := range map[string]string{
		"not json":                    "cannot read",
		`{"runs":[{"schema":2}]}`:     "schema 2",
		`{"runs":[{"run_id":"a b"}]}`: "needs an id",
		`{"runs":{}}`:                 "cannot read",
	} {
		kmPrints(t, h, document)
		fix := sessionsOf(t, h, "fix")
		if len(fix.ProviderErrors) != 1 || fix.ProviderErrors[0].Provider != "kapellmeister" || !strings.Contains(fix.ProviderErrors[0].Error, reason) {
			t.Errorf("%q: errors %+v, want %q", document, fix.ProviderErrors, reason)
		}
		if len(fix.Sessions.Items) != 4 {
			t.Errorf("%q reached the sessions: %+v", document, fix.Sessions)
		}
	}
}

func TestKmIsFoundInTheAgentsLocalBinWithoutShadowingStagesTools(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	kmPrints(t, h, `{"runs":[]}`)
	shadow := testutil.WriteFile(t, filepath.Join(h.sbx.AgentHome(), ".local", "bin", "curl"), "#!/bin/sh\necho shadowed\n")
	if err := os.Chmod(shadow, 0o755); err != nil {
		t.Fatal(err)
	}
	fix := sessionsOf(t, h, "fix")
	if len(fix.ProviderErrors) != 0 || fix.Activities == nil || len(fix.Activities) != 0 || len(fix.Sessions.Items) != 4 {
		t.Errorf("activities %+v errors %+v sessions %+v", fix.Activities, fix.ProviderErrors, fix.Sessions)
	}
}

func TestKapellmeisterRunsFirstByDefaultAndCanBeLeftOut(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, `{"runs":[{"run_id":"r1","command":"run","ensemble":"e","liveness":"alive"}]}`)
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[{"id":"l1","title":"loop","status":"running"}]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	var order []string
	for _, activity := range sessionsOf(t, h, "fix").Activities {
		order = append(order, activity.Provider)
	}
	if !slices.Equal(order, []string{"kapellmeister", "loop"}) {
		t.Errorf("order %v", order)
	}
	userConfig(t, h, "[hall]\nproviders = [\"loop\"]\n[hall.provider.loop]\ncommand = [\"loop\"]\n")
	order = nil
	for _, activity := range sessionsOf(t, h, "fix").Activities {
		order = append(order, activity.Provider)
	}
	if !slices.Equal(order, []string{"loop"}) {
		t.Errorf("order %v with kapellmeister left out", order)
	}
}

func TestAKmStatusCapturedFromARealRunTranslates(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	kmPrints(t, h, fixture(t, "km-status-schema-1-captured.json"))
	fix := sessionsOf(t, h, "fix")
	if len(fix.ProviderErrors) != 0 || len(fix.Activities) != 1 {
		t.Fatalf("activities %+v errors %+v", fix.Activities, fix.ProviderErrors)
	}
	run := fix.Activities[0]
	if run.ID != "20261120-063806-7153d2" || run.Title != "km run · thorough" || run.Status != "running" || run.CAOPort != 9889 ||
		run.Round.Current != 1 || *run.Round.Max != 3 {
		t.Errorf("run %+v", run)
	}
	var steps []string
	for _, step := range run.Steps {
		steps = append(steps, step.Name+"="+step.State)
	}
	if want := []string{"verify=done", "plan=done", "test-plan=done", "implement=done", "verify=done", "review=active", "gate=pending", "test=pending"}; !slices.Equal(steps, want) {
		t.Errorf("steps %v, want %v", steps, want)
	}
	var members []string
	for _, group := range run.Groups {
		for _, member := range group.Members {
			session := "-"
			if member.Session != nil {
				session = member.Session.ID
			}
			members = append(members, group.Name+"/"+member.Name+"/"+member.State+"/"+session)
		}
	}
	if want := []string{
		"leads/planner/exited/-", "leads/implementer/idle/7cdae93c", "leads/gate/idle/-", "leads/classifier/idle/-", "leads/tonmeister/idle/-",
		"review/lead/working/-", "review/second/working/-",
	}; !slices.Equal(members, want) {
		t.Errorf("members %v, want %v", members, want)
	}
	if tokens := run.Counters.Tokens; tokens == nil || tokens.Value != 19445 || tokens.AtLeast {
		t.Errorf("tokens %+v", run.Counters.Tokens)
	}
}

func kmRuns(t *testing.T, runs ...string) string {
	t.Helper()
	return `{"runs":[` + strings.Join(runs, ",") + `]}`
}

func TestLiveKmRunsComeFirstAndRunsLeftOutAreSaid(t *testing.T) {
	h := readyToRead(t)
	var runs []string
	for i := range 3 {
		runs = append(runs, fmt.Sprintf(`{"run_id":"old-%d","command":"run","liveness":"abandoned"}`, i))
	}
	for i := range 15 {
		runs = append(runs, fmt.Sprintf(`{"run_id":"live-%d","command":"run","liveness":"alive"}`, i))
	}
	kmPrints(t, h, kmRuns(t, runs...))
	fix := sessionsOf(t, h, "fix")
	alive := 0
	for _, activity := range fix.Activities {
		if strings.HasPrefix(activity.ID, "live-") {
			alive++
		}
	}
	if len(fix.Activities) != 16 || alive != 15 {
		t.Errorf("%d activities, %d of them live, want 16 with every live run", len(fix.Activities), alive)
	}
	if len(fix.ProviderErrors) != 1 || !strings.Contains(fix.ProviderErrors[0].Error, "2 more km runs were left out") {
		t.Errorf("errors %+v", fix.ProviderErrors)
	}
}

func TestABadKmRunIsSkippedAndTheOthersKept(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, kmRuns(t,
		`{"run_id":"r1","command":"run","liveness":"alive"}`,
		`{"run_id":"a b","command":"run","liveness":"alive"}`,
		`null`,
		`{"run_id":"r1","command":"tune","liveness":"alive"}`,
		`{"schema":2,"run_id":"r2"}`,
		`{"run_id":"big","command":"run","liveness":"alive","usage":{"input_tokens":1e308,"output_tokens":1e308,"measured":"all"}}`,
	))
	fix := sessionsOf(t, h, "fix")
	var ids []string
	for _, activity := range fix.Activities {
		ids = append(ids, activity.ID)
		if activity.ID == "big" && activity.Counters != nil {
			t.Errorf("tokens past what a number holds are shown: %+v", activity.Counters)
		}
	}
	if !slices.Equal(ids, []string{"r1", "big"}) {
		t.Errorf("activities %v, want r1 and big kept", ids)
	}
	var reasons []string
	for _, failure := range fix.ProviderErrors {
		if failure.Provider != "kapellmeister" || strings.Contains(failure.Error, "json:") {
			t.Errorf("error %+v", failure)
		}
		reasons = append(reasons, failure.Error)
	}
	joined := strings.Join(reasons, "\n")
	for _, want := range []string{"needs an id", "not a run", "repeats the run r1", "schema 2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q lack %q", joined, want)
		}
	}
}

func TestAStepFromAnEarlierRoundIsPendingAgainAndTimesAreToTheSecond(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, kmRuns(t, `{"schema":1,"run_id":"r","command":"run","liveness":"alive","round":2,"max_rounds":3,
		"started":"2026-11-20T06:39:58.993343465Z","updated":"2026-11-20T06:46:15.173096853Z",
		"score":{"steps":[{"name":"plan"},{"name":"implement"},{"name":"review"}]},
		"current":[{"step":"implement","round":2}],
		"trail":[{"step":"plan","round":0},{"step":"implement","round":1},{"step":"review","round":1,"exit":"changes"}],
		"ensemble":{"name":"e","sections":[{"name":"s","chairs":[
			{"name":"lead","state":"playing","needs_input":{"count":1,"reason":"which redirect to keep"}},
			{"name":"second","state":"playing","needs_input":{"count":2,"reason":"later"}}]}]}}`))
	run := sessionsOf(t, h, "fix").Activities[0]
	var steps []string
	for _, step := range run.Steps {
		steps = append(steps, step.Name+"="+step.State)
	}
	if want := []string{"plan=done", "implement=active", "review=pending"}; !slices.Equal(steps, want) {
		t.Errorf("steps %v, want %v", steps, want)
	}
	if run.StartedAt != "2026-11-20T06:39:58Z" || run.UpdatedAt != "2026-11-20T06:46:15Z" {
		t.Errorf("times %s %s", run.StartedAt, run.UpdatedAt)
	}
	if run.Status != "waiting" || run.Reason != "lead needs input: which redirect to keep; 1 more chair needs input" {
		t.Errorf("status %s reason %q", run.Status, run.Reason)
	}
}

func TestAKmThatCannotBeRunIsAbsentAndRepeatedStepsCollapse(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, `{"runs":[]}`)
	if err := os.Chmod(filepath.Join(h.sbx.AgentHome(), ".local", "bin", "km"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fix := sessionsOf(t, h, "fix"); len(fix.ProviderErrors) != 0 {
		t.Errorf("a km that is not executable reported %+v", fix.ProviderErrors)
	}
	long := strings.Repeat("s", 120)
	kmPrints(t, h, kmRuns(t, `{"run_id":"r","command":"run","ensemble":"e","liveness":"alive","playing":[{"step":"`+long+`","chair":"a"},{"step":"`+long+`","chair":"b"}]}`))
	if steps := sessionsOf(t, h, "fix").Activities[0].Steps; len(steps) != 1 {
		t.Errorf("steps %+v, want the repeated step once", steps)
	}
}

func TestFinishedKmRunsComeFromKmsReport(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, kmRuns(t, `{"run_id":"live","command":"run","ensemble":"duo","liveness":"alive"}`))
	kmReports(t, h, `{"runs":[
		{"run_id":"r-done","command":"run","ensemble":"duo","started":"2026-11-20T10:55:39.5Z","outcome":"approved","rounds":1,"duration_ms":91000,
		 "usage":{"known":true,"input_tokens":15000,"output_tokens":4000,"cache_read_tokens":99}},
		{"run_id":"r-changes","command":"run","ensemble":"duo","outcome":"changes_requested","rounds":2,"usage":{"known":false}},
		{"run_id":"live","command":"run","ensemble":"duo","outcome":"approved"}]}`)
	started := time.Now()
	fix := sessionsOf(t, h, "fix")
	var got []string
	for _, activity := range fix.Activities {
		got = append(got, activity.ID+"="+activity.Status+"/"+activity.Reason)
	}
	if want := []string{"live=running/", "r-done=succeeded/approved", "r-changes=stopped/changes requested"}; !slices.Equal(got, want) {
		t.Errorf("activities %v, want %v", got, want)
	}
	done := fix.Activities[1]
	if done.Round == nil || done.Round.Current != 1 || done.Counters == nil || done.Counters.Tokens == nil || done.Counters.Tokens.Value != 19000 ||
		done.Counters.Tokens.AtLeast || done.Counters.ElapsedSeconds == nil || done.Counters.ElapsedSeconds.Value != 91 || done.StartedAt != "2026-11-20T10:55:39Z" {
		t.Errorf("finished run %+v counters %+v", done, done.Counters)
	}
	if changes := fix.Activities[2]; changes.Counters != nil && changes.Counters.Tokens != nil {
		t.Errorf("tokens km did not know are shown: %+v", changes.Counters)
	}
	calls := testutil.ReadFile(t, filepath.Join(h.sbx.AgentHome(), "km-calls.log"))
	var since string
	for _, call := range strings.Split(calls, "\n") {
		if rest, ok := strings.CutPrefix(call, "report -json -since "); ok {
			since = rest
		}
	}
	at, err := time.Parse(time.RFC3339, since)
	if err != nil || at.Before(started.Add(-2*time.Hour-time.Minute)) || at.After(started.Add(-2*time.Hour+time.Minute)) {
		t.Errorf("km report was asked for runs since %q, want two hours ago; calls %q", since, calls)
	}
}

func TestARowNamesAFinishedRunsOutcomeTimeAndTokens(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, `{"runs":[]}`)
	kmReports(t, h, `{"runs":[{"run_id":"r","command":"run","ensemble":"duo","outcome":"approved","rounds":1,"duration_ms":91000,"usage":{"known":true,"input_tokens":15000,"output_tokens":4000}}]}`)
	h.expect(0, "list")
	if !strings.Contains(h.stdout.String(), "km run · duo · round 1 · succeeded · 1m31s · 19k tokens") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestAKmReportThatSkipsOrFailsLeavesLiveRunsShown(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, kmRuns(t, `{"run_id":"live","command":"run","ensemble":"duo","liveness":"alive"}`))
	kmReports(t, h, `{"runs":[{"run_id":"r","command":"run","outcome":"approved"}],"skipped_dirs":["/home/agent/missing"]}`)
	if fix := sessionsOf(t, h, "fix"); len(fix.Activities) != 2 || len(fix.ProviderErrors) != 0 {
		t.Errorf("activities %+v errors %+v, want both runs and no error for a skipped directory", fix.Activities, fix.ProviderErrors)
	}
	kmReports(t, h, "not json")
	fix := sessionsOf(t, h, "fix")
	if len(fix.Activities) != 1 || fix.Activities[0].ID != "live" || len(fix.ProviderErrors) != 1 || fix.ProviderErrors[0].Provider != "kapellmeister" {
		t.Errorf("activities %+v errors %+v, want the live run kept and one error", fix.Activities, fix.ProviderErrors)
	}
}

func TestAStepKmIsOnThatIsNotInTheScoreShowsFirstAsActive(t *testing.T) {
	h := readyToRead(t)
	kmPrints(t, h, fixture(t, "km-status-preflight.json"))
	run := sessionsOf(t, h, "fix").Activities[0]
	var steps []string
	for _, step := range run.Steps {
		steps = append(steps, step.Name+"="+step.State)
	}
	if want := []string{"preflight=active", "draft=pending", "check=pending"}; !slices.Equal(steps, want) {
		t.Errorf("steps %v, want %v", steps, want)
	}
	if len(run.Groups) != 2 || run.Groups[1].Members[1].Agent != "agy" || run.Groups[1].Members[1].State != "idle" {
		t.Errorf("groups %+v", run.Groups)
	}
	h.expect(0, "list")
	if !strings.Contains(h.stdout.String(), "km run · duo · round 0/2 · preflight") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestManyStepsKmIsOnOutsideTheScoreAreBounded(t *testing.T) {
	h := readyToRead(t)
	var current []string
	for i := range 300 {
		current = append(current, fmt.Sprintf(`{"step":"extra-%d","round":0}`, i))
	}
	kmPrints(t, h, kmRuns(t, `{"schema":1,"run_id":"r","command":"run","liveness":"alive","score":{"steps":[{"name":"draft"},{"name":"check"}]},"current":[`+strings.Join(current, ",")+`]}`))
	fix := sessionsOf(t, h, "fix")
	if len(fix.Activities) != 1 {
		t.Fatalf("activities %+v errors %+v", fix.Activities, fix.ProviderErrors)
	}
	steps := fix.Activities[0].Steps
	var names []string
	for _, step := range steps {
		names = append(names, step.Name)
	}
	if len(steps) > maxSteps || !slices.Contains(names, "draft") || !slices.Contains(names, "check") {
		t.Errorf("%d steps %v, want a few current steps and the score kept", len(steps), names)
	}
}
