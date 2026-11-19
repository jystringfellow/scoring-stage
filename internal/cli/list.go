package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const listFormat = "stage-list/1"

type stageListing struct {
	Format string        `json:"format"`
	ReadAt string        `json:"read_at"`
	Stages []stageRecord `json:"stages"`
}

type stageRecord struct {
	Slug        string         `json:"slug"`
	Sandbox     string         `json:"sandbox"`
	State       string         `json:"state"`
	Repository  string         `json:"repository,omitempty"`
	BaseBranch  string         `json:"base_branch,omitempty"`
	Agents      []string       `json:"agents,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
	LastUsedAt  string         `json:"last_used_at,omitempty"`
	Disk        []diskUse      `json:"disk,omitempty"`
	Read        *readRecord    `json:"read,omitempty"`
	Sessions    sessionsRecord `json:"sessions"`
	Takes       takesRecord    `json:"takes"`
	Ships       []Ship         `json:"ships"`
	RunningJobs int            `json:"running_jobs"`
	TaskError   string         `json:"task_error,omitempty"`
}

type takesRecord struct {
	Count  int    `json:"count"`
	Latest string `json:"latest,omitempty"`
}

type stageEntry struct {
	record stageRecord
	task   *Task
	jobs   []sessionRecord
}

func (a *App) cmdList(args []string, home string) (int, error) {
	fs := a.newFlags("list", "[--json] [--shallow]")
	asJSON := fs.Bool("json", false, "print every stage as JSON")
	shallow := fs.Bool("shallow", false, "read only what the host holds and run nothing inside a sandbox")
	if _, err := fs.parse(args); err != nil {
		return 0, err
	}
	listed, err := sbx.List()
	if err != nil {
		return 0, err
	}
	entries, err := collectStages(home, listed)
	if err != nil {
		return 0, err
	}
	if !*shallow {
		a.readStages(entries)
	}
	if *asJSON {
		listing := stageListing{Format: listFormat, ReadAt: time.Now().UTC().Format(time.RFC3339), Stages: []stageRecord{}}
		for _, entry := range entries {
			listing.Stages = append(listing.Stages, entry.record)
		}
		return 0, json.NewEncoder(a.Stdout).Encode(listing)
	}
	return 0, a.printStages(entries, time.Now())
}

func collectStages(home string, listed []sbx.Listed) ([]*stageEntry, error) {
	sandboxes := map[string]sbx.Listed{}
	slugs := map[string]bool{}
	for _, sandbox := range listed {
		if slug, ok := strings.CutPrefix(sandbox.Name, sandboxPrefix); ok {
			sandboxes[slug] = sandbox
			slugs[slug] = true
		}
	}
	recorded, err := recordedSlugs(home)
	if err != nil {
		return nil, err
	}
	for _, slug := range recorded {
		slugs[slug] = true
	}
	var entries []*stageEntry
	for slug := range slugs {
		entries = append(entries, describeStage(home, slug, sandboxes))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].record.Slug < entries[j].record.Slug })
	return entries, nil
}

func recordedSlugs(home string) ([]string, error) {
	dirs, err := os.ReadDir(filepath.Join(home, "tasks"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var slugs []string
	for _, dir := range dirs {
		if !dir.IsDir() || !slugPattern.MatchString(dir.Name()) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(taskDir(home, dir.Name()), "task.json")); err == nil {
			slugs = append(slugs, dir.Name())
		}
	}
	return slugs, nil
}

func describeStage(home, slug string, sandboxes map[string]sbx.Listed) *stageEntry {
	entry := &stageEntry{record: stageRecord{Slug: config.Printable(slug), Sandbox: config.Printable(sandboxOf(slug)), Ships: []Ship{},
		Sessions: sessionsRecord{Status: "not_read", Items: []sessionRecord{}}}}
	record := &entry.record
	_, recordErr := os.Lstat(filepath.Join(taskDir(home, slug), "task.json"))
	hasRecord := recordErr == nil && slugPattern.MatchString(slug)
	if hasRecord {
		task, err := loadTask(home, slug)
		if err != nil {
			record.TaskError = config.Printable(err.Error())
		} else {
			record.Repository, record.BaseBranch = config.Printable(task.GitHubRepo), config.Printable(task.BaseBranch)
			for _, agent := range task.Agents {
				record.Agents = append(record.Agents, config.Printable(agent))
			}
			if len(task.Ships) > 0 {
				record.Ships = task.Ships
			}
			record.RunningJobs = runningJobs(home, slug)
			entry.jobs = jobSessions(home, slug)
			record.Sessions.Items = append(record.Sessions.Items, entry.jobs...)
			if task.Sandbox == sandboxOf(slug) {
				entry.task = task
			}
		}
		record.Takes = countTakes(home, slug)
	}
	sandbox, exists := sandboxes[slug]
	switch {
	case exists && !hasRecord:
		record.State = "no_task"
	case exists:
		record.State = config.Printable(sandbox.Status)
	default:
		record.State = "gone"
	}
	if exists {
		record.CreatedAt, record.LastUsedAt = config.Printable(sandbox.CreatedAt), config.Printable(sandbox.LastUsedAt)
	}
	return entry
}

func runningJobs(home, slug string) int {
	jobs, err := listJobs(home, slug)
	if err != nil {
		return 0
	}
	running := 0
	for _, job := range jobs {
		if job.State == "running" {
			running++
		}
	}
	return running
}

func countTakes(home, slug string) takesRecord {
	var kept takesRecord
	dirs, err := os.ReadDir(takesDir(home, slug))
	if err != nil {
		return kept
	}
	for _, dir := range dirs {
		if !dir.IsDir() || strings.HasPrefix(dir.Name(), ".") {
			continue
		}
		kept.Count++
		if name := config.Printable(dir.Name()); name > kept.Latest {
			kept.Latest = name
		}
	}
	return kept
}

func (a *App) readStages(entries []*stageEntry) {
	var asking sync.WaitGroup
	for _, entry := range entries {
		if entry.task == nil || entry.record.State != "running" {
			continue
		}
		asking.Add(1)
		go func() {
			defer asking.Done()
			record, read := readStage(entry.task, []int{defaultCAOPort})
			entry.record.Read, entry.record.Disk, entry.record.Sessions = record, read.disk, read.sessions
			entry.record.Sessions.Items = append(entry.record.Sessions.Items, entry.jobs...)
		}()
	}
	asking.Wait()
	for _, entry := range entries {
		if entry.task != nil {
			a.warnOfFullDisk(entry.task, entry.record.Disk)
		}
	}
}

func (a *App) printStages(entries []*stageEntry, now time.Time) error {
	table := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "SLUG\tSTATE\tREPOSITORY\tAGE\tDISK\tSESSIONS\tSHIPS")
	for _, entry := range entries {
		record := entry.record
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", record.Slug, strings.ReplaceAll(record.State, "_", " "),
			repositoryAt(record), age(record.CreatedAt, now), fullestDisk(record.Disk), sessionsSummary(record.Sessions), len(record.Ships))
	}
	return table.Flush()
}

func sessionsSummary(sessions sessionsRecord) string {
	if len(sessions.Items) == 0 {
		switch sessions.Status {
		case "ok":
			return "0"
		case "no_cao":
			return "no CAO"
		case "timed_out":
			return "timed out"
		case "unreadable":
			return "unreadable"
		}
		return "-"
	}
	counts := map[string]int{}
	for _, session := range sessions.Items {
		counts[session.State]++
	}
	var parts []string
	for _, state := range []string{"working", "waiting", "idle", "exited", "unknown"} {
		if counts[state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[state], state))
		}
	}
	return fmt.Sprintf("%d (%s)", len(sessions.Items), strings.Join(parts, ", "))
}

func repositoryAt(record stageRecord) string {
	switch {
	case record.Repository == "":
		return "-"
	case record.BaseBranch == "":
		return record.Repository
	}
	return record.Repository + "@" + record.BaseBranch
}

func age(createdAt string, now time.Time) string {
	created, err := time.Parse(time.RFC3339, createdAt)
	if err != nil || created.After(now) {
		return "-"
	}
	elapsed := now.Sub(created)
	switch {
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	}
	return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
}

func fullestDisk(use []diskUse) string {
	if len(use) == 0 {
		return "-"
	}
	fullest := 0
	for _, d := range use {
		fullest = max(fullest, d.UsedPercent)
	}
	return fmt.Sprintf("%d%%", fullest)
}
