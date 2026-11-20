package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func viewPath(h *harness, slug string) string {
	return filepath.Join(takesDir(h.home, slug), hallViewFile)
}

func TestTheHallKeepsAStagesLastViewOnTheHostAfterTheStageIsGone(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[{"id":"a","title":"Fix the login redirect","status":"running"}]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	hall := startHall(t, h)
	eventually(t, func() bool { _, err := os.Stat(viewPath(h, "fix")); return err == nil })
	hall.stop()
	saved := testutil.ReadFile(t, viewPath(h, "fix"))
	if strings.Contains(saved, "first line") || !strings.Contains(saved, "Fix the login redirect") {
		t.Errorf("saved view %s, want the activity and no terminal lines", saved)
	}
	if info, err := os.Stat(viewPath(h, "fix")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("saved view mode %v, %v", info.Mode(), err)
	}

	if err := os.RemoveAll(h.task("fix")); err != nil {
		t.Fatal(err)
	}
	h.sbx.SetSandboxes("")
	gone := stageInHall(t, startHall(t, h), "fix")
	if gone.State != "gone" || gone.Remembered == "" || gone.Sessions != 4 || !slices.Equal(gone.Activities, []string{"a=running"}) || len(gone.Attention) != 0 {
		t.Errorf("gone stage %+v, want its saved view, marked and asking nothing", gone)
	}
	if count := countTakes(h.home, "fix").Count; count != 0 {
		t.Errorf("the saved view counts as %d takes", count)
	}
}

func TestAStoppedStageShowsItsSavedViewAfterTheHallRestarts(t *testing.T) {
	h := readyToRead(t)
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":"running"}]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	hall := startHall(t, h)
	eventually(t, func() bool { _, err := os.Stat(viewPath(h, "fix")); return err == nil })
	hall.stop()
	h.sbx.SetSandboxes("")
	h.sbx.SetStopped("stage-fix\n")
	stopped := stageInHall(t, startHall(t, h), "fix")
	if stopped.State != "stopped" || stopped.Remembered == "" || !slices.Equal(stopped.Activities, []string{"a=running"}) {
		t.Errorf("stopped stage %+v", stopped)
	}
}

func writeView(t *testing.T, h *harness, slug string, view map[string]any) {
	t.Helper()
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(takesDir(h.home, slug), hallViewFile), string(data))
}

func TestASavedViewOlderThanTheKeepIsRemovedAndNotShown(t *testing.T) {
	h := readyToRead(t)
	writeView(t, h, "old", map[string]any{"format": hallViewFormat, "slug": "old", "saved_at": time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)})
	writeView(t, h, "recent", map[string]any{"format": hallViewFormat, "slug": "recent", "saved_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	var slugs []string
	for _, stage := range readHall(t, startHall(t, h)).Stages {
		slugs = append(slugs, stage.Slug)
	}
	if slices.Contains(slugs, "old") || !slices.Contains(slugs, "recent") {
		t.Errorf("stages %v, want recent shown and old not", slugs)
	}
	if _, err := os.Stat(viewPath(h, "old")); !os.IsNotExist(err) {
		t.Errorf("the old view is still on disk: %v", err)
	}
}

func TestASavedViewIsCheckedAgainWhenReadBack(t *testing.T) {
	h := readyToRead(t)
	now := time.Now().UTC().Format(time.RFC3339)
	writeView(t, h, "evil", map[string]any{
		"format": hallViewFormat, "slug": "evil", "saved_at": now, "read_at": now, "repository": "acme/\x1b[2Japi",
		"sessions": []map[string]any{{"source": "cao", "id": "1a2b3c4d", "name": "\x1b]0;owned\x07", "state": "pwned"}, {"source": "web", "id": "x"}},
		"activities": []map[string]any{
			{"provider": "loop", "id": "fine", "title": "ok", "status": "running"},
			{"provider": "loop", "id": "a b", "title": "bad id", "status": "running"},
			{"provider": "Not A Name", "id": "p", "title": "bad provider", "status": "running"},
		},
	})
	writeView(t, h, "other", map[string]any{"format": hallViewFormat, "slug": "not-other", "saved_at": now})
	testutil.WriteFile(t, filepath.Join(takesDir(h.home, "huge"), hallViewFile), `{"format":"`+hallViewFormat+`","slug":"huge","saved_at":"`+now+`","repository":"`+strings.Repeat("x", 2<<20)+`"}`)
	snapshot := readHall(t, startHall(t, h))
	var evil *hallStageJSON
	for i, stage := range snapshot.Stages {
		switch stage.Slug {
		case "evil":
			evil = &snapshot.Stages[i]
		case "other", "not-other", "huge":
			t.Errorf("a view that should be refused was shown: %s", stage.Slug)
		}
	}
	if evil == nil {
		t.Fatalf("stages %+v", snapshot.Stages)
	}
	if strings.ContainsRune(evil.Repository, 0x1b) || len(evil.Sessions.Items) != 1 || strings.ContainsRune(evil.Sessions.Items[0].Name, 0x1b) || evil.Sessions.Items[0].State != "unknown" {
		t.Errorf("evil read back as %+v", evil.stageRecord)
	}
	if len(evil.Activities) != 1 || evil.Activities[0].ID != "fine" {
		t.Errorf("activities %+v, want only the valid one", evil.Activities)
	}
}

func TestKeepZeroSavesNothing(t *testing.T) {
	h := readyToRead(t)
	userConfig(t, h, "[hall]\nkeep = \"0\"\n")
	quickHall(t, 100*time.Millisecond, 200*time.Millisecond, time.Hour)
	hall := startHall(t, h)
	time.Sleep(600 * time.Millisecond)
	hall.stop()
	if _, err := os.Stat(viewPath(h, "fix")); !os.IsNotExist(err) {
		t.Errorf("a view was saved with keep = 0: %v", err)
	}
}
