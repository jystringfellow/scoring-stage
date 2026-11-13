package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(until) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func jobsReady(t *testing.T) (*harness, *[]*exec.Cmd) {
	t.Helper()
	h, _ := putReady(t)
	t.Setenv("STAGE_TEST_AS_STAGE", "1")
	h.sbx.RunExecs()
	var holders []*exec.Cmd
	var mu sync.Mutex
	original := startJobHolder
	startJobHolder = func(c *exec.Cmd) error {
		if err := c.Start(); err != nil {
			return err
		}
		mu.Lock()
		holders = append(holders, c)
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() {
		defer func() { startJobHolder = original }()
		for _, c := range holders {
			// Reap every holder before the fake's files and environment disappear.
			if c.ProcessState == nil {
				ids, _ := listJobs(h.home, "fix")
				for _, j := range ids {
					if j.State == "running" {
						h.stage("job", "stop", "fix", j.ID)
					}
				}
				c.Wait()
			}
		}
	})
	return h, &holders
}

func gated(t *testing.T, h *harness) (string, []string) {
	t.Helper()
	gate := filepath.Join(t.TempDir(), "gate")
	return gate, []string{"exec", "--detach", "fix", "--", "sh", "-c", `while [ ! -f "$1" ]; do sleep 0.02; done; echo out; echo err >&2; exit 3`, "sh", gate}
}

func statusOf(t *testing.T, h *harness, id string) jobStatus {
	t.Helper()
	h.expect(0, "job", "status", "fix", id, "--json")
	var j jobStatus
	if err := json.Unmarshal(h.stdout.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestADetachedCommandPrintsOnlyItsIdAndReturnsWhileItKeepsRunning(t *testing.T) {
	h, _ := jobsReady(t)
	_, args := gated(t, h)
	h.expect(0, args...)
	if h.stdout.String() != "1\n" {
		t.Fatal(h.stdout.String())
	}
	h.expectStderr("stage job wait fix 1")
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 })
	if j := statusOf(t, h, "1"); j.State != "running" || j.Status != nil {
		t.Fatalf("%+v", j)
	}
	call := h.sbx.LastCall()
	if !strings.HasPrefix(call, "exec -u agent -w "+filepath.Join(h.task("fix"), "src")+" stage-fix sh -c ") {
		t.Fatal(call)
	}
	if sessions := h.sbx.Sessions(); !strings.HasPrefix(sessions[0], "session-start ") {
		t.Fatal(sessions)
	}
}

