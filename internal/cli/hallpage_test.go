package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type pageScenario struct {
	Hash        string            `json:"hash,omitempty"`
	Clicks      []string          `json:"clicks,omitempty"`
	View        hallView          `json:"view"`
	Stored      map[string]string `json:"stored,omitempty"`
	DefaultMode string            `json:"default_mode,omitempty"`
}

type pageDialog struct {
	Open   bool     `json:"open"`
	Title  string   `json:"title"`
	Facts  string   `json:"facts"`
	Labels []string `json:"labels"`
	Picked string   `json:"picked"`
	Code   []string `json:"code"`
	Tail   string   `json:"tail"`
	Text   string   `json:"text"`
	Link   string   `json:"link"`
}

type pageResult struct {
	Status      string            `json:"status"`
	Code        []string          `json:"code"`
	Theme       string            `json:"theme"`
	Stored      map[string]string `json:"stored"`
	ModeLabel   string            `json:"mode_label"`
	Tiles       [][]string        `json:"tiles"`
	RowsText    string            `json:"rows_text"`
	OpenDetails int               `json:"open_details"`
	Copied      []string          `json:"copied"`
	GuideHidden bool              `json:"guide_hidden"`
	GuideLabel  string            `json:"guide_label"`
	Where       string            `json:"where"`
	Hash        string            `json:"hash"`
	Dialog      pageDialog        `json:"dialog"`
}

func showPage(t *testing.T, scenario pageScenario) pageResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("node is needed to run the page: %v", err)
		}
		t.Skip("node is not installed, so the page cannot be run")
	}
	data, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "testdata/hallpage-dom.js", "hallpage/hall.js", path).CombinedOutput()
	if err != nil {
		t.Fatalf("the page failed: %v\n%s", err, output)
	}
	var result pageResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("the page printed %q: %v", output, err)
	}
	return result
}

func pageView(stages ...stageRecord) hallView {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	view := hallView{Format: "stage-hall/1", ReadAt: now.Format(time.RFC3339), Stages: []hallStage{}, Scoreboard: []scoreRow{}}
	for _, record := range stages {
		view.Stages = append(view.Stages, describeForHall(record, now))
	}
	return view
}

func pageStage(slug string, jobs int) stageRecord {
	record := graphRecord()
	record.Slug, record.RunningJobs = slug, jobs
	return record
}

func stoppedStage(slug string) stageRecord {
	record := pageStage(slug, 0)
	record.State = "stopped"
	record.Activities = []activityRecord{}
	record.Sessions = sessionsRecord{Status: "not_read", Items: []sessionRecord{}}
	return record
}

func TestTheSummaryCountsWhatRunsWhatNeedsYouItsSessionsAndFinishedRuns(t *testing.T) {
	view := pageView(pageStage("fix", 1), stoppedStage("old"))
	view.Scoreboard = []scoreRow{{Repository: "acme/web", Runs: 6, Succeeded: 5, Failed: 1}, {Repository: "acme/api", Runs: 3, Succeeded: 3}}
	page := showPage(t, pageScenario{View: view})
	want := [][]string{
		{"Running", "1", "of 2 stages"},
		{"Needs you", "1", "fix: waiting"},
		{"Sessions", "3", "2 working · 1 idle"},
		{"Finished runs", "9", "8 succeeded · 1 failed"},
	}
	if len(page.Tiles) != len(want) {
		t.Fatalf("tiles %q, want %q", page.Tiles, want)
	}
	for i := range want {
		if !slices.Equal(page.Tiles[i], want[i]) {
			t.Errorf("tile %d is %q, want %q", i, page.Tiles[i], want[i])
		}
	}
	if page.Where != "read-only · 127.0.0.1:7420 · 2 stages" {
		t.Errorf("header says %q", page.Where)
	}
}

func TestStagesStartCollapsedAndOpenToWhatYouCanRunFromYourTerminal(t *testing.T) {
	page := showPage(t, pageScenario{View: pageView(pageStage("fix", 0))})
	if page.OpenDetails != 0 || len(page.Code) != 0 {
		t.Errorf("collapsed page shows %d details and commands %q", page.OpenDetails, page.Code)
	}
	page = showPage(t, pageScenario{Hash: "open=fix", View: pageView(pageStage("fix", 0))})
	if page.OpenDetails != 1 {
		t.Errorf("open details %d, want 1", page.OpenDetails)
	}
	for _, want := range []string{"stage attach fix 1a2b3c4d", "stage shell fix", "stage ship fix"} {
		if !slices.Contains(page.Code, want) {
			t.Errorf("commands %q lack %q", page.Code, want)
		}
	}
}

