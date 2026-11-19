package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	activityFormat      = "stage-activity/1"
	activityFormatName  = "stage-activity/"
	maxActivities       = 16
	maxSteps            = 64
	maxGroups           = 32
	maxMembers          = 64
	maxProviderDocument = 256 << 10
)

var (
	activityID          = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	sessionRefID        = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	currencyCode        = regexp.MustCompile(`^[A-Z]{3}$`)
	activityStatuses    = []string{"running", "waiting", "blocked", "succeeded", "failed", "stopped", "unknown"}
	stepStates          = []string{"pending", "active", "done", "failed", "skipped"}
	memberStates        = []string{"working", "waiting", "idle", "exited", "unknown"}
	sessionSources      = []string{"cao", "job"}
	errNotStageActivity = errors.New("it is not a " + activityFormat + " document")
)

type activityRecord struct {
	Provider  string            `json:"provider"`
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	RawStatus string            `json:"raw_status,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Round     *activityRound    `json:"round,omitempty"`
	StartedAt string            `json:"started_at,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	CAOPort   int               `json:"cao_port,omitempty"`
	Steps     []activityStep    `json:"steps,omitempty"`
	Groups    []activityGroup   `json:"groups,omitempty"`
	Counters  *activityCounters `json:"counters,omitempty"`
}

type activityRound struct {
	Current int  `json:"current"`
	Max     *int `json:"max,omitempty"`
}

type activityStep struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	RawState string `json:"raw_state,omitempty"`
}

type activityGroup struct {
	Name    string           `json:"name"`
	Members []activityMember `json:"members"`
}

type activityMember struct {
	Name     string      `json:"name"`
	Agent    string      `json:"agent,omitempty"`
	State    string      `json:"state"`
	RawState string      `json:"raw_state,omitempty"`
	Session  *sessionRef `json:"session,omitempty"`
}

type sessionRef struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

type activityCounters struct {
	Tokens         *counter `json:"tokens,omitempty"`
	Cost           *counter `json:"cost,omitempty"`
	ElapsedSeconds *counter `json:"elapsed_seconds,omitempty"`
}

type counter struct {
	Value    float64 `json:"value"`
	AtLeast  bool    `json:"at_least"`
	Currency string  `json:"currency,omitempty"`
}

type providerError struct {
	Provider string `json:"provider"`
	Error    string `json:"error"`
}

type counterInput struct {
	Value    *float64 `json:"value"`
	AtLeast  *bool    `json:"at_least"`
	Currency *string  `json:"currency"`
}

type activityInput struct {
	ID        *string `json:"id"`
	Title     *string `json:"title"`
	Status    *string `json:"status"`
	Reason    *string `json:"reason"`
	StartedAt *string `json:"started_at"`
	UpdatedAt *string `json:"updated_at"`
	CAOPort   *int    `json:"cao_port"`
	Round     *struct {
		Current *int `json:"current"`
		Max     *int `json:"max"`
	} `json:"round"`
	Steps *[]struct {
		Name  *string `json:"name"`
		State *string `json:"state"`
	} `json:"steps"`
	Groups *[]struct {
		Name    *string `json:"name"`
		Members *[]struct {
			Name    *string `json:"name"`
			Agent   *string `json:"agent"`
			State   *string `json:"state"`
			Session *struct {
				Source *string `json:"source"`
				ID     *string `json:"id"`
			} `json:"session"`
		} `json:"members"`
	} `json:"groups"`
	Counters *struct {
		Tokens         *counterInput `json:"tokens"`
		Cost           *counterInput `json:"cost"`
		ElapsedSeconds *counterInput `json:"elapsed_seconds"`
	} `json:"counters"`
}

func parseActivities(provider string, document []byte) ([]activityRecord, error) {
	if len(document) > maxProviderDocument {
		return nil, fmt.Errorf("it is over %d KiB", maxProviderDocument>>10)
	}
	if !utf8.Valid(document) {
		return nil, errors.New("it is not valid UTF-8")
	}
	var header struct {
		Format *string `json:"format"`
	}
	if err := json.Unmarshal(document, &header); err != nil || header.Format == nil {
		return nil, errNotStageActivity
	}
	if *header.Format != activityFormat {
		if version, ok := strings.CutPrefix(*header.Format, activityFormatName); ok {
			return nil, fmt.Errorf("it speaks %s%s, and this stage understands %s", activityFormatName, shortPrintable(version, 20), activityFormat)
		}
		return nil, errNotStageActivity
	}
	var input struct {
		Activities *[]activityInput `json:"activities"`
	}
	if err := json.Unmarshal(document, &input); err != nil {
		return nil, fmt.Errorf("it does not have the shape of %s: %s", activityFormat, shortPrintable(err.Error(), 200))
	}
	if input.Activities == nil {
		return nil, errors.New("it has no activities")
	}
	if len(*input.Activities) > maxActivities {
		return nil, fmt.Errorf("it has more than %d activities", maxActivities)
	}
	activities := []activityRecord{}
	var ids []string
	for index, activity := range *input.Activities {
		record, err := checkActivity(activity)
		if err != nil {
			return nil, fmt.Errorf("activity %d %s", index, err)
		}
		if slices.Contains(ids, record.ID) {
			return nil, fmt.Errorf("activity %d repeats the id %s", index, record.ID)
		}
		ids = append(ids, record.ID)
		record.Provider = provider
		activities = append(activities, record)
	}
	return activities, nil
}

