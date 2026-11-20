package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func graphRecord() stageRecord {
	three := 3
	return stageRecord{
		Slug:  "fix",
		State: "running",
		Sessions: sessionsRecord{Status: "ok", Items: []sessionRecord{
			{Source: "cao", ID: "1a2b3c4d", State: "idle"},
			{Source: "cao", ID: "5e6f7a8b", State: "working"},
			{Source: "job", ID: "1", Name: "go test ./...", State: "working", Tail: []string{"ok"}},
		}},
		Activities: []activityRecord{{
			Provider: "loop", ID: "a", Title: "Fix the login redirect", Status: "running", Round: &activityRound{Current: 2, Max: &three},
			Steps: []activityStep{{Name: "plan", State: "done"}, {Name: "review", State: "active"}},
			Groups: []activityGroup{
				{Name: "builders", Members: []activityMember{{Name: "lead", Agent: "claude", State: "idle", Session: &sessionRef{Source: "cao", ID: "1a2b3c4d"}}}},
				{Name: "reviewers", Members: []activityMember{
					{Name: "security", Agent: "codex", State: "working", Session: &sessionRef{Source: "cao", ID: "5e6f7a8b"}},
					{Name: "style", State: "waiting"},
				}},
			},
		}},
	}
}

func TestTheGraphRunsFromStageToActivityGroupAndMember(t *testing.T) {
	graph := describeForHall(graphRecord(), time.Now()).Graph
	var nodes []string
	for _, node := range graph.Nodes {
		nodes = append(nodes, fmt.Sprintf("%s %s %q %q %s @%d,%d", node.ID, node.Kind, node.Label, node.Detail, node.State, node.Column, node.Row))
	}
	want := []string{
		`s stage "fix" "" running @0,0`,
		`a0 activity "Fix the login redirect" "review" running @1,0`,
		`a0g0 group "builders" ""  @2,0`,
		`a0g0m0 member "lead" "claude" idle @3,0`,
		`a0g1 group "reviewers" ""  @2,1`,
		`a0g1m0 member "security" "codex" working @3,1`,
		`a0g1m1 member "style" "" waiting @3,2`,
		`u group "sessions" ""  @2,3`,
		`u0 member "go test ./..." "job" working @3,3`,
	}
	if !slices.Equal(nodes, want) {
		t.Errorf("nodes\n%s\nwant\n%s", strings.Join(nodes, "\n"), strings.Join(want, "\n"))
	}
	var edges []string
	for _, edge := range graph.Edges {
		edges = append(edges, edge.From+">"+edge.To)
	}
	if want := []string{"s>a0", "a0>a0g0", "a0g0>a0g0m0", "a0>a0g1", "a0g1>a0g1m0", "a0g1>a0g1m1", "s>u", "u>u0"}; !slices.Equal(edges, want) {
		t.Errorf("edges %v, want %v", edges, want)
	}
	if graph.Columns != 4 || graph.Rows != 4 {
		t.Errorf("size %d×%d", graph.Columns, graph.Rows)
	}
	if member := graph.Nodes[3]; member.Session != "cao 1a2b3c4d" {
		t.Errorf("lead links to %q", member.Session)
	}
}

func TestTheGraphOfAStageIsBounded(t *testing.T) {
	record := stageRecord{Slug: "big", State: "running"}
	for a := range 16 {
		activity := activityRecord{ID: fmt.Sprint(a), Title: "t", Status: "running"}
		for range 32 {
			group := activityGroup{Name: "g"}
			for m := range 64 {
				group.Members = append(group.Members, activityMember{Name: fmt.Sprint(m), State: "idle"})
			}
			activity.Groups = append(activity.Groups, group)
		}
		record.Activities = append(record.Activities, activity)
	}
	graph := describeForHall(record, time.Now()).Graph
	if len(graph.Nodes) > maxGraphNodes+1 {
		t.Errorf("%d nodes", len(graph.Nodes))
	}
	last := graph.Nodes[len(graph.Nodes)-1]
	if last.Kind != "more" || !strings.HasPrefix(last.Label, "+") {
		t.Errorf("last node %+v, want a note of what was left out", last)
	}
}

func TestThePageDrawsTheGraphAsSVGAndRespectsReducedMotion(t *testing.T) {
	page, err := os.ReadFile("hallpage/hall.js")
	if err != nil {
		t.Fatal(err)
	}
	style, err := os.ReadFile("hallpage/hall.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "createElementNS(svgNamespace") {
		t.Errorf("hall.js does not draw SVG")
	}
	if !strings.Contains(string(style), "prefers-reduced-motion") {
		t.Errorf("hall.css ignores reduced motion")
	}
}
