package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type runningHall struct {
	url  string
	port string
	stop func() int
}

func startHall(t *testing.T, h *harness, args ...string) runningHall {
	t.Helper()
	reader, writer := io.Pipe()
	stop := make(chan struct{})
	var stderr bytes.Buffer
	app := &App{
		Stdin:           strings.NewReader(""),
		Stdout:          writer,
		Stderr:          &stderr,
		FindGh:          func() string { return h.gh },
		BrokerPID:       func(string) int { return 0 },
		IsTerminal:      func() bool { return false },
		StdinIsTerminal: func() bool { return false },
		GitHubAPI:       h.github.api(),
		hallStop:        stop,
	}
	exited := make(chan int, 1)
	go func() {
		exited <- app.Main(append([]string{"hall"}, args...))
		writer.Close()
	}()
	lines := bufio.NewScanner(reader)
	if !lines.Scan() {
		t.Fatalf("stage hall printed nothing; exit %d, stderr %q", <-exited, stderr.String())
	}
	url := regexp.MustCompile(`http://127\.0\.0\.1:([0-9]+)/[0-9a-f]{32}/`).FindStringSubmatch(lines.Text())
	if url == nil {
		t.Fatalf("stage hall printed %q", lines.Text())
	}
	go io.Copy(io.Discard, reader)
	code := -1
	running := runningHall{url: url[0], port: url[1], stop: func() int {
		if code >= 0 {
			return code
		}
		close(stop)
		select {
		case code = <-exited:
			return code
		case <-time.After(10 * time.Second):
			t.Fatalf("stage hall did not stop")
			return -1
		}
	}}
	t.Cleanup(func() { running.stop() })
	return running
}

func hallGet(t *testing.T, method, url, host string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		request.Host = host
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

type hallSnapshot struct {
	Format string `json:"format"`
	Stages []struct {
		stageRecord
		Remembered string `json:"remembered"`
		Earlier    []struct {
			Activity   activityRecord `json:"activity"`
			LastSeenAt string         `json:"last_seen_at"`
		} `json:"earlier_activities"`
		Attention []string `json:"attention"`
		Summary   struct {
			Sessions string `json:"sessions"`
			Activity string `json:"activity"`
			Disk     string `json:"disk"`
			Age      string `json:"age"`
		} `json:"summary"`
	} `json:"stages"`
}

func readHall(t *testing.T, hall runningHall) hallSnapshot {
	t.Helper()
	response := hallGet(t, "GET", hall.url+"api/hall", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("api/hall: %s", response.Status)
	}
	var snapshot hallSnapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func quickHall(t *testing.T, list, read, jobRead time.Duration) {
	t.Helper()
	before := [3]time.Duration{hallListEvery, hallReadEvery, hallJobReadEvery}
	hallListEvery, hallReadEvery, hallJobReadEvery = list, read, jobRead
	t.Cleanup(func() { hallListEvery, hallReadEvery, hallJobReadEvery = before[0], before[1], before[2] })
}

func TestHallServesTheStagesOnLoopbackBehindAToken(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	hall := startHall(t, h)
	snapshot := readHall(t, hall)
	if snapshot.Format != "stage-hall/1" || len(snapshot.Stages) != 1 {
		t.Fatalf("snapshot %+v", snapshot)
	}
	fix := snapshot.Stages[0]
	if fix.Slug != "fix" || fix.Sessions.Status != "ok" || len(fix.Sessions.Items) != 4 ||
		fix.Summary.Sessions != "4 (1 working, 1 waiting, 1 idle, 1 unknown)" || fix.Summary.Disk != "95%" {
		t.Errorf("fix %+v", fix)
	}
	for _, path := range []string{"", "hall.js", "hall.css", "api/hall"} {
		response := hallGet(t, "GET", hall.url+path, "")
		if response.StatusCode != http.StatusOK {
			t.Errorf("%q: %s", path, response.Status)
		}
		for header, want := range map[string]string{
			"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "no-referrer",
			"Cache-Control":           "no-store",
		} {
			if got := response.Header.Get(header); got != want {
				t.Errorf("%q: %s = %q, want %q", path, header, got, want)
			}
		}
	}
	if code := hall.stop(); code != 0 {
		t.Errorf("stage hall exited %d", code)
	}
	if _, err := http.Get(hall.url + "api/hall"); err == nil {
		t.Errorf("the page is still served after stage hall stopped")
	}
}

func TestHallAnswersOnlyGETsForItsTokenAndItsOwnHost(t *testing.T) {
	h := readyToRead(t)
	hall := startHall(t, h)
	base := strings.TrimSuffix(hall.url, "/")
	root := base[:strings.LastIndex(base, "/")]
	for _, check := range []struct {
		method, url, host string
		want              int
	}{
		{"GET", root + "/", "", http.StatusNotFound},
		{"GET", root + "/00000000000000000000000000000000/api/hall", "", http.StatusNotFound},
		{"GET", hall.url + "../api/hall", "", http.StatusNotFound},
		{"GET", hall.url + "nothing", "", http.StatusNotFound},
		{"POST", hall.url + "api/hall", "", http.StatusMethodNotAllowed},
		{"DELETE", hall.url, "", http.StatusMethodNotAllowed},
		{"GET", hall.url + "api/hall", "attacker.example:" + hall.port, http.StatusForbidden},
		{"GET", hall.url + "api/hall", "127.0.0.1", http.StatusForbidden},
		{"GET", hall.url + "api/hall", "localhost:" + hall.port, http.StatusForbidden},
		{"GET", hall.url + "api/hall", "127.0.0.1:" + hall.port, http.StatusOK},
		{"GET", root + "/favicon.ico", "", http.StatusNoContent},
	} {
		if got := hallGet(t, check.method, check.url, check.host).StatusCode; got != check.want {
			t.Errorf("%s %s Host %q: %d, want %d", check.method, check.url, check.host, got, check.want)
		}
	}
}

func TestHallListensOnlyOnLoopback(t *testing.T) {
	h := readyToRead(t)
	hall := startHall(t, h, "--port", "0")
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	tried := 0
	for _, address := range addresses {
		ip, ok := address.(*net.IPNet)
		if !ok || ip.IP.IsLoopback() || ip.IP.To4() == nil {
			continue
		}
		tried++
		if conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.IP.String(), hall.port), time.Second); err == nil {
			conn.Close()
			t.Errorf("the hall answered on %s", ip.IP)
		}
	}
	if tried == 0 {
		t.Log("this machine has no other IPv4 address to try")
	}
	h.expect(2, "hall", "--port", "70000")
}

