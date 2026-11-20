package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func kmPrints(t *testing.T, h *harness, document string) {
	t.Helper()
	file := testutil.WriteFile(t, filepath.Join(t.TempDir(), "status.json"), document)
	path := testutil.WriteFile(t, filepath.Join(h.sbx.AgentHome(), ".local", "bin", "km"),
		"#!/bin/sh\n[ \"$*\" = \"status -json\" ] || exit 64\ncat '"+file+"'\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
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