func TestADetachedCommandIsLaterReportedWithItsRealStatusAndItsFullOutput(t *testing.T) {
	h, _ := jobsReady(t)
	gate, args := gated(t, h)
	h.expect(0, args...)
	testutil.WriteFile(t, gate, "")
	h.expect(3, "job", "wait", "fix", "1", "--timeout", "5s", "--json")
	var j jobStatus
	if err := json.Unmarshal(h.stdout.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.State != "exited" || j.Status == nil || *j.Status != 3 || j.EndedAt == "" || j.StartedAt == "" || j.OutputBytes != 8 {
		t.Fatalf("%+v", j)
	}
	h.expect(0, "job", "output", "fix", "1")
	if h.stdout.String() != "out\nerr\n" {
		t.Fatal(h.stdout.String())
	}
	for _, name := range []string{"job.json", "output", "exit.json", "lock"} {
		info, err := os.Stat(filepath.Join(h.task("fix"), "jobs", "1", name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %v", name, info.Mode())
		}
	}
	info, err := os.Stat(filepath.Join(h.task("fix"), "jobs", "1"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode: %v %v", info, err)
	}
	if sessions := h.sbx.Sessions(); len(sessions) != 2 || sessions[1] != "session-end 3" {
		t.Fatal(sessions)
	}
}

func TestWaitReturnsTheJobsStatusAndHonoursItsTimeout(t *testing.T) {
	h, _ := jobsReady(t)
	gate, args := gated(t, h)
	h.expect(0, args...)
	began := time.Now()
	h.expect(124, "job", "wait", "fix", "1", "--timeout", "200ms", "--json")
	if elapsed := time.Since(began); elapsed < 180*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal(elapsed)
	}
	if !strings.Contains(h.stdout.String(), `"state":"running"`) {
		t.Fatal(h.stdout.String())
	}
	testutil.WriteFile(t, gate, "")
	h.expect(3, "job", "wait", "fix", "1", "--timeout", "5s")
	h.expect(0, "exec", "fix", "--detach", "--", "sh", "-c", "exit 0")
	h.expect(0, "job", "wait", "fix", "2", "--timeout", "5s")
	h.expect(2, "job", "wait", "fix", "1", "--timeout", "-1s")
}

func TestAJobWhoseHolderWasKilledIsReportedLost(t *testing.T) {
	h, holders := jobsReady(t)
	gate, args := gated(t, h)
	args[6] = "printf saved; " + args[6]
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 && statusOf(t, h, "1").OutputBytes == 5 })
	fields := strings.Fields(h.sbx.Sessions()[0])
	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	// The orphan belongs to this test, and must finish even if an assertion fails.
	t.Cleanup(func() {
		testutil.WriteFile(t, gate, "")
		_ = syscall.Kill(pid, syscall.SIGTERM)
		eventually(t, func() bool { return len(h.sbx.Sessions()) >= 2 })
	})
	c := (*holders)[0]
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	c.Wait()
	if j := statusOf(t, h, "1"); j.State != "lost" || j.Status != nil {
		t.Fatalf("%+v", j)
	}
	h.expect(125, "job", "wait", "fix", "1")
	h.expectStderr("lost")
	h.expect(0, "job", "list", "fix")
	if !strings.Contains(h.stdout.String(), "lost") {
		t.Fatal(h.stdout.String())
	}
	h.expect(0, "job", "output", "fix", "1")
	if h.stdout.String() != "saved" {
		t.Fatal(h.stdout.String())
	}
	h.expect(1, "job", "stop", "fix", "1")
	h.expectStderr("lost")
}

func TestAJobLeftRunningByAHostThatRestartedIsReportedLost(t *testing.T) {
	h, _ := jobsReady(t)
	dir := filepath.Join(h.task("fix"), "jobs", "1")
	testutil.WriteFile(t, filepath.Join(dir, "job.json"), `{"id":"1","slug":"fix","sandbox":"stage-fix","command":["example"],"started_at":"now"}`)
	testutil.WriteFile(t, filepath.Join(dir, "output"), "saved\n")
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if j := statusOf(t, h, "1"); j.State != "lost" {
		t.Fatalf("%+v", j)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if j := statusOf(t, h, "1"); j.State != "running" {
		t.Fatalf("%+v", j)
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	testutil.WriteFile(t, filepath.Join(dir, "exit.json"), `{"status":9,"ended_at":"then"}`)
	if j := statusOf(t, h, "1"); j.State != "exited" || j.Status == nil || *j.Status != 9 {
		t.Fatalf("%+v", j)
	}
	h.expect(1, "job", "stop", "fix", "1")
	h.expectStderr("exited")
}

func TestStopEndsARunningJobAndKeepsItsOutput(t *testing.T) {
	h, _ := jobsReady(t)
	_, args := gated(t, h)
	args[6] = "printf saved; " + args[6]
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 && statusOf(t, h, "1").OutputBytes == 5 })
	h.expect(0, "job", "stop", "fix", "1")
	if j := statusOf(t, h, "1"); j.State != "stopped" || j.Status != nil {
		t.Fatalf("%+v", j)
	}
	h.expect(125, "job", "wait", "fix", "1")
	h.expectStderr("stopped")
	h.expect(0, "job", "output", "fix", "1")
	if h.stdout.String() != "saved" {
		t.Fatal(h.stdout.String())
	}
	if sessions := h.sbx.Sessions(); len(sessions) != 2 || sessions[1] != "session-end stopped" {
		t.Fatal(sessions)
	}
	h.expect(1, "job", "stop", "fix", "1")
	h.expectStderr("stopped")
}