func TestOnlyOneHallRunsForAStageHomeAndASecondSaysWhereTheFirstIs(t *testing.T) {
	h := readyToRead(t)
	testutil.WriteFile(t, filepath.Join(h.home, "hall.lock"), "pid 999999 port 1\n")
	hall := startHall(t, h)
	token := strings.Trim(strings.TrimPrefix(hall.url, "http://127.0.0.1:"+hall.port), "/")
	if lock := testutil.ReadFile(t, filepath.Join(h.home, "hall.lock")); strings.Contains(lock, token) {
		t.Errorf("the lock file holds the token: %q", lock)
	}
	second := make(chan int, 1)
	go func() { second <- h.stage("hall") }()
	select {
	case code := <-second:
		stderr := h.stderr.String()
		if code == 0 || !strings.Contains(stderr, "already running") ||
			!strings.Contains(stderr, fmt.Sprintf("pid %d", os.Getpid())) || !strings.Contains(stderr, "http://127.0.0.1:"+hall.port+"/") {
			t.Errorf("second hall exited %d: %s", code, stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("a second stage hall started for the same STAGE_HOME")
	}
}

func TestHallStopsAtOnceWhileTheListingHangs(t *testing.T) {
	h := readyToRead(t)
	quickHall(t, 100*time.Millisecond, time.Hour, time.Hour)
	hall := startHall(t, h)
	h.sbx.HangListing()
	time.Sleep(400 * time.Millisecond)
	started := time.Now()
	hall.stop()
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("stopping took %s", took)
	}
}

func TestAReadIsTimedFromWhenItReturned(t *testing.T) {
	h := readyToRead(t)
	h.sbx.SlowReads()
	quickHall(t, 100*time.Millisecond, 2*time.Second, time.Hour)
	hall := startHall(t, h)
	time.Sleep(1600 * time.Millisecond)
	hall.stop()
	if reads := strings.Fields(testutil.ReadFile(t, filepath.Join(h.sbx.State, "reads.log"))); len(reads) != 1 {
		t.Errorf("reads %v within 1.6 s of a read that took 1 s, with a 2 s cadence", reads)
	}
}

func TestThePageNeverCausesAnSbxCall(t *testing.T) {
	h := readyToRead(t)
	quickHall(t, time.Hour, time.Hour, time.Hour)
	hall := startHall(t, h)
	before := len(h.sbx.Calls())
	for range 20 {
		readHall(t, hall)
	}
	if after := len(h.sbx.Calls()); after != before {
		t.Errorf("20 page polls made %d sbx calls", after-before)
	}
}

func TestHallReadsARunningStageOnItsCadenceAndNeverAStoppedOne(t *testing.T) {
	h := readyToRead(t)
	h.expect(0, "up", h.remote.Checkout, "other")
	h.sbx.SetSandboxes("stage-fix\n")
	h.sbx.SetStopped("stage-other\n")
	quickHall(t, 100*time.Millisecond, time.Hour, time.Hour)
	hall := startHall(t, h)
	time.Sleep(1500 * time.Millisecond)
	hall.stop()
	reads := strings.Fields(testutil.ReadFile(t, filepath.Join(h.sbx.State, "reads.log")))
	if len(reads) != 1 || reads[0] != "stage-fix" {
		t.Errorf("reads %v, want stage-fix once in an hour's cadence and stage-other never", reads)
	}
}

func TestHallReadsAStageWithARunningJobMoreOften(t *testing.T) {
	h, _ := jobsReady(t)
	fakeDf(t, nearlyFullDf(h))
	_, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 })
	h.sbx.SetSandboxes("stage-fix\n")
	quickHall(t, 100*time.Millisecond, time.Hour, 300*time.Millisecond)
	hall := startHall(t, h)
	time.Sleep(1500 * time.Millisecond)
	hall.stop()
	if reads := strings.Fields(testutil.ReadFile(t, filepath.Join(h.sbx.State, "reads.log"))); len(reads) < 3 {
		t.Errorf("reads %v, want the job's faster cadence", reads)
	}
}