func TestACopyButtonCopiesItsCommandAndNothingElse(t *testing.T) {
	page := showPage(t, pageScenario{Hash: "open=fix", Clicks: []string{"copy stage ship fix"}, View: pageView(pageStage("fix", 0))})
	if !slices.Equal(page.Copied, []string{"stage ship fix"}) {
		t.Errorf("copied %q", page.Copied)
	}
}

func TestWithNoStagesThePageSaysHowToStartOne(t *testing.T) {
	page := showPage(t, pageScenario{View: pageView()})
	for _, want := range []string{"No stages yet", "stage up <repo> <slug>"} {
		if !strings.Contains(page.RowsText, want) {
			t.Errorf("page says %q, want %q", page.RowsText, want)
		}
	}
	if len(page.Tiles) != 0 {
		t.Errorf("tiles %q with no stages", page.Tiles)
	}
}

func TestTheGuideCanBeHiddenAndStaysHiddenInThisBrowser(t *testing.T) {
	page := showPage(t, pageScenario{View: pageView(pageStage("fix", 0))})
	if page.GuideHidden || page.GuideLabel != "Hide guide" {
		t.Errorf("first visit: hidden %v, label %q", page.GuideHidden, page.GuideLabel)
	}
	page = showPage(t, pageScenario{Clicks: []string{"guide"}, View: pageView(pageStage("fix", 0))})
	if !page.GuideHidden || page.GuideLabel != "Guide" || page.Stored["stage-hall-guide"] != "hidden" {
		t.Errorf("after hiding: hidden %v, label %q, stored %v", page.GuideHidden, page.GuideLabel, page.Stored)
	}
	page = showPage(t, pageScenario{Stored: map[string]string{"stage-hall-guide": "hidden"}, View: pageView(pageStage("fix", 0))})
	if !page.GuideHidden {
		t.Errorf("a hidden guide came back")
	}
}

func TestTheModeSwitchFlipsTheThemeAndRemembersItInThisBrowser(t *testing.T) {
	page := showPage(t, pageScenario{Clicks: []string{"mode"}, DefaultMode: "dark", View: pageView(pageStage("fix", 0))})
	if page.Theme != "light" || page.Stored["stage-hall-mode"] != "light" || page.ModeLabel != "Switch to dark mode" {
		t.Errorf("theme %q, stored %v, label %q; want light, remembered, and a switch back to dark", page.Theme, page.Stored, page.ModeLabel)
	}
	page = showPage(t, pageScenario{DefaultMode: "light", View: pageView(pageStage("fix", 0))})
	if page.Theme != "" || page.ModeLabel != "Switch to dark mode" {
		t.Errorf("unclicked: theme %q, label %q; want the configured mode left alone", page.Theme, page.ModeLabel)
	}
}

func TestAModeChosenBeforeIsKeptOnTheNextVisit(t *testing.T) {
	page := showPage(t, pageScenario{DefaultMode: "dark", Stored: map[string]string{"stage-hall-mode": "light"}, View: pageView(pageStage("fix", 0))})
	if page.Theme != "light" || page.ModeLabel != "Switch to dark mode" {
		t.Errorf("theme %q, label %q", page.Theme, page.ModeLabel)
	}
	page = showPage(t, pageScenario{DefaultMode: "dark", Stored: map[string]string{"stage-hall-mode": "neon"}, View: pageView(pageStage("fix", 0))})
	if page.Theme != "" {
		t.Errorf("an unknown stored mode was applied: %q", page.Theme)
	}
}

func TestAnExpandedRunningStageShowsHowToAttachToEachCAOTerminal(t *testing.T) {
	page := showPage(t, pageScenario{Hash: "open=fix", View: pageView(pageStage("fix", 0))})
	var attach []string
	for _, command := range page.Code {
		if strings.HasPrefix(command, "stage attach ") {
			attach = append(attach, command)
		}
	}
	want := []string{"stage attach fix 1a2b3c4d", "stage attach fix 5e6f7a8b"}
	if !slices.Equal(attach, want) {
		t.Errorf("attach commands %q, want %q", attach, want)
	}
}

func TestAStoppedStageShowsNoAttachCommand(t *testing.T) {
	stopped := pageStage("fix", 0)
	stopped.State = "stopped"
	page := showPage(t, pageScenario{Hash: "open=fix", View: pageView(stopped)})
	if len(page.Code) != 0 {
		t.Errorf("commands %q on a stopped stage", page.Code)
	}
}

func waitingOnSecurity(slug string) stageRecord {
	record := pageStage(slug, 0)
	members := record.Activities[0].Groups[1].Members
	members[0].State, members[1].State = "waiting", "idle"
	return record
}

