package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	kmSchema              = 1
	kapellmeisterProvider = "kapellmeister"
	maxKmTokens           = 1e15
)

var builtinProviders = map[string]stageProvider{
	kapellmeisterProvider: {name: kapellmeisterProvider, command: []string{"km", "status", "-json"}, translate: translateKmStatus},
}

var errKmUnreadable = errors.New("km status -json printed something stage cannot read")

type kmStatus struct {
	Runs []json.RawMessage `json:"runs"`
}

type kmRun struct {
	Schema    int             `json:"schema"`
	RunID     string          `json:"run_id"`
	Command   string          `json:"command"`
	Liveness  string          `json:"liveness"`
	Started   string          `json:"started"`
	Updated   string          `json:"updated"`
	CAO       string          `json:"cao"`
	Ensemble  json.RawMessage `json:"ensemble"`
	Round     int             `json:"round"`
	MaxRounds int             `json:"max_rounds"`
	Usage     *kmUsage        `json:"usage"`
	Playing   []kmPlaying     `json:"playing"`
	Current   []kmStep        `json:"current"`
	Trail     []kmStep        `json:"trail"`
	Score     *struct {
		Steps []kmStep `json:"steps"`
	} `json:"score"`
}

type kmPlaying struct {
	Step  string `json:"step"`
	Chair string `json:"chair"`
}

type kmStep struct {
	Name  string `json:"name"`
	Step  string `json:"step"`
	Round int    `json:"round"`
}

type kmUsage struct {
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
	Measured     string  `json:"measured"`
}

type kmEnsemble struct {
	Name     string `json:"name"`
	Sections []struct {
		Name   string    `json:"name"`
		Chairs []kmChair `json:"chairs"`
	} `json:"sections"`
}

type kmChair struct {
	Address    string `json:"address"`
	Name       string `json:"name"`
	Agent      string `json:"agent"`
	State      string `json:"state"`
	Terminal   string `json:"terminal"`
	NeedsInput struct {
		Count  int    `json:"count"`
		Reason string `json:"reason"`
	} `json:"needs_input"`
}

func translateKmStatus(output []byte) ([]activityRecord, []string, error) {
	var status kmStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, nil, errKmUnreadable
	}
	var live, rest []activityRecord
	var notes []string
	seen := map[string]bool{}
	for index, raw := range status.Runs {
		activity, err := translateKmEntry(raw)
		if err == nil && seen[activity.ID] {
			err = fmt.Errorf("repeats the run %s", activity.ID)
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("km run %d left out: %s", index, err))
			continue
		}
		seen[activity.ID] = true
		if activity.Status == "running" || activity.Status == "waiting" {
			live = append(live, activity)
		} else {
			rest = append(rest, activity)
		}
	}
	activities := append(live, rest...)
	if len(activities) > maxActivities {
		notes = append(notes, fmt.Sprintf("%d more km runs were left out; stage shows %d, live ones first", len(activities)-maxActivities, maxActivities))
		activities = activities[:maxActivities]
	}
	if activities == nil {
		activities = []activityRecord{}
	}
	return activities, notes, nil
}

func translateKmEntry(raw json.RawMessage) (activityRecord, error) {
	if string(raw) == "null" || len(raw) == 0 || raw[0] != '{' {
		return activityRecord{}, errors.New("it is not a run")
	}
	var run kmRun
	if err := json.Unmarshal(raw, &run); err != nil {
		return activityRecord{}, errors.New("it does not have km's shape")
	}
	if run.Schema > kmSchema {
		return activityRecord{}, fmt.Errorf("km status speaks schema %d, and this stage reads schema %d and earlier", run.Schema, kmSchema)
	}
	activity, err := translateKmRun(run)
	if err != nil {
		return activityRecord{}, err
	}
	document, err := json.Marshal(struct {
		Format     string           `json:"format"`
		Activities []activityRecord `json:"activities"`
	}{activityFormat, []activityRecord{activity}})
	if err != nil {
		return activityRecord{}, errors.New("it does not have km's shape")
	}
	checked, err := parseActivities(kapellmeisterProvider, document)
	if err != nil {
		return activityRecord{}, errors.New(strings.TrimPrefix(err.Error(), "activity 0 "))
	}
	return checked[0], nil
}

