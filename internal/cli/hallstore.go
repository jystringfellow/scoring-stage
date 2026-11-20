package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
	"time"
)

const (
	hallViewFile   = ".hall-view.json"
	hallViewFormat = "stage-hall-view/1"
	maxHallView    = 1 << 20
)

var savedProviderName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type savedSession struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Server string `json:"server,omitempty"`
	Name   string `json:"name,omitempty"`
	Agent  string `json:"agent,omitempty"`
	State  string `json:"state"`
}

type savedView struct {
	Format     string            `json:"format"`
	Slug       string            `json:"slug"`
	SavedAt    string            `json:"saved_at"`
	ReadAt     string            `json:"read_at,omitempty"`
	Repository string            `json:"repository,omitempty"`
	BaseBranch string            `json:"base_branch,omitempty"`
	Sessions   []savedSession    `json:"sessions"`
	Activities []activityRecord  `json:"activities"`
	Earlier    []earlierActivity `json:"earlier"`
}

func saveHallView(home string, record stageRecord, readAt time.Time, earlier []earlierActivity, now time.Time) error {
	view := savedView{
		Format: hallViewFormat, Slug: record.Slug, SavedAt: now.UTC().Format(time.RFC3339), ReadAt: readAt.UTC().Format(time.RFC3339),
		Repository: record.Repository, BaseBranch: record.BaseBranch, Sessions: []savedSession{},
		Activities: record.Activities, Earlier: earlier,
	}
	for _, session := range record.Sessions.Items {
		if session.Source == "cao" {
			view.Sessions = append(view.Sessions, savedSession{Source: session.Source, ID: session.ID, Server: session.Server, Name: session.Name, Agent: session.Agent, State: session.State})
		}
	}
	data, err := json.Marshal(view)
	if err == nil && len(data) > maxHallView {
		view.Earlier = nil
		data, err = json.Marshal(view)
	}
	if err != nil {
		return err
	}
	if len(data) > maxHallView {
		return errors.New("the hall's view of the stage is too large to keep")
	}
	dir := takesDir(home, record.Slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	partial, err := os.CreateTemp(dir, ".hall-view-*")
	if err != nil {
		return err
	}
	defer os.Remove(partial.Name())
	if _, err := partial.Write(data); err != nil {
		partial.Close()
		return err
	}
	if err := partial.Close(); err != nil {
		return err
	}
	return os.Rename(partial.Name(), filepath.Join(dir, hallViewFile))
}

func loadHallView(home, slug string, keep time.Duration, now time.Time) (savedView, bool) {
	if _, err := checkSlug(slug); err != nil {
		return savedView{}, false
	}
	path := filepath.Join(takesDir(home, slug), hallViewFile)
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return savedView{}, false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return savedView{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxHallView+1))
	file.Close()
	var view savedView
	if err != nil || len(data) > maxHallView || json.Unmarshal(data, &view) != nil || view.Format != hallViewFormat || view.Slug != slug {
		return savedView{}, false
	}
	saved, err := time.Parse(time.RFC3339, view.SavedAt)
	if err != nil || keep <= 0 || now.Sub(saved) > keep {
		os.Remove(path)
		return savedView{}, false
	}
	return checkSavedView(view), true
}

func checkSavedView(view savedView) savedView {
	checked := savedView{Format: view.Format, Slug: view.Slug, SavedAt: view.SavedAt, Sessions: []savedSession{}, Activities: []activityRecord{}, Earlier: []earlierActivity{}}
	if _, err := time.Parse(time.RFC3339, view.ReadAt); err == nil {
		checked.ReadAt = view.ReadAt
	}
	checked.Repository, checked.BaseBranch = shortPrintable(view.Repository, 200), shortPrintable(view.BaseBranch, 200)
	for _, session := range view.Sessions[:min(len(view.Sessions), 256)] {
		if !slices.Contains(sessionSources, session.Source) || !sessionRefID.MatchString(session.ID) {
			continue
		}
		state, _ := known(session.State, memberStates)
		checked.Sessions = append(checked.Sessions, savedSession{Source: session.Source, ID: session.ID, Server: shortPrintable(session.Server, 64),
			Name: shortPrintable(session.Name, 100), Agent: shortPrintable(session.Agent, 64), State: state})
	}
	for _, activity := range view.Activities[:min(len(view.Activities), maxActivities)] {
		if valid, ok := recheckActivity(activity); ok {
			checked.Activities = append(checked.Activities, valid)
		}
	}
	for _, earlier := range view.Earlier[:min(len(view.Earlier), maxEarlierActivities)] {
		valid, ok := recheckActivity(earlier.Activity)
		if _, err := time.Parse(time.RFC3339, earlier.LastSeenAt); ok && err == nil {
			checked.Earlier = append(checked.Earlier, earlierActivity{Activity: valid, LastSeenAt: earlier.LastSeenAt})
		}
	}
	return checked
}

func recheckActivity(activity activityRecord) (activityRecord, bool) {
	if !savedProviderName.MatchString(activity.Provider) {
		return activityRecord{}, false
	}
	document, err := json.Marshal(struct {
		Format     string           `json:"format"`
		Activities []activityRecord `json:"activities"`
	}{activityFormat, []activityRecord{activity}})
	if err != nil {
		return activityRecord{}, false
	}
	checked, err := parseActivities(activity.Provider, document)
	if err != nil || len(checked) != 1 {
		return activityRecord{}, false
	}
	return checked[0], true
}

func savedSlugs(home string) []string {
	dirs, err := os.ReadDir(filepath.Join(home, "takes"))
	if err != nil {
		return nil
	}
	var slugs []string
	for _, dir := range dirs {
		if _, err := checkSlug(dir.Name()); err != nil || !dir.IsDir() {
			continue
		}
		if info, err := os.Lstat(filepath.Join(home, "takes", dir.Name(), hallViewFile)); err == nil && info.Mode().IsRegular() {
			slugs = append(slugs, dir.Name())
		}
	}
	return slugs
}

func (view savedView) sessions(jobs []sessionRecord) sessionsRecord {
	sessions := sessionsRecord{Status: "ok", Items: []sessionRecord{}}
	for _, saved := range view.Sessions {
		sessions.Items = append(sessions.Items, sessionRecord{Source: saved.Source, ID: saved.ID, Server: saved.Server, Name: saved.Name, Agent: saved.Agent, State: saved.State})
	}
	sessions.Items = append(sessions.Items, jobs...)
	return sessions
}