func TestStagesThatNeedAttentionComeFirst(t *testing.T) {
	h := readyToRead(t)
	h.expect(0, "up", h.remote.Checkout, "other")
	h.sbx.SetSandboxes("stage-fix\nstage-other\n")
	fakeDf(t, "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda 100 10 90 10% /\n")
	serveCAO(h, "9889")
	h.sbx.SetCAO("9889", "/terminals/5e6f7a8b", caoTerminal("5e6f7a8b", "review-5e6f", "codex", "review", "processing"))
	h.sbx.FailReads("stage-other", "boom\n")
	snapshot := readHall(t, startHall(t, h))
	var order []string
	for _, stage := range snapshot.Stages {
		order = append(order, stage.Slug+":"+strings.Join(stage.Attention, ","))
	}
	if want := "other:read,fix:"; strings.Join(order, ",") != want {
		t.Errorf("order %v, want %s", order, want)
	}
}

func TestThePageInsertsOnlyTextAndNamesNoTool(t *testing.T) {
	sources := map[string]string{}
	for _, name := range []string{"hallpage/index.html", "hallpage/hall.js", "hallpage/hall.css", "hall.go", "list.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = string(data)
	}
	for name, source := range sources {
		if !strings.HasPrefix(name, "hallpage/") {
			continue
		}
		source = strings.Replace(source, `"http://www.w3.org/2000/svg"`, "", 1)
		for _, forbidden := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "<script>", "style=", "http:", "https:"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains %q", name, forbidden)
			}
		}
	}
	toolWords := regexp.MustCompile(`(?i)\b(km|kapellmeister|ensemble|chairs?)\b`)
	for name, source := range sources {
		if word := toolWords.FindString(source); word != "" {
			t.Errorf("%s names a tool's own word %q", name, word)
		}
	}
}

func TestAnOpenTerminalShowsItsNewestLines(t *testing.T) {
	page, err := os.ReadFile("hallpage/hall.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "lines.scrollTop = lines.scrollHeight") {
		t.Errorf("an open terminal is not scrolled to its last line")
	}
}

