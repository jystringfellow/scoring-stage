package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

type pageScenario struct {
	Width       int               `json:"width"`
	Hash        string            `json:"hash,omitempty"`
	Click       string            `json:"click,omitempty"`
	View        hallView          `json:"view"`
	Stored      map[string]string `json:"stored,omitempty"`
	DefaultMode string            `json:"default_mode,omitempty"`
}

type pageResult struct {
	Status      string            `json:"status"`
	GraphHidden bool              `json:"graph_hidden"`
	TableHidden bool              `json:"table_hidden"`
	GraphText   []string          `json:"graph_text"`
	Drawn       []string          `json:"drawn"`
	Pictures    int               `json:"pictures"`
	Pressed     string            `json:"pressed"`
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
	page := showPage(t, pageScenario{Width: 1280, View: view})
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
	page := showPage(t, pageScenario{Width: 1280, View: pageView(pageStage("fix", 0))})
	if page.OpenDetails != 0 || len(page.Code) != 0 {
		t.Errorf("collapsed page shows %d details and commands %q", page.OpenDetails, page.Code)
	}
	page = showPage(t, pageScenario{Width: 1280, Hash: "open=fix", View: pageView(pageStage("fix", 0))})
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
	page := showPage(t, pageScenario{Width: 1280, Hash: "open=fix", Click: "copy stage ship fix", View: pageView(pageStage("fix", 0))})
	if !slices.Equal(page.Copied, []string{"stage ship fix"}) {
		t.Errorf("copied %q", page.Copied)
	}
}

func TestWithNoStagesThePageSaysHowToStartOne(t *testing.T) {
	page := showPage(t, pageScenario{Width: 1280, View: pageView()})
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
	page := showPage(t, pageScenario{Width: 1280, View: pageView(pageStage("fix", 0))})
	if page.GuideHidden || page.GuideLabel != "Hide guide" {
		t.Errorf("first visit: hidden %v, label %q", page.GuideHidden, page.GuideLabel)
	}
	page = showPage(t, pageScenario{Width: 1280, Click: "guide", View: pageView(pageStage("fix", 0))})
	if !page.GuideHidden || page.GuideLabel != "Guide" || page.Stored["stage-hall-guide"] != "hidden" {
		t.Errorf("after hiding: hidden %v, label %q, stored %v", page.GuideHidden, page.GuideLabel, page.Stored)
	}
	page = showPage(t, pageScenario{Width: 1280, Stored: map[string]string{"stage-hall-guide": "hidden"}, View: pageView(pageStage("fix", 0))})
	if !page.GuideHidden {
		t.Errorf("a hidden guide came back")
	}
}

func TestTheModeSwitchFlipsTheThemeAndRemembersItInThisBrowser(t *testing.T) {
	page := showPage(t, pageScenario{Width: 1280, Click: "mode", DefaultMode: "dark", View: pageView(pageStage("fix", 0))})
	if page.Theme != "light" || page.Stored["stage-hall-mode"] != "light" || page.ModeLabel != "Switch to dark mode" {
		t.Errorf("theme %q, stored %v, label %q; want light, remembered, and a switch back to dark", page.Theme, page.Stored, page.ModeLabel)
	}
	page = showPage(t, pageScenario{Width: 1280, DefaultMode: "light", View: pageView(pageStage("fix", 0))})
	if page.Theme != "" || page.ModeLabel != "Switch to dark mode" {
		t.Errorf("unclicked: theme %q, label %q; want the configured mode left alone", page.Theme, page.ModeLabel)
	}
}

func TestAModeChosenBeforeIsKeptOnTheNextVisit(t *testing.T) {
	page := showPage(t, pageScenario{Width: 1280, DefaultMode: "dark", Stored: map[string]string{"stage-hall-mode": "light"}, View: pageView(pageStage("fix", 0))})
	if page.Theme != "light" || page.ModeLabel != "Switch to dark mode" {
		t.Errorf("theme %q, label %q", page.Theme, page.ModeLabel)
	}
	page = showPage(t, pageScenario{Width: 1280, DefaultMode: "dark", Stored: map[string]string{"stage-hall-mode": "neon"}, View: pageView(pageStage("fix", 0))})
	if page.Theme != "" {
		t.Errorf("an unknown stored mode was applied: %q", page.Theme)
	}
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

func TestTheGraphDrawsInAWindowNarrowerThanTheTableNeeds(t *testing.T) {
	page := showPage(t, pageScenario{Width: 400, Hash: "view=graph&open=fix", View: pageView(pageStage("fix", 0))})
	if page.GraphHidden || !page.TableHidden || page.Pressed != "graph" || page.Pictures != 1 {
		t.Errorf("page %+v, want the graph of fix", page)
	}
}

func TestTheGraphSaysHowWideAWindowItNeedsWhenTheWindowIsTooNarrow(t *testing.T) {
	page := showPage(t, pageScenario{Width: 320, Hash: "view=graph&open=fix", View: pageView(pageStage("fix", 0))})
	if page.GraphHidden || page.Pictures != 0 || page.Pressed != "graph" {
		t.Fatalf("page %+v, want a message in place of the graph", page)
	}
	text := strings.Join(page.GraphText, " ")
	if !regexp.MustCompile(`needs a window at least [0-9]+ px wide`).MatchString(text) {
		t.Errorf("graph says %q, want how wide a window it needs", text)
	}
	if page.TableHidden {
		t.Errorf("the table is hidden while the graph cannot be drawn")
	}
}

func TestWithNoStageExpandedTheGraphDrawsEveryStageRunningAJob(t *testing.T) {
	page := showPage(t, pageScenario{Width: 1280, Click: "graph", View: pageView(pageStage("docs", 0), pageStage("fix", 1))})
	if page.GraphHidden || !slices.Equal(page.Drawn, []string{"fix"}) || page.Pictures != 1 {
		t.Fatalf("page %+v, want the graph of fix alone", page)
	}
	if text := strings.Join(page.GraphText, " "); !strings.Contains(text, "running job") {
		t.Errorf("graph says %q, want why these stages are drawn", text)
	}
}

func TestAnExpandedStageIsDrawnInsteadOfTheStagesRunningJobs(t *testing.T) {
	page := showPage(t, pageScenario{Width: 1280, Hash: "view=graph&open=docs", View: pageView(pageStage("docs", 0), pageStage("fix", 1))})
	if !slices.Equal(page.Drawn, []string{"docs"}) || page.Pictures != 1 {
		t.Errorf("page %+v, want the graph of docs alone", page)
	}
}

func TestWithNoStageExpandedOrRunningAJobTheGraphSaysHowToPickOne(t *testing.T) {
	page := showPage(t, pageScenario{Width: 1280, Click: "graph", View: pageView(pageStage("docs", 0), pageStage("fix", 0))})
	if page.GraphHidden || page.Pictures != 0 {
		t.Fatalf("page %+v, want a message in place of the graph", page)
	}
	text := strings.Join(page.GraphText, " ")
	for _, want := range []string{"No stage is running a job", "Expand a stage in the table"} {
		if !strings.Contains(text, want) {
			t.Errorf("graph says %q, want %q", text, want)
		}
	}
}

func TestAnExpandedRunningStageShowsHowToAttachToEachCAOTerminal(t *testing.T) {
	page := showPage(t, pageScenario{Width: 1280, Hash: "open=fix", View: pageView(pageStage("fix", 0))})
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
	page := showPage(t, pageScenario{Width: 1280, Hash: "open=fix", View: pageView(stopped)})
	if len(page.Code) != 0 {
		t.Errorf("commands %q on a stopped stage", page.Code)
	}
}
