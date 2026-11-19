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
	"slices"
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
	disk           []diskUse
	sessions       sessionsRecord
	activities     []activityRecord
	providerErrors []providerError
}

type stageProvider struct {
	name    string
	command []string
}

type readPlan struct {
	ports     []int
	providers []stageProvider
	disk      bool
}

const maxReportedPorts = 8

func readStage(task *Task, plan readPlan) (*readRecord, stageRead) {
	record, read := readOnce(task, plan)
	var extra []int
	for _, activity := range read.activities {
		if port := activity.CAOPort; port != 0 && !slices.Contains(plan.ports, port) && !slices.Contains(extra, port) && len(extra) < maxReportedPorts {
			extra = append(extra, port)
		}
	}
	if len(extra) == 0 || record.Error != "" {
		return record, read
	}
	more, second := readOnce(task, readPlan{ports: extra})
	record.TookMS += more.TookMS
	if more.Error != "" {
		record.Error = more.Error
	}
	read.sessions = mergeSessions(read.sessions, second.sessions)
	return record, read
}

func readOnce(task *Task, plan readPlan) (*readRecord, stageRead) {
	started := time.Now()
	record := &readRecord{At: started.UTC().Format(time.RFC3339)}
	boundary := newBoundary()
	output, err := sbx.ReadWithin(stageReadTimeout, strings.NewReader(readScriptFor(boundary, plan)), stageReadLimit,
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
	return record, readFrames(frames, task.RepoDir, plan)
}

func mergeSessions(first, second sessionsRecord) sessionsRecord {
	rank := map[string]int{"ok": 4, "timed_out": 3, "unreadable": 2, "no_cao": 1}
	merged := first
	if rank[second.Status] > rank[first.Status] {
		merged.Status = second.Status
	}
	merged.Items = append(append([]sessionRecord{}, first.Items...), second.Items...)
	var notes []string
	for _, detail := range []string{first.Detail, second.Detail} {
		if detail != "" && !slices.Contains(notes, detail) {
			notes = append(notes, detail)
		}
	}
	merged.Detail = strings.Join(notes, "; ")
	return merged
}

func newBoundary() string {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return "stage-read-" + hex.EncodeToString(nonce)
}

func readScriptFor(boundary string, plan readPlan) string {
	var ports, providers []string
	for _, port := range plan.ports {
		ports = append(ports, strconv.Itoa(port))
	}
	for index, provider := range plan.providers {
		line := []string{"provider", strconv.Itoa(index)}
		for _, arg := range provider.command {
			line = append(line, shellQuote(arg))
		}
		providers = append(providers, strings.Join(line, " "))
	}
	disk := ""
	if plan.disk {
		disk = "1"
	}
	return strings.NewReplacer("@BOUNDARY@", boundary, "@PORTS@", strings.Join(ports, " "), "@DISK@", disk,
		"@PROVIDERS@", strings.Join(providers, "\n")).Replace(readScript)
}

func shellQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func providerOutcome(provider string, frame readFrame) ([]activityRecord, *providerError) {
	failed := func(reason string) ([]activityRecord, *providerError) {
		return nil, &providerError{Provider: provider, Error: reason}
	}
	switch frame.status {
	case "0":
		activities, err := parseActivities(provider, []byte(frame.body))
		if err != nil {
			return failed("output refused: " + err.Error())
		}
		return activities, nil
	case "absent":
		return nil, nil
	case "no-timeout":
		return failed("not run: the stage has no timeout command")
	case "late":
		return failed("not run: the read ran out of time")
	case "too-large":
		return failed(fmt.Sprintf("output refused: it is over %d KiB", maxProviderDocument>>10))
	case "124", "137":
		return failed("it did not finish within 5 seconds")
	}
	return failed("it exited with status " + shortPrintable(frame.status, 16))
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

func readFrames(frames []readFrame, repoDir string, plan readPlan) stageRead {
	ports := plan.ports
	read := stageRead{activities: []activityRecord{}, providerErrors: []providerError{}}
	servers := map[string]string{}
	terminals := map[string]*sessionRecord{}
	var order []string
	leftOut := 0
	noTimeout := false
	providerSeen := map[int]bool{}
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
		case len(fields) == 2 && fields[0] == "provider":
			index, err := strconv.Atoi(fields[1])
			if err != nil || index < 0 || index >= len(plan.providers) || providerSeen[index] {
				continue
			}
			providerSeen[index] = true
			activities, failure := providerOutcome(plan.providers[index].name, frame)
			read.activities = append(read.activities, activities...)
			if failure != nil {
				read.providerErrors = append(read.providerErrors, *failure)
			}
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
