package cli

import "fmt"

const maxGraphNodes = 200

type hallGraph struct {
	Nodes   []graphNode `json:"nodes"`
	Edges   []graphEdge `json:"edges"`
	Columns int         `json:"columns"`
	Rows    int         `json:"rows"`
}

type graphNode struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Detail  string `json:"detail,omitempty"`
	State   string `json:"state,omitempty"`
	Session string `json:"session,omitempty"`
	Column  int    `json:"column"`
	Row     int    `json:"row"`
}

type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type graphLayout struct {
	graph hallGraph
	left  int
}

func (l *graphLayout) place(node graphNode, parent string, leaf bool) bool {
	if len(l.graph.Nodes) >= maxGraphNodes {
		l.left++
		return false
	}
	node.Row = l.graph.Rows
	l.graph.Nodes = append(l.graph.Nodes, node)
	if parent != "" {
		l.graph.Edges = append(l.graph.Edges, graphEdge{From: parent, To: node.ID})
	}
	l.graph.Columns = max(l.graph.Columns, node.Column+1)
	if leaf {
		l.graph.Rows++
	}
	return true
}

func stageGraph(record stageRecord, unclaimed []sessionRecord) hallGraph {
	layout := &graphLayout{graph: hallGraph{Nodes: []graphNode{}, Edges: []graphEdge{}}}
	empty := len(record.Activities) == 0 && len(unclaimed) == 0
	layout.place(graphNode{ID: "s", Kind: "stage", Label: record.Slug, State: record.State}, "", empty)
	for a, activity := range record.Activities {
		id := fmt.Sprintf("a%d", a)
		step := ""
		for _, candidate := range activity.Steps {
			if candidate.State == "active" {
				step = candidate.Name
				break
			}
		}
		if !layout.place(graphNode{ID: id, Kind: "activity", Label: activity.Title, Detail: step, State: activity.Status, Column: 1}, "s", len(activity.Groups) == 0) {
			for _, group := range activity.Groups {
				layout.left += 1 + len(group.Members)
			}
			continue
		}
		for g, group := range activity.Groups {
			groupID := fmt.Sprintf("%sg%d", id, g)
			if !layout.place(graphNode{ID: groupID, Kind: "group", Label: group.Name, Column: 2}, id, len(group.Members) == 0) {
				layout.left += len(group.Members)
				continue
			}
			for m, member := range group.Members {
				node := graphNode{ID: fmt.Sprintf("%sm%d", groupID, m), Kind: "member", Label: member.Name, Detail: member.Agent, State: member.State, Column: 3}
				if member.Session != nil {
					node.Session = member.Session.Source + " " + member.Session.ID
				}
				layout.place(node, groupID, true)
			}
		}
	}
	if len(unclaimed) > 0 && layout.place(graphNode{ID: "u", Kind: "group", Label: "sessions", Column: 2}, "s", false) {
		for i, session := range unclaimed {
			label, detail := session.Name, session.Agent
			if label == "" {
				label = session.ID
			}
			if detail == "" {
				detail = session.Source
			}
			layout.place(graphNode{ID: fmt.Sprintf("u%d", i), Kind: "member", Label: label, Detail: detail, State: session.State,
				Session: session.Source + " " + session.ID, Column: 3}, "u", true)
		}
	} else if len(unclaimed) > 0 {
		layout.left += len(unclaimed)
	}
	if layout.left > 0 {
		layout.graph.Nodes = append(layout.graph.Nodes, graphNode{ID: "more", Kind: "more", Label: fmt.Sprintf("+%d more", layout.left), Column: 1, Row: layout.graph.Rows})
		layout.graph.Rows++
	}
	return layout.graph
}
