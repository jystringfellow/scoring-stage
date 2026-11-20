package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"
)

const kmSchema = 1

var builtinProviders = map[string]stageProvider{
	"kapellmeister": {name: "kapellmeister", command: []string{"km", "status", "-json"}, translate: translateKmStatus},
}

var errKmUnreadable = errors.New("km status -json printed something stage cannot read")

type kmStatus struct {
	Runs []kmRun `json:"runs"`
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
	Name string `json:"name"`
	Step string `json:"step"`
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

func translateKmStatus(output []byte) ([]byte, error) {
	var status kmStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, errKmUnreadable
	}
	activities := []activityRecord{}
	for _, run := range status.Runs[:min(len(status.Runs), maxActivities)] {
		if run.Schema > kmSchema {
			return nil, fmt.Errorf("km status speaks schema %d, and this stage reads schema %d and earlier", run.Schema, kmSchema)
		}
		activity, err := translateKmRun(run)
		if err != nil {
			return nil, err
		}
		activities = append(activities, activity)
	}
	return json.Marshal(struct {
		Format     string           `json:"format"`
		Activities []activityRecord `json:"activities"`
	}{activityFormat, activities})
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
	for _, group := range ensemble.Sections {
		for _, chair := range group.Chairs {
			if chair.NeedsInput.Count > 0 && activity.Status == "running" {
				activity.Status, activity.Reason = "waiting", clip(kmChairName(chair)+" needs input", 100)
				if chair.NeedsInput.Reason != "" {
					activity.Reason = clip(activity.Reason+": "+chair.NeedsInput.Reason, 500)
				}
			}
		}
	}
	if run.Round > 0 || run.MaxRounds > 0 {
		activity.Round = &activityRound{Current: max(run.Round, 0)}
		if run.MaxRounds >= activity.Round.Current && run.MaxRounds > 0 {
			activity.Round.Max = &run.MaxRounds
		}
	}
	if usage := run.Usage; usage != nil && (usage.Measured == "all" || usage.Measured == "some") {
		activity.Counters = &activityCounters{Tokens: &counter{Value: usage.InputTokens + usage.OutputTokens, AtLeast: usage.Measured == "some"}}
	}
	return activity, nil
}

func kmSteps(run kmRun) []activityStep {
	var steps []activityStep
	if run.Score == nil {
		for _, playing := range run.Playing {
			if playing.Step != "" && !slices.ContainsFunc(steps, func(s activityStep) bool { return s.Name == playing.Step }) {
				steps = append(steps, activityStep{Name: clip(playing.Step, 100), State: "active"})
			}
		}
		return steps[:min(len(steps), maxSteps)]
	}
	stepped := func(list []kmStep, name string) bool {
		return slices.ContainsFunc(list, func(s kmStep) bool { return s.Step == name })
	}
	for _, step := range run.Score.Steps[:min(len(run.Score.Steps), maxSteps)] {
		if step.Name == "" {
			continue
		}
		state := "pending"
		switch {
		case stepped(run.Current, step.Name):
			state = "active"
		case stepped(run.Trail, step.Name):
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
	if _, err := time.Parse(time.RFC3339, text); err != nil {
		return ""
	}
	return text
}

func clip(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}