func stageInHall(t *testing.T, hall runningHall, slug string) (stage struct {
	Slug, State, Remembered, Activity string
	Sessions                          int
	Activities, Earlier               []string
	Attention                         []string
}) {
	t.Helper()
	for _, found := range readHall(t, hall).Stages {
		if found.Slug != slug {
			continue
		}
		stage.Slug, stage.State, stage.Remembered, stage.Sessions, stage.Attention = found.Slug, found.State, found.Remembered, len(found.Sessions.Items), found.Attention
		stage.Activity = found.Summary.Activity
		for _, activity := range found.Activities {
			stage.Activities = append(stage.Activities, activity.ID+"="+activity.Status)
		}
		for _, earlier := range found.Earlier {
			if earlier.LastSeenAt == "" {
				t.Errorf("earlier activity %s has no last-seen time", earlier.Activity.ID)
			}
			stage.Earlier = append(stage.Earlier, earlier.Activity.ID+"="+earlier.Activity.Status)
		}
		return stage
	}
	t.Fatalf("no stage %s", slug)
	return stage
}

func TestAStageThatStopsKeepsItsLastReadingMarkedAsRemembered(t *testing.T) {
	h := readyToRead(t)
	serveCAO(h, "9889")
	printsDocument(t, "loop", `{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":"running"}]}`)
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	quickHall(t, 100*time.Millisecond, time.Hour, time.Hour)
	hall := startHall(t, h)
	if live := stageInHall(t, hall, "fix"); live.Remembered != "" || live.Sessions != 4 {
		t.Fatalf("live stage %+v", live)
	}
	h.sbx.SetSandboxes("")
	h.sbx.SetStopped("stage-fix\n")
	eventually(t, func() bool { return stageInHall(t, hall, "fix").State == "stopped" })
	stopped := stageInHall(t, hall, "fix")
	if stopped.Remembered == "" || stopped.Sessions != 4 || !slices.Equal(stopped.Activities, []string{"a=running"}) || len(stopped.Attention) != 0 {
		t.Errorf("stopped stage %+v, want its last reading kept, marked, and asking for no attention", stopped)
	}
}

func TestAnActivityThatEndsIsKeptAsEarlierActivity(t *testing.T) {
	h := readyToRead(t)
	document := filepath.Join(t.TempDir(), "activity.json")
	testutil.WriteFile(t, document, `{"format":"stage-activity/1","activities":[{"id":"a","title":"t","status":"running"}]}`)
	providerProgram(t, "loop", "cat '"+document+"'\n")
	userConfig(t, h, "[hall.provider.loop]\ncommand = [\"loop\"]\n")
	quickHall(t, 100*time.Millisecond, 300*time.Millisecond, time.Hour)
	hall := startHall(t, h)
	testutil.WriteFile(t, document, `{"format":"stage-activity/1","activities":[{"id":"b","title":"t","status":"running"}]}`)
	eventually(t, func() bool { return len(stageInHall(t, hall, "fix").Earlier) == 1 })
	stage := stageInHall(t, hall, "fix")
	if !slices.Equal(stage.Activities, []string{"b=running"}) || !slices.Equal(stage.Earlier, []string{"a=running"}) {
		t.Errorf("stage %+v, want b live and a kept as earlier", stage)
	}
	testutil.WriteFile(t, document, `{"format":"stage-activity/1","activities":[]}`)
	eventually(t, func() bool { return len(stageInHall(t, hall, "fix").Earlier) == 2 })
	if stage := stageInHall(t, hall, "fix"); stage.Activity != "last seen: t" {
		t.Errorf("row %q, want the last ended activity named as such", stage.Activity)
	}
}

func TestAStageIsReadOnceMoreWhenItsJobEnds(t *testing.T) {
	h, _ := jobsReady(t)
	fakeDf(t, nearlyFullDf(h))
	gate, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 })
	h.sbx.SetSandboxes("stage-fix\n")
	quickHall(t, 100*time.Millisecond, time.Hour, time.Hour)
	hall := startHall(t, h)
	reads := func() int { return len(strings.Fields(testutil.ReadFile(t, filepath.Join(h.sbx.State, "reads.log")))) }
	if got := reads(); got != 1 {
		t.Fatalf("reads %d before the job ended", got)
	}
	testutil.WriteFile(t, gate, "")
	eventually(t, func() bool { return reads() == 2 })
	time.Sleep(500 * time.Millisecond)
	hall.stop()
	if got := reads(); got != 2 {
		t.Errorf("reads %d, want one more read when the job ended and no more", got)
	}
}
