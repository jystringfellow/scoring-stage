package cli

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

//go:embed read.sh
var readScript string

const (
	stageReadLimit  = 1 << 20
	tailLines       = 40
	tailLineRunes   = 300
	jobTailBytes    = 16 << 10
	defaultCAOPort  = 9889
	caoServerFormat = "127.0.0.1:%d"
)

var (
	stageReadTimeout = 10 * time.Second
	caoTerminalID    = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

type readRecord struct {
	At     string `json:"at"`
	TookMS int64  `json:"took_ms"`
	Error  string `json:"error,omitempty"`
}

type sessionsRecord struct {
	Status string          `json:"status"`
	Detail string          `json:"detail,omitempty"`
	Items  []sessionRecord `json:"items"`
}

type sessionRecord struct {
	Source   string   `json:"source"`
	ID       string   `json:"id"`
	Server   string   `json:"server,omitempty"`
	Name     string   `json:"name,omitempty"`
	Agent    string   `json:"agent,omitempty"`
	State    string   `json:"state"`
	RawState string   `json:"raw_state,omitempty"`
	Tail     []string `json:"tail,omitempty"`
}

type readFrame struct {
	name, body, status string
}

type stageRead struct {
	disk     []diskUse
	sessions sessionsRecord
}

func readStage(task *Task, ports []int) (*readRecord, stageRead) {
	started := time.Now()
	record := &readRecord{At: started.UTC().Format(time.RFC3339)}
	boundary := newBoundary()
	output, err := sbx.ReadWithin(stageReadTimeout, strings.NewReader(readScriptFor(boundary, ports)), stageReadLimit,
		"exec", "-i", "-u", "agent", "-w", task.RepoDir, task.Sandbox, "sh", "-s")
	record.TookMS = time.Since(started).Milliseconds()
	if err != nil {
		record.Error = config.Printable(err.Error())
		status := "unreadable"
		if strings.Contains(err.Error(), "did not finish") {
			status = "timed_out"
		}
		return record, stageRead{sessions: sessionsRecord{Status: status, Items: []sessionRecord{}}}
	}
	frames, err := parseFrames(output, boundary)
	if err != nil {
		record.Error = config.Printable(err.Error())
		return record, stageRead{sessions: sessionsRecord{Status: "unreadable", Items: []sessionRecord{}}}
	}
	return record, readFrames(frames, task.RepoDir, ports)
}

func newBoundary() string {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return "stage-read-" + hex.EncodeToString(nonce)
}

func readScriptFor(boundary string, ports []int) string {
	var listed []string
	for _, port := range ports {
		listed = append(listed, strconv.Itoa(port))
	}
	return strings.NewReplacer("@BOUNDARY@", boundary, "@PORTS@", strings.Join(listed, " ")).Replace(readScript)
}

func parseFrames(output, boundary string) ([]readFrame, error) {
	begin, end := boundary+" begin ", "\n"+boundary+" end "
	var frames []readFrame
	for rest := output; rest != ""; {
		if !strings.HasPrefix(rest, begin) {
			return nil, errors.New("the stage sent output stage did not ask for")
		}
		name, after, _ := strings.Cut(rest[len(begin):], "\n")
		body, closing, found := strings.Cut(after, end)
		if !found {
			return nil, errors.New("the stage's output ended inside a part")
		}
		status, next, _ := strings.Cut(closing, "\n")
		frames = append(frames, readFrame{name: name, body: body, status: status})
		rest = next
	}
	if len(frames) == 0 {
		return nil, errors.New("the stage sent nothing")
	}
	return frames, nil
}

func readFrames(frames []readFrame, repoDir string, ports []int) stageRead {
	var read stageRead
	servers := map[string]string{}
	terminals := map[string]*sessionRecord{}
	var order []string
	leftOut := 0
	noTimeout := false
	for _, frame := range frames {
		fields := strings.Fields(frame.name)
		switch {
		case frame.name == "disk" && frame.status == "0":
			read.disk = parseDiskUse(frame.body, repoDir)
		case frame.name == "cao" && frame.status == "no-curl":
			read.sessions.Detail = "the stage has no curl"
			for _, port := range ports {
				servers[strconv.Itoa(port)] = "unreadable"
			}
		case frame.name == "note" && frame.status == "no-timeout":
			noTimeout = true
		case len(fields) == 3 && fields[0] == "cao" && fields[2] == "sessions" && askedFor(fields[1], ports):
			servers[fields[1]] = serverState(frame.status)
		case len(fields) == 4 && fields[0] == "cao" && fields[2] == "terminal" && askedFor(fields[1], ports):
			session, ok := caoSession(fields[1], fields[3], frame)
			if !ok {
				leftOut++
				continue
			}
			key := fields[1] + " " + fields[3]
			if terminals[key] == nil {
				order = append(order, key)
			}
			terminals[key] = session
		case len(fields) == 3 && fields[0] == "tail" && frame.status == "0":
			if session := terminals[fields[1]+" "+fields[2]]; session != nil {
				session.Tail = lastLines(frame.body)
			}
		}
	}
	read.sessions.Items = []sessionRecord{}
	for _, key := range order {
		read.sessions.Items = append(read.sessions.Items, *terminals[key])
	}
	sort.SliceStable(read.sessions.Items, func(i, j int) bool {
		a, b := read.sessions.Items[i], read.sessions.Items[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		return a.ID < b.ID
	})
	read.sessions.Status = overallCAOState(servers)
	var notes []string
	if read.sessions.Detail != "" {
		notes = append(notes, read.sessions.Detail)
	}
	if leftOut > 0 {
		notes = append(notes, fmt.Sprintf("%s left out because CAO's answer could not be read", count(leftOut, "terminal")))
	}
	if noTimeout {
		notes = append(notes, "the stage has no timeout command, so no terminal tails were read")
	}
	read.sessions.Detail = strings.Join(notes, "; ")
	return read
}

func askedFor(port string, ports []int) bool {
	for _, asked := range ports {
		if strconv.Itoa(asked) == port {
			return true
		}
	}
	return false
}

func serverState(status string) string {
	switch status {
	case "0":
		return "ok"
	case "7":
		return "none"
	case "28", "late":
		return "timed_out"
	}
	return "unreadable"
}

func overallCAOState(servers map[string]string) string {
	seen := map[string]bool{}
	for _, state := range servers {
		seen[state] = true
	}
	switch {
	case seen["ok"]:
		return "ok"
	case seen["timed_out"]:
		return "timed_out"
	case seen["unreadable"]:
		return "unreadable"
	}
	return "no_cao"
}

func caoSession(port, id string, frame readFrame) (*sessionRecord, bool) {
	if frame.status != "0" || !caoTerminalID.MatchString(id) {
		return nil, false
	}
	var terminal struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Provider     string `json:"provider"`
		AgentProfile string `json:"agent_profile"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal([]byte(frame.body), &terminal); err != nil || terminal.ID != id {
		return nil, false
	}
	portNumber, _ := strconv.Atoi(port)
	name := terminal.AgentProfile
	if name == "" {
		name = terminal.Name
	}
	return &sessionRecord{
		Source:   "cao",
		ID:       id,
		Server:   fmt.Sprintf(caoServerFormat, portNumber),
		Name:     shortPrintable(name, 100),
		Agent:    shortPrintable(terminal.Provider, 64),
		State:    caoState(terminal.Status),
		RawState: shortPrintable(terminal.Status, 64),
	}, true
}

func caoState(status string) string {
	switch status {
	case "processing":
		return "working"
	case "waiting_user_answer":
		return "waiting"
	case "idle", "completed":
		return "idle"
	}
	return "unknown"
}

func shortPrintable(text string, limit int) string {
	escaped := []rune(config.Printable(text))
	if len(escaped) > limit {
		return string(escaped[:limit-1]) + "…"
	}
	return string(escaped)
}

func lastLines(text string) []string {
	escaped, _ := escapeOutput([]byte(strings.TrimRight(text, "\n")), true)
	lines := strings.Split(escaped, "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	for i, line := range lines {
		if runes := []rune(line); len(runes) > tailLineRunes {
			lines[i] = string(runes[:tailLineRunes-1]) + "…"
		}
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func jobSessions(home, slug string) []sessionRecord {
	jobs, err := listJobs(home, slug)
	if err != nil {
		return nil
	}
	var sessions []sessionRecord
	for _, job := range jobs {
		state := "exited"
		if job.State == "running" {
			state = "working"
		}
		sessions = append(sessions, sessionRecord{
			Source:   "job",
			ID:       job.ID,
			Name:     shortPrintable(strings.Join(job.Command, " "), 100),
			State:    state,
			RawState: job.State,
			Tail:     jobTail(home, slug, job.ID),
		})
	}
	return sessions
}

func jobTail(home, slug, id string) []string {
	file, err := os.Open(filepath.Join(jobDir(home, slug, id), "output"))
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	start := max(info.Size()-jobTailBytes, 0)
	data, err := io.ReadAll(io.NewSectionReader(file, start, info.Size()-start))
	if err != nil {
		return nil
	}
	if start > 0 {
		if _, rest, found := strings.Cut(string(data), "\n"); found {
			data = []byte(rest)
		}
	}
	return lastLines(string(data))
}