func TestDownRefusesWhileAJobIsRunningAndRemovesFinishedJobsWithTheTask(t *testing.T) {
	h, _ := jobsReady(t)
	_, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(h.sbx.Sessions()) == 1 })
	before := len(h.sbx.Calls())
	h.expect(1, "down", "fix", "--discard-takes")
	h.expectStderr("1 running jobs (1)")
	h.expectStderr("nothing was removed")
	if _, err := os.Stat(h.task("fix")); err != nil {
		t.Fatal(err)
	}
	for _, call := range h.sbx.Calls()[before:] {
		if strings.HasPrefix(call, "env rm") || strings.HasPrefix(call, "rm ") || strings.HasPrefix(call, "exec ") {
			t.Fatal(call)
		}
	}
	h.expect(0, "job", "stop", "fix", "1")
	h.expect(0, "down", "fix")
	if _, err := os.Stat(h.task("fix")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestDownRemovesLostJobsAndJobsOfASandboxThatIsAlreadyGone(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(fmt.Sprint(absent), func(t *testing.T) {
			h, _ := jobsReady(t)
			dir := filepath.Join(h.task("fix"), "jobs", "1")
			testutil.WriteFile(t, filepath.Join(dir, "job.json"), `{"id":"1","slug":"fix","sandbox":"stage-fix","command":["example"]}`)
			testutil.WriteFile(t, filepath.Join(dir, "lock"), "")
			lock, err := os.Open(filepath.Join(dir, "lock"))
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if absent {
				syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
				h.sbx.SetSandboxes("")
			}
			h.expect(0, "down", "fix")
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}

func TestJobOutputEscapesControlCharactersAndNeverInterpretsThem(t *testing.T) {
	h, _ := jobsReady(t)
	h.expect(0, "exec", "--detach", "fix", "--", "sh", "-c", `printf 'hello\033\r\302\205\342\200\256\303\251\n'`)
	h.expect(0, "job", "wait", "fix", "1", "--timeout", "5s")
	old := jobReadBuffer
	jobReadBuffer = 1
	t.Cleanup(func() { jobReadBuffer = old })
	h.expect(0, "job", "output", "fix", "1", "--follow")
	if got := h.stdout.String(); got != "hello\\x1b\\x0d\\x85\\x202eé\n" {
		t.Fatalf("%q", got)
	}
}

func TestJobOutputIsCutAtItsLimit(t *testing.T) {
	h, _ := jobsReady(t)
	t.Setenv("STAGE_TEST_JOB_OUTPUT_LIMIT", "4")
	h.expect(0, "exec", "--detach", "fix", "--", "sh", "-c", `printf 123456789`)
	h.expect(0, "job", "wait", "fix", "1", "--timeout", "5s")
	if j := statusOf(t, h, "1"); !j.OutputTruncated || j.OutputBytes != 4 {
		t.Fatalf("%+v", j)
	}
	h.expect(0, "job", "output", "fix", "1")
	if !strings.HasPrefix(h.stdout.String(), "1234") {
		t.Fatal(h.stdout.String())
	}
	h.expectStderr("truncated")
}

func TestJobsAreListedWithTheirStatesAsTextAndJSON(t *testing.T) {
	h, _ := jobsReady(t)
	h.expect(0, "job", "list", "fix", "--json")
	if strings.TrimSpace(h.stdout.String()) != "[]" {
		t.Fatal(h.stdout.String())
	}
	h.expect(0, "exec", "--detach", "fix", "--", "sh", "-c", "exit 6")
	h.expect(6, "job", "wait", "fix", "1", "--timeout", "5s")
	_, args := gated(t, h)
	h.expect(0, args...)
	h.expect(0, "job", "list", "fix", "--json")
	var js []jobStatus
	if err := json.Unmarshal(h.stdout.Bytes(), &js); err != nil {
		t.Fatal(err)
	}
	if len(js) != 2 || js[0].State != "exited" || js[1].State != "running" {
		t.Fatalf("%+v", js)
	}
	h.expect(0, "job", "list", "fix")
	for _, want := range []string{"1", "exited", "6", "2", "running"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Fatal(h.stdout.String())
		}
	}
}

func TestJobIdsCannotNameAPathAndHoldRequiresItsLock(t *testing.T) {
	h, _ := jobsReady(t)
	for _, id := range []string{"../x", "1/../1", "0", "-1", "1000000000", "1\x1b"} {
		h.expect(1, "job", "status", "fix", "--", id)
		h.expectStderr("no job")
		if strings.Contains(h.stderr.String(), "\x1b") {
			t.Fatal("unescaped error")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rejected := exec.Command(executable, "job", "hold", "fix", "1")
	said, err := rejected.CombinedOutput()
	if err == nil || !strings.Contains(string(said), "requires its inherited lock") {
		t.Fatalf("hold: %s %v", said, err)
	}
	for _, args := range [][]string{{"job"}, {"job", "unknown"}} {
		h.expect(2, args...)
	}
	h.expect(0, "--help")
	for _, s := range []string{"put <slug>", "--detach", "job list"} {
		if !strings.Contains(h.stdout.String(), s) {
			t.Fatal(h.stdout.String())
		}
	}
}

func TestConcurrentDetachedStartsGetDistinctIds(t *testing.T) {
	h, _ := jobsReady(t)
	task, err := loadTask(h.home, "fix")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := startJob(h.home, task, []string{"sh", "-c", "exit 0"})
			results <- id
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	seen := map[string]bool{}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range results {
		if seen[id] {
			t.Fatal("duplicate", id)
		}
		seen[id] = true
	}
	for id := range seen {
		h.expect(0, "job", "wait", "fix", id, "--timeout", "5s")
	}
}

func TestAnExecCallerCanExitBeforeItsDetachedCommand(t *testing.T) {
	h, _ := jobsReady(t)
	gate, args := gated(t, h)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	caller := exec.Command(executable, args...)
	var stderr bytes.Buffer
	caller.Stderr = &stderr
	out, err := caller.Output()
	if err != nil {
		t.Fatalf("caller: %v %s", err, &stderr)
	}
	if string(out) != "1\n" || statusOf(t, h, "1").State != "running" {
		t.Fatalf("%q", out)
	}
	t.Cleanup(func() {
		if j, err := readJob(h.home, "fix", "1"); err == nil && j.State == "running" {
			h.stage("job", "stop", "fix", "1")
		}
	})
	testutil.WriteFile(t, gate, "")
	h.expect(3, "job", "wait", "fix", "1", "--timeout", "5s")
}

func TestJobOutputFollowsWritesUntilTheCommandEndsAndDrainsTheLastBytes(t *testing.T) {
	h, _ := jobsReady(t)
	gate := filepath.Join(t.TempDir(), "gate")
	h.expect(0, "exec", "--detach", "fix", "--", "sh", "-c", `printf first; while [ ! -f "$1" ]; do sleep 0.02; done; printf '\303\251last\n'`, "sh", gate)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	follower := exec.Command(executable, "job", "output", "fix", "1", "--follow")
	pipe, err := follower.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := follower.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if follower.ProcessState == nil {
			follower.Process.Kill()
			follower.Wait()
		}
	})
	first := make([]byte, 5)
	if _, err := io.ReadFull(pipe, first); err != nil {
		t.Fatal(err)
	}
	if string(first) != "first" || statusOf(t, h, "1").State != "running" {
		t.Fatal(string(first))
	}
	testutil.WriteFile(t, gate, "")
	rest, err := io.ReadAll(pipe)
	if err != nil {
		t.Fatal(err)
	}
	if err := follower.Wait(); err != nil {
		t.Fatal(err)
	}
	if string(rest) != "élast\n" {
		t.Fatalf("%q", rest)
	}
	h.expect(0, "job", "wait", "fix", "1", "--timeout", "5s")
}

func TestADetachedCommandWhoseSbxCannotStartExits127AndKeepsTheError(t *testing.T) {
	h, _ := jobsReady(t)
	original := startJobHolder
	startJobHolder = func(c *exec.Cmd) error {
		c.Env = append(c.Env, "PATH="+t.TempDir())
		return original(c)
	}
	h.expect(0, "exec", "--detach", "fix", "--", "true")
	h.expect(127, "job", "wait", "fix", "1", "--timeout", "5s")
	h.expect(0, "job", "output", "fix", "1")
	if !strings.Contains(h.stdout.String(), "sbx") || !strings.Contains(h.stdout.String(), "executable file not found") {
		t.Fatal(h.stdout.String())
	}
}

func TestJobReadersNeverMistakeAnotherReadersLockForAHolder(t *testing.T) {
	h, _ := jobsReady(t)
	dir := filepath.Join(h.task("fix"), "jobs", "1")
	testutil.WriteFile(t, filepath.Join(dir, "job.json"), `{"id":"1","slug":"fix","sandbox":"stage-fix","command":["example"]}`)
	testutil.WriteFile(t, filepath.Join(dir, "lock"), "")
	lock, err := os.Open(filepath.Join(dir, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	if j := statusOf(t, h, "1"); j.State != "lost" {
		t.Fatalf("%+v", j)
	}
}

func TestExecWithoutDetachCreatesNoJobsAndKeepsTheExistingInvocation(t *testing.T) {
	h, _ := putReady(t)
	h.expect(0, "exec", "fix", "--", "printf", "--detach")
	want := "exec -u agent -w " + filepath.Join(h.task("fix"), "src") + " stage-fix printf --detach"
	if h.sbx.LastCall() != want {
		t.Fatal(h.sbx.LastCall())
	}
	if _, err := os.Stat(filepath.Join(h.task("fix"), "jobs")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestDetachedExecRefusesAnAbsentSandboxOrAnEmptyCommand(t *testing.T) {
	h, _ := putReady(t)
	h.expect(2, "exec", "--detach")
	h.expect(2, "exec", "--detach", "fix", "--")
	h.sbx.SetSandboxes("")
	h.expect(1, "exec", "--detach", "fix", "--", "true")
	h.expectStderr("stage down")
	if _, err := os.Stat(filepath.Join(h.task("fix"), "jobs")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestJobHoldRefusesARecordedCommandWithoutItsInheritedExclusiveLock(t *testing.T) {
	h, _ := jobsReady(t)
	dir := filepath.Join(h.task("fix"), "jobs", "1")
	testutil.WriteFile(t, filepath.Join(dir, "job.json"), `{"id":"1","slug":"fix","sandbox":"stage-fix","command":["true"]}`)
	testutil.WriteFile(t, filepath.Join(dir, "output"), "")
	testutil.WriteFile(t, filepath.Join(dir, "lock"), "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, which := range []string{"none", "unlocked", "other"} {
		t.Run(which, func(t *testing.T) {
			command := exec.Command(executable, "job", "hold", "fix", "1")
			if which != "none" {
				path := filepath.Join(dir, "lock")
				if which == "other" {
					path = testutil.WriteFile(t, filepath.Join(t.TempDir(), "other"), "")
				}
				lock, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if which == "other" {
					if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
						t.Fatal(err)
					}
				}
				command.ExtraFiles = []*os.File{lock}
			}
			said, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(said), "requires its inherited lock") {
				t.Fatalf("%s: %s %v", which, said, err)
			}
		})
	}
	if len(h.sbx.Sessions()) != 0 {
		t.Fatal(h.sbx.Sessions())
	}
}
