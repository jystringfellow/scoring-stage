package cli

import (
	"slices"
	"sort"
	"strings"
	"time"
)

const maxFinishedActivities = 64

var finishedStatuses = []string{"succeeded", "failed", "stopped"}

type scoreRow struct {
	Repository    string  `json:"repository"`
	Runs          int     `json:"runs"`
	Succeeded     int     `json:"succeeded"`
	Failed        int     `json:"failed"`
	Stopped       int     `json:"stopped"`
	Tokens        float64 `json:"tokens"`
	TokensAtLeast bool    `json:"tokens_at_least"`
}

func keepFinished(kept []earlierActivity, reading stageReading, keep time.Duration, now time.Time) []earlierActivity {
	key := func(a activityRecord) string { return a.Provider + " " + a.ID }
	seenAt := reading.at.UTC().Format(time.RFC3339)
	for _, activity := range reading.read.activities {
		if !slices.Contains(finishedStatuses, activity.Status) {
			continue
		}
		kept = slices.DeleteFunc(kept, func(e earlierActivity) bool { return key(e.Activity) == key(activity) })
		kept = append(kept, earlierActivity{Activity: activity, LastSeenAt: seenAt})
	}
	kept = slices.DeleteFunc(kept, func(e earlierActivity) bool {
		seen, err := time.Parse(time.RFC3339, e.LastSeenAt)
		return err != nil || keep > 0 && now.Sub(seen) > keep
	})
	return kept[max(len(kept)-maxFinishedActivities, 0):]
}

func scoreboard(stages []hallStage, finished map[string][]earlierActivity) []scoreRow {
	rows := map[string]*scoreRow{}
	for _, stage := range stages {
		runs := finished[stage.Slug]
		if len(runs) == 0 {
			continue
		}
		repository := stage.Repository
		if repository == "" {
			repository = "-"
		}
		row := rows[repository]
		if row == nil {
			row = &scoreRow{Repository: repository}
			rows[repository] = row
		}
		for _, run := range runs {
			row.Runs++
			switch run.Activity.Status {
			case "succeeded":
				row.Succeeded++
			case "failed":
				row.Failed++
			case "stopped":
				row.Stopped++
			}
			if counters := run.Activity.Counters; counters != nil && counters.Tokens != nil {
				row.Tokens += counters.Tokens.Value
				row.TokensAtLeast = row.TokensAtLeast || counters.Tokens.AtLeast
			} else {
				row.TokensAtLeast = true
			}
		}
	}
	board := []scoreRow{}
	for _, row := range rows {
		board = append(board, *row)
	}
	sort.Slice(board, func(i, j int) bool { return strings.Compare(board[i].Repository, board[j].Repository) < 0 })
	return board
}
