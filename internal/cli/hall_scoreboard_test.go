package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func finishedDocument(activities string) string {
	return `{"format":"stage-activity/1","activities":[` + activities + `]}`
}

func TestTheScoreboardCountsFinishedRunsByRepository(t *testing.T) {
	h := readyToRead(t)
	h.expect(0, "up", h.remote.Checkout, "other")
	h.sbx.SetSandboxes("stage-fix\nstage-other\n")
	printsDocument(t, "loop", finishedDocument(`
		{"id":"done","title":"t","status":"succeeded","counters":{"tokens":{"value":1000}}},
		{"id":"broke","title":"t","status":"failed","counters":{"tokens":{"value":500,"at_least":true}}},
		{"id":"halted","title":"t","status":"stopped"},
		{"id":"live","title":"t","status":"running","counters":{"tokens":{"value":7}}}`))
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	board := readHall(t, startHall(t, h)).Scoreboard
	if len(board) != 1 {
		t.Fatalf("scoreboard %+v", board)
	}
	row := board[0]
	if row.Repository != "acme/api" || row.Runs != 6 || row.Succeeded != 2 || row.Failed != 2 || row.Stopped != 2 || row.Tokens != 3000 || !row.TokensAtLeast {
		t.Errorf("row %+v, want both stages' finished runs and not the live ones", row)
	}
}

func TestFinishedRunsStayOnTheScoreboardAfterTheyAreNoLongerReported(t *testing.T) {
	h := readyToRead(t)
	document := filepath.Join(t.TempDir(), "activity.json")
	testutil.WriteFile(t, document, finishedDocument(`{"id":"done","title":"t","status":"succeeded","counters":{"tokens":{"value":1000}}}`))
	providerProgram(t, "loop", "cat '"+document+"'\n")
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	quickHall(t, 100*time.Millisecond, 300*time.Millisecond, time.Hour)
	hall := startHall(t, h)
	testutil.WriteFile(t, document, finishedDocument(""))
	time.Sleep(time.Second)
	if board := readHall(t, hall).Scoreboard; len(board) != 1 || board[0].Runs != 1 {
		t.Errorf("scoreboard %+v once the run was no longer reported", board)
	}
	hall.stop()
	if err := os.RemoveAll(h.task("fix")); err != nil {
		t.Fatal(err)
	}
	h.sbx.SetSandboxes("")
	if board := readHall(t, startHall(t, h)).Scoreboard; len(board) != 1 || board[0].Runs != 1 || board[0].Succeeded != 1 {
		t.Errorf("scoreboard %+v after the stage and the hall were gone", board)
	}
}