func TestEachStageOpensItsOwnGraphInADialog(t *testing.T) {
	page := showPage(t, pageScenario{Clicks: []string{"graph fix"}, View: pageView(pageStage("docs", 0), pageStage("fix", 0))})
	if !page.Dialog.Open || page.Dialog.Title != "fix" || page.Hash != "#graph=fix" {
		t.Fatalf("dialog %+v, hash %q; want fix's graph open and linked", page.Dialog, page.Hash)
	}
	for _, want := range []string{"fix", "Fix the login redirect", "builders", "lead", "reviewers", "security", "style", "sessions", "go test ./..."} {
		if !slices.Contains(page.Dialog.Labels, want) {
			t.Errorf("graph %q lacks %q", page.Dialog.Labels, want)
		}
	}
	if slices.Contains(page.Dialog.Labels, "docs") {
		t.Errorf("fix's graph draws another stage: %q", page.Dialog.Labels)
	}
	if !strings.Contains(page.Dialog.Link, "#graph=fix") {
		t.Errorf("link %q", page.Dialog.Link)
	}
}

func TestTheGraphDialogStartsOnTheAgentThatNeedsYou(t *testing.T) {
	page := showPage(t, pageScenario{Clicks: []string{"graph fix", "copy stage attach fix 5e6f7a8b"}, View: pageView(waitingOnSecurity("fix"))})
	if page.Dialog.Picked != "security" {
		t.Fatalf("picked %q, want the waiting security agent", page.Dialog.Picked)
	}
	if want := []string{"stage attach fix 5e6f7a8b", "stage attach fix 5e6f7a8b --watch"}; !slices.Equal(page.Dialog.Code, want) {
		t.Errorf("commands %q, want %q", page.Dialog.Code, want)
	}
	if !slices.Equal(page.Copied, []string{"stage attach fix 5e6f7a8b"}) {
		t.Errorf("copied %q", page.Copied)
	}
}

func TestClickingAnAgentInTheGraphShowsItsTerminalLinesAndHowToReachIt(t *testing.T) {
	page := showPage(t, pageScenario{Clicks: []string{"graph fix", "agent go test ./..."}, View: pageView(pageStage("fix", 0))})
	if page.Dialog.Picked != "go test ./..." || page.Dialog.Tail != "ok" {
		t.Errorf("picked %q with terminal %q, want the job and its last line", page.Dialog.Picked, page.Dialog.Tail)
	}
	if want := []string{"stage job output fix 1 --follow"}; !slices.Equal(page.Dialog.Code, want) {
		t.Errorf("commands %q, want %q", page.Dialog.Code, want)
	}
	page = showPage(t, pageScenario{Clicks: []string{"graph fix", "agent lead"}, View: pageView(pageStage("fix", 0))})
	if want := []string{"stage attach fix 1a2b3c4d", "stage attach fix 1a2b3c4d --watch"}; page.Dialog.Picked != "lead" || !slices.Equal(page.Dialog.Code, want) {
		t.Errorf("picked %q with commands %q, want lead and %q", page.Dialog.Picked, page.Dialog.Code, want)
	}
}

func TestClosingTheGraphDialogClearsItsLink(t *testing.T) {
	for _, closing := range []string{"close", "escape"} {
		page := showPage(t, pageScenario{Clicks: []string{"graph fix", closing}, View: pageView(pageStage("fix", 0))})
		if page.Dialog.Open || page.Hash != "" {
			t.Errorf("%s: open %v, hash %q", closing, page.Dialog.Open, page.Hash)
		}
	}
}

func TestALinkOpensAStagesGraphDialog(t *testing.T) {
	page := showPage(t, pageScenario{Hash: "graph=fix", View: pageView(pageStage("fix", 0))})
	if !page.Dialog.Open || page.Dialog.Title != "fix" {
		t.Errorf("dialog %+v, want fix's graph", page.Dialog)
	}
	page = showPage(t, pageScenario{Hash: "graph=gone", View: pageView(pageStage("fix", 0))})
	if page.Dialog.Open {
		t.Errorf("a link to a stage the hall does not have opened %+v", page.Dialog)
	}
}

func TestAStoppedStagesGraphOffersNothingToAttachTo(t *testing.T) {
	stopped := pageStage("fix", 0)
	stopped.State = "stopped"
	page := showPage(t, pageScenario{Clicks: []string{"graph fix", "agent lead"}, View: pageView(stopped)})
	if len(page.Dialog.Code) != 0 || !strings.Contains(page.Dialog.Text, "not running") {
		t.Errorf("commands %q, text %q; want none, and that the stage is not running", page.Dialog.Code, page.Dialog.Text)
	}
}