func checkActivity(input activityInput) (activityRecord, error) {
	var record activityRecord
	if input.ID == nil || !activityID.MatchString(*input.ID) {
		return record, fmt.Errorf("needs an id of 1 to 128 letters, digits and ._:-")
	}
	record.ID = *input.ID
	var err error
	if record.Title, err = requiredText("title", input.Title, 200); err != nil {
		return record, err
	}
	if input.Status == nil {
		return record, errors.New("needs a status")
	}
	record.Status, record.RawStatus = known(*input.Status, activityStatuses)
	if record.Reason, err = optionalText("reason", input.Reason, 500); err != nil {
		return record, err
	}
	for _, stamp := range []struct {
		name   string
		value  *string
		target *string
	}{{"started_at", input.StartedAt, &record.StartedAt}, {"updated_at", input.UpdatedAt, &record.UpdatedAt}} {
		if stamp.value == nil {
			continue
		}
		if _, err := time.Parse(time.RFC3339, *stamp.value); err != nil {
			return record, fmt.Errorf("has a %s that is not an RFC 3339 time", stamp.name)
		}
		*stamp.target = *stamp.value
	}
	if input.CAOPort != nil {
		if *input.CAOPort < 1 || *input.CAOPort > 65535 {
			return record, errors.New("has a cao_port outside 1 to 65535")
		}
		record.CAOPort = *input.CAOPort
	}
	if input.Round != nil {
		if input.Round.Current == nil || *input.Round.Current < 0 || input.Round.Max != nil && *input.Round.Max < *input.Round.Current {
			return record, errors.New("has a round that needs a current of 0 or more and a max no smaller")
		}
		record.Round = &activityRound{Current: *input.Round.Current, Max: input.Round.Max}
	}
	if input.Steps != nil {
		if len(*input.Steps) > maxSteps {
			return record, fmt.Errorf("has more than %d steps", maxSteps)
		}
		for _, step := range *input.Steps {
			name, err := requiredText("step name", step.Name, 100)
			if err != nil {
				return record, err
			}
			if step.State == nil {
				return record, errors.New("has a step with no state")
			}
			state, raw := known(*step.State, stepStates)
			record.Steps = append(record.Steps, activityStep{Name: name, State: state, RawState: raw})
		}
	}
	if record.Groups, err = checkGroups(input); err != nil {
		return record, err
	}
	if input.Counters != nil {
		record.Counters = &activityCounters{}
		for _, item := range []struct {
			name     string
			input    *counterInput
			target   **counter
			currency bool
		}{{"tokens", input.Counters.Tokens, &record.Counters.Tokens, false}, {"cost", input.Counters.Cost, &record.Counters.Cost, true}, {"elapsed_seconds", input.Counters.ElapsedSeconds, &record.Counters.ElapsedSeconds, false}} {
			if item.input == nil {
				continue
			}
			if item.input.Value == nil || *item.input.Value < 0 {
				return record, fmt.Errorf("has a %s counter that needs a value of 0 or more", item.name)
			}
			value := &counter{Value: *item.input.Value, AtLeast: item.input.AtLeast != nil && *item.input.AtLeast}
			if item.currency {
				if item.input.Currency == nil || !currencyCode.MatchString(*item.input.Currency) {
					return record, errors.New("has a cost that needs a three-letter currency")
				}
				value.Currency = *item.input.Currency
			}
			*item.target = value
		}
	}
	return record, nil
}

func checkGroups(input activityInput) ([]activityGroup, error) {
	if input.Groups == nil {
		return nil, nil
	}
	if len(*input.Groups) > maxGroups {
		return nil, fmt.Errorf("has more than %d groups", maxGroups)
	}
	var groups []activityGroup
	for _, group := range *input.Groups {
		name, err := requiredText("group name", group.Name, 100)
		if err != nil {
			return nil, err
		}
		checked := activityGroup{Name: name, Members: []activityMember{}}
		if group.Members != nil {
			if len(*group.Members) > maxMembers {
				return nil, fmt.Errorf("has a group with more than %d members", maxMembers)
			}
			for _, member := range *group.Members {
				name, err := requiredText("member name", member.Name, 100)
				if err != nil {
					return nil, err
				}
				agent, err := optionalText("member agent", member.Agent, 64)
				if err != nil {
					return nil, err
				}
				if member.State == nil {
					return nil, errors.New("has a member with no state")
				}
				state, raw := known(*member.State, memberStates)
				checkedMember := activityMember{Name: name, Agent: agent, State: state, RawState: raw}
				if member.Session != nil {
					if member.Session.Source == nil || !slices.Contains(sessionSources, *member.Session.Source) || member.Session.ID == nil || !sessionRefID.MatchString(*member.Session.ID) {
						return nil, errors.New("has a member whose session needs a source of cao or job and an id")
					}
					checkedMember.Session = &sessionRef{Source: *member.Session.Source, ID: *member.Session.ID}
				}
				checked.Members = append(checked.Members, checkedMember)
			}
		}
		groups = append(groups, checked)
	}
	return groups, nil
}

func requiredText(name string, value *string, limit int) (string, error) {
	if value == nil || *value == "" {
		return "", fmt.Errorf("needs a %s", name)
	}
	return optionalText(name, value, limit)
}

func optionalText(name string, value *string, limit int) (string, error) {
	if value == nil {
		return "", nil
	}
	if utf8.RuneCountInString(*value) > limit {
		return "", fmt.Errorf("has a %s over %d characters", name, limit)
	}
	return shortPrintable(*value, 4*limit), nil
}

func known(value string, allowed []string) (string, string) {
	if slices.Contains(allowed, value) {
		return value, ""
	}
	return "unknown", shortPrintable(value, 64)
}