func translateKmRun(run kmRun) (activityRecord, error) {
	var ensemble kmEnsemble
	if len(run.Ensemble) > 0 && run.Ensemble[0] == '"' {
		if err := json.Unmarshal(run.Ensemble, &ensemble.Name); err != nil {
			return activityRecord{}, errKmUnreadable
		}
	} else if len(run.Ensemble) > 0 && string(run.Ensemble) != "null" {
		if err := json.Unmarshal(run.Ensemble, &ensemble); err != nil {
			return activityRecord{}, errKmUnreadable
		}
	}
	title := "km"
	if run.Command != "" {
		title += " " + run.Command
	}
	if ensemble.Name != "" {
		title += " · " + ensemble.Name
	}
	activity := activityRecord{
		ID:        run.RunID,
		Title:     clip(title, 200),
		Status:    "unknown",
		StartedAt: rfc3339OrEmpty(run.Started),
		UpdatedAt: rfc3339OrEmpty(run.Updated),
		CAOPort:   loopbackPort(run.CAO),
		Steps:     kmSteps(run),
		Groups:    kmGroups(run, ensemble),
	}
	switch run.Liveness {
	case "alive":
		activity.Status = "running"
	case "abandoned":
		activity.Status, activity.Reason = "stopped", "km is no longer running this run"
	}
	var asking []kmChair
	for _, group := range ensemble.Sections {
		for _, chair := range group.Chairs {
			if chair.NeedsInput.Count > 0 {
				asking = append(asking, chair)
			}
		}
	}
	if len(asking) > 0 && activity.Status == "running" {
		first := asking[0]
		reason := clip(kmChairName(first), 100) + " needs input"
		if first.NeedsInput.Reason != "" {
			reason += ": " + first.NeedsInput.Reason
		}
		switch more := len(asking) - 1; {
		case more == 1:
			reason += "; 1 more chair needs input"
		case more > 1:
			reason += fmt.Sprintf("; %d more chairs need input", more)
		}
		activity.Status, activity.Reason = "waiting", clip(reason, 500)
	}
	if run.Round > 0 || run.MaxRounds > 0 {
		activity.Round = &activityRound{Current: max(run.Round, 0)}
		if run.MaxRounds >= activity.Round.Current && run.MaxRounds > 0 {
			activity.Round.Max = &run.MaxRounds
		}
	}
	if usage := run.Usage; usage != nil && (usage.Measured == "all" || usage.Measured == "some") {
		if total := usage.InputTokens + usage.OutputTokens; total >= 0 && total <= maxKmTokens {
			activity.Counters = &activityCounters{Tokens: &counter{Value: total, AtLeast: usage.Measured == "some"}}
		}
	}
	return activity, nil
}

func kmSteps(run kmRun) []activityStep {
	var steps []activityStep
	if run.Score == nil {
		for _, playing := range run.Playing {
			name := clip(playing.Step, 100)
			if name != "" && len(steps) < maxSteps && !slices.ContainsFunc(steps, func(s activityStep) bool { return s.Name == name }) {
				steps = append(steps, activityStep{Name: name, State: "active"})
			}
		}
		return steps
	}
	stepped := func(list []kmStep, name string) bool {
		return slices.ContainsFunc(list, func(s kmStep) bool { return s.Step == name })
	}
	doneThisRound := func(name string) bool {
		return slices.ContainsFunc(run.Trail, func(s kmStep) bool { return s.Step == name && (s.Round == run.Round || s.Round == 0) })
	}
	for _, step := range run.Score.Steps[:min(len(run.Score.Steps), maxSteps)] {
		if step.Name == "" {
			continue
		}
		state := "pending"
		switch {
		case stepped(run.Current, step.Name):
			state = "active"
		case doneThisRound(step.Name):
			state = "done"
		}
		steps = append(steps, activityStep{Name: clip(step.Name, 100), State: state})
	}
	return steps
}

func kmGroups(run kmRun, ensemble kmEnsemble) []activityGroup {
	var groups []activityGroup
	for _, section := range ensemble.Sections[:min(len(ensemble.Sections), maxGroups)] {
		group := activityGroup{Name: clip(section.Name, 100), Members: []activityMember{}}
		if group.Name == "" {
			group.Name = "chairs"
		}
		for _, chair := range section.Chairs[:min(len(section.Chairs), maxMembers)] {
			member := activityMember{Name: clip(kmChairName(chair), 100), Agent: clip(chair.Agent, 64), State: kmChairState(chair)}
			if caoTerminalID.MatchString(chair.Terminal) {
				member.Session = &sessionRef{Source: "cao", ID: chair.Terminal}
			}
			group.Members = append(group.Members, member)
		}
		groups = append(groups, group)
	}
	if len(groups) == 0 && len(run.Playing) > 0 {
		group := activityGroup{Name: "playing", Members: []activityMember{}}
		for _, playing := range run.Playing[:min(len(run.Playing), maxMembers)] {
			if playing.Chair != "" {
				group.Members = append(group.Members, activityMember{Name: clip(playing.Chair, 100), State: "working"})
			}
		}
		groups = append(groups, group)
	}
	return groups
}

func kmChairName(chair kmChair) string {
	if chair.Name != "" {
		return chair.Name
	}
	if chair.Address != "" {
		return chair.Address
	}
	return "chair"
}

func kmChairState(chair kmChair) string {
	if chair.NeedsInput.Count > 0 {
		return "waiting"
	}
	switch chair.State {
	case "playing":
		return "working"
	case "waiting":
		return "idle"
	case "done":
		return "exited"
	}
	return "unknown"
}

func loopbackPort(address string) int {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return 0
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

func rfc3339OrEmpty(text string) string {
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return ""
	}
	return parsed.Format(time.RFC3339)
}

func clip(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}
