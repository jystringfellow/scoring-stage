package cli

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

//go:embed hallpage
var hallPage embed.FS

const (
	hallFormat        = "stage-hall/1"
	hallSecurityRules = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; frame-ancestors 'none'"
)

var (
	hallListEvery    = 5 * time.Second
	hallReadEvery    = 60 * time.Second
	hallJobReadEvery = 10 * time.Second
	hallAssets       = map[string]string{"": "index.html", "hall.js": "hall.js", "hall.css": "hall.css"}
	hallContentTypes = map[string]string{"index.html": "text/html; charset=utf-8", "hall.js": "text/javascript; charset=utf-8", "hall.css": "text/css; charset=utf-8"}
)

type hallStage struct {
	stageRecord
	Attention []string        `json:"attention"`
	Summary   hallSummary     `json:"summary"`
	Unclaimed []sessionRecord `json:"unclaimed_sessions"`
}

type hallSummary struct {
	Sessions string `json:"sessions"`
	Activity string `json:"activity"`
	Disk     string `json:"disk"`
	Age      string `json:"age"`
}

type hallView struct {
	Format string      `json:"format"`
	ReadAt string      `json:"read_at"`
	Error  string      `json:"error,omitempty"`
	Stages []hallStage `json:"stages"`
}

type hall struct {
	home     string
	plan     readPlan
	mu       sync.Mutex
	snapshot []byte
	readings map[string]stageReading
}

func (a *App) cmdHall(args []string, home string) (int, error) {
	fs := a.newFlags("hall", "[--port N]")
	port := fs.Int("port", 0, "the loopback port to serve on; 0 picks a free one")
	if _, err := fs.parse(args); err != nil {
		return 0, err
	}
	if *port < 0 || *port > 65535 {
		return 0, &usageError{"--port must be between 0 and 65535"}
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		return 0, errorf("cannot listen on 127.0.0.1: %v", err)
	}
	served := &hall{home: home, plan: hallPlan(settings.Hall), readings: map[string]stageReading{}}
	served.refresh(time.Now())
	token := newHallToken()
	bound := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	server := &http.Server{Handler: served.handler(token, bound), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if a.hallStop != nil {
		go func() {
			select {
			case <-a.hallStop:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	fmt.Fprintf(a.Stdout, "stage hall: http://127.0.0.1:%s/%s/ (Ctrl-C stops it)\n", bound, token)
	served.keepFresh(ctx)
	shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	server.Shutdown(shutdown)
	return 0, nil
}

func newHallToken() string {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		panic(err)
	}
	return hex.EncodeToString(token)
}

func (h *hall) keepFresh(ctx context.Context) {
	ticker := time.NewTicker(hallListEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.refresh(now)
		}
	}
}

func (h *hall) refresh(now time.Time) {
	view := hallView{Format: hallFormat, ReadAt: now.UTC().Format(time.RFC3339), Stages: []hallStage{}}
	listed, err := sbx.List()
	var entries []*stageEntry
	if err == nil {
		entries, err = collectStages(h.home, listed)
	}
	if err != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		var last hallView
		if json.Unmarshal(h.snapshot, &last) == nil {
			view = last
		}
		view.Error = config.Printable(err.Error())
		h.snapshot, _ = json.Marshal(view)
		return
	}
	var due []*stageEntry
	running := map[string]bool{}
	for _, entry := range entries {
		if entry.task == nil || entry.record.State != "running" {
			continue
		}
		running[entry.record.Slug] = true
		every := hallReadEvery
		if entry.record.RunningJobs > 0 {
			every = hallJobReadEvery
		}
		if last, ok := h.readings[entry.record.Slug]; !ok || now.Sub(last.at) >= every {
			due = append(due, entry)
		}
	}
	for entry, reading := range readEntries(due, h.plan) {
		reading.at = now
		h.readings[entry.record.Slug] = reading
	}
	for slug := range h.readings {
		if !running[slug] {
			delete(h.readings, slug)
		}
	}
	for _, entry := range entries {
		if reading, ok := h.readings[entry.record.Slug]; ok {
			applyReading(entry, reading)
		}
		view.Stages = append(view.Stages, describeForHall(entry.record, now))
	}
	slices.SortStableFunc(view.Stages, func(a, b hallStage) int {
		if (len(a.Attention) > 0) != (len(b.Attention) > 0) {
			if len(a.Attention) > 0 {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Slug, b.Slug)
	})
	snapshot, _ := json.Marshal(view)
	h.mu.Lock()
	h.snapshot = snapshot
	h.mu.Unlock()
}

func describeForHall(record stageRecord, now time.Time) hallStage {
	stage := hallStage{
		stageRecord: record,
		Attention:   attentionFor(record),
		Summary: hallSummary{
			Sessions: sessionsSummary(record.Sessions),
			Activity: activitySummary(record.Activities, record.ProviderErrors),
			Disk:     fullestDisk(record.Disk),
			Age:      age(record.CreatedAt, now),
		},
		Unclaimed: []sessionRecord{},
	}
	claimed := map[string]bool{}
	for _, activity := range record.Activities {
		for _, group := range activity.Groups {
			for _, member := range group.Members {
				if member.Session != nil {
					claimed[member.Session.Source+" "+member.Session.ID] = true
				}
			}
		}
	}
	for _, session := range record.Sessions.Items {
		if !claimed[session.Source+" "+session.ID] {
			stage.Unclaimed = append(stage.Unclaimed, session)
		}
	}
	return stage
}

func attentionFor(record stageRecord) []string {
	reasons := []string{}
	waiting := slices.ContainsFunc(record.Sessions.Items, func(s sessionRecord) bool { return s.State == "waiting" })
	failing := false
	for _, activity := range record.Activities {
		failing = failing || activity.Status == "failed" || activity.Status == "blocked"
		for _, group := range activity.Groups {
			waiting = waiting || slices.ContainsFunc(group.Members, func(m activityMember) bool { return m.State == "waiting" })
		}
	}
	if waiting {
		reasons = append(reasons, "waiting")
	}
	if failing {
		reasons = append(reasons, "failing")
	}
	if record.Read != nil && record.Read.Error != "" || len(record.ProviderErrors) > 0 {
		reasons = append(reasons, "read")
	}
	if slices.ContainsFunc(record.Disk, func(d diskUse) bool { return d.UsedPercent >= diskNearlyFullPercent }) {
		reasons = append(reasons, "disk")
	}
	return reasons
}

func (h *hall) handler(token, port string) http.Handler {
	prefix := "/" + token + "/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", hallSecurityRules)
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("Cache-Control", "no-store")
		headers.Set("X-Frame-Options", "DENY")
		if r.Host != "127.0.0.1:"+port && r.Host != "localhost:"+port {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			headers.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.EscapedPath()
		if len(path) < len(prefix) || subtle.ConstantTimeCompare([]byte(path[:len(prefix)]), []byte(prefix)) != 1 {
			http.NotFound(w, r)
			return
		}
		rest := path[len(prefix):]
		if rest == "api/hall" {
			h.mu.Lock()
			snapshot := h.snapshot
			h.mu.Unlock()
			headers.Set("Content-Type", "application/json")
			w.Write(snapshot)
			return
		}
		asset, ok := hallAssets[rest]
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := hallPage.ReadFile("hallpage/" + asset)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		headers.Set("Content-Type", hallContentTypes[asset])
		w.Write(data)
	})
}
