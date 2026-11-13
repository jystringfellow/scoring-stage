package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jystringfellow/scoring-stage/internal/config"
)

var (
	jobIDPattern         = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	jobOutputLimit int64 = 1 << 30
	jobReadBuffer        = 32 * 1024
	jobPoll              = 100 * time.Millisecond
	jobStopGrace         = 5 * time.Second
	startJobHolder       = func(command *exec.Cmd) error {
		if err := command.Start(); err != nil {
			return err
		}
		return command.Process.Release()
	}
)

type jobRecord struct {
	ID        string   `json:"id"`
	Slug      string   `json:"slug"`
	Sandbox   string   `json:"sandbox"`
	Command   []string `json:"command"`
	StartedAt string   `json:"started_at"`
}

type jobExit struct {
	Status          *int   `json:"status,omitempty"`
	Stopped         bool   `json:"stopped,omitempty"`
	EndedAt         string `json:"ended_at"`
	OutputTruncated bool   `json:"output_truncated"`
}

type jobStatus struct {
	jobRecord
	State           string `json:"state"`
	Status          *int   `json:"status,omitempty"`
	EndedAt         string `json:"ended_at,omitempty"`
	OutputBytes     int64  `json:"output_bytes"`
	OutputTruncated bool   `json:"output_truncated"`
}

func jobDir(home, slug, id string) string { return filepath.Join(taskDir(home, slug), "jobs", id) }

func writeJobJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writePrivate(path, string(data)+"\n")
}

func startJob(home string, task *Task, argv []string) (string, error) {
	parent := filepath.Join(taskDir(home, task.Slug), "jobs")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	partial, err := os.MkdirTemp(parent, ".starting-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(partial)
	lock, err := os.OpenFile(filepath.Join(partial, "lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(partial, "output"), nil, 0600); err != nil {
		return "", err
	}
	var id, dir string
	for n := 1; n <= 999999999; n++ {
		id = strconv.Itoa(n)
		dir = jobDir(home, task.Slug, id)
		record := jobRecord{ID: id, Slug: task.Slug, Sandbox: task.Sandbox, Command: argv, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := writeJobJSON(filepath.Join(partial, "job.json"), record); err != nil {
			return "", err
		}
		if err := os.Rename(partial, dir); err == nil {
			break
		} else {
			if _, existsErr := os.Stat(dir); existsErr != nil {
				return "", err
			}
			if n == 999999999 {
				return "", errorf("no job ids available")
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	command := exec.Command(executable, "job", "hold", task.Slug, id)
	command.Env = append(os.Environ(), "STAGE_HOME="+home)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.ExtraFiles = []*os.File{lock}
	if err := startJobHolder(command); err != nil {
		return "", err
	}
	return id, nil
}

func readJob(home, slug, id string) (jobStatus, error) {
	var result jobStatus
	if !jobIDPattern.MatchString(id) {
		return result, errorf("no job %s in %s", config.Printable(id), slug)
	}
	dir := jobDir(home, slug, id)
	data, err := os.ReadFile(filepath.Join(dir, "job.json"))
	if os.IsNotExist(err) {
		return result, errorf("no job %s in %s", id, slug)
	}
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result.jobRecord); err != nil {
		return result, err
	}
	if result.ID != id || result.Slug != slug {
		return result, errorf("job %s in %s has an invalid record", id, slug)
	}
	finish := func() (bool, error) {
		data, err := os.ReadFile(filepath.Join(dir, "exit.json"))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		var ended jobExit
		if err := json.Unmarshal(data, &ended); err != nil {
			return false, err
		}
		if !ended.Stopped && (ended.Status == nil || *ended.Status < 0 || *ended.Status > 255) {
			return false, errorf("job %s has an invalid exit record", id)
		}
		result.State = "exited"
		result.Status = ended.Status
		result.EndedAt = ended.EndedAt
		result.OutputTruncated = ended.OutputTruncated
		if ended.Stopped {
			result.State = "stopped"
			result.Status = nil
		}
		return true, nil
	}
	done, err := finish()
	if err != nil {
		return result, err
	}
	if !done {
		lock, err := os.Open(filepath.Join(dir, "lock"))
		if err != nil {
			return result, err
		}
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		switch {
		case err == nil:
			done, err = finish()
			if err == nil && !done {
				result.State = "lost"
			}
		case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN):
			result.State = "running"
			err = nil
		}
		lock.Close()
		if err != nil {
			return result, err
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "output")); err == nil {
		result.OutputBytes = info.Size()
	} else if !os.IsNotExist(err) {
		return result, err
	}
	return result, nil
}

func listJobs(home, slug string) ([]jobStatus, error) {
	jobs := []jobStatus{}
	entries, err := os.ReadDir(filepath.Join(taskDir(home, slug), "jobs"))
	if os.IsNotExist(err) {
		return jobs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !jobIDPattern.MatchString(entry.Name()) {
			return nil, errorf("invalid job directory %s", config.Printable(entry.Name()))
		}
		job, err := readJob(home, slug, entry.Name())
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { a, _ := strconv.Atoi(jobs[i].ID); b, _ := strconv.Atoi(jobs[j].ID); return a < b })
	return jobs, nil
}

func (a *App) cmdDetachedExec(args []string, home string) (int, error) {
	if len(args) > 0 && args[0] == "--detach" {
		args = args[1:]
	} else {
		args = append([]string{args[0]}, args[2:]...)
	}
	if len(args) == 0 {
		return 0, &usageError{"the following arguments are required: slug"}
	}
	task, err := loadTask(home, args[0])
	if err != nil {
		return 0, err
	}
	argv := args[1:]
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return 0, &usageError{"usage: stage exec --detach <slug> -- <command> [args...]"}
	}
	id, err := startDetached(home, task, argv)
	if err != nil {
		return 0, err
	}
	fmt.Fprintln(a.Stdout, id)
	fmt.Fprintf(a.Stderr, "stage: stage job wait %s %s\n", task.Slug, id)
	return 0, nil
}

func startDetached(home string, task *Task, argv []string) (string, error) {
	if err := requireSandbox(task); err != nil {
		return "", err
	}
	return startJob(home, task, argv)
}

func (a *App) cmdJob(args []string, home string) (int, error) {
	if len(args) == 0 {
		return 0, &usageError{"usage: stage job list|status|wait|output|stop <slug> [<id>]"}
	}
	action := args[0]
	if action == "hold" {
		if len(args) != 3 {
			return 0, errorf("job hold requires its inherited lock")
		}
		return 0, holdJob(home, args[1], args[2])
	}
	switch action {
	case "list", "status", "wait", "output", "stop":
	default:
		return 0, &usageError{"unknown job action: " + config.Printable(action)}
	}
	fs := a.newFlags("job "+action, "<slug> <id> [options]")
	var asJSON, follow bool
	var timeout time.Duration
	if action == "list" || action == "status" || action == "wait" {
		fs.BoolVar(&asJSON, "json", false, "print job state as JSON")
	}
	if action == "output" {
		fs.BoolVar(&follow, "follow", false, "read until the job finishes")
	}
	if action == "wait" {
		fs.DurationVar(&timeout, "timeout", 0, "maximum wait (0 waits without limit)")
	}
	names := []string{"slug", "id"}
	if action == "list" {
		names = names[:1]
	}
	positional, err := fs.parse(args[1:], names...)
	if err != nil {
		return 0, err
	}
	if timeout < 0 {
		return 0, &usageError{"timeout must not be negative"}
	}
	task, err := loadTask(home, positional[0])
	if err != nil {
		return 0, err
	}
	if action == "list" {
		jobs, err := listJobs(home, task.Slug)
		if err != nil {
			return 0, err
		}
		if asJSON {
			return 0, json.NewEncoder(a.Stdout).Encode(jobs)
		}
		for _, j := range jobs {
			cmd := []rune(config.Printable(strings.Join(j.Command, " ")))
			if len(cmd) > 160 {
				cmd = append(cmd[:160], []rune("...")...)
			}
			state := j.State
			if j.Status != nil {
				state += fmt.Sprintf(" with status %d", *j.Status)
			}
			fmt.Fprintf(a.Stdout, "%s  %s  %s\n", j.ID, state, string(cmd))
		}
		return 0, nil
	}
	id := positional[1]
	job, err := readJob(home, task.Slug, id)
	if err != nil {
		return 0, err
	}
	switch action {
	case "status":
		return 0, a.printJob(job, asJSON)
	case "wait":
		job, err = waitJob(home, task.Slug, id, timeout)
		if err != nil {
			return 0, err
		}
		if err := a.printJob(job, asJSON); err != nil {
			return 0, err
		}
		switch job.State {
		case "exited":
			return *job.Status, nil
		case "running":
			fmt.Fprintln(a.Stderr, "stage: job", id, "is still running (wait timed out)")
			return 124, nil
		default:
			fmt.Fprintln(a.Stderr, "stage: job", id, "is", job.State)
			return 125, nil
		}
	case "output":
		return 0, a.outputJob(home, task.Slug, id, follow)
	case "stop":
		job, err = stopJob(home, task.Slug, id)
		if err != nil {
			return 0, err
		}
		if job.State == "exited" {
			return 0, a.printJob(job, false)
		}
		a.say("job %s stopped", id)
	}
	return 0, nil
}

func stopJob(home, slug, id string) (jobStatus, error) {
	job, err := readJob(home, slug, id)
	if err != nil {
		return job, err
	}
	if job.State != "running" {
		return job, errorf("job %s is %s", id, job.State)
	}
	if err := os.WriteFile(filepath.Join(jobDir(home, slug, id), "stop"), nil, 0600); err != nil {
		return job, err
	}
	job, err = waitJob(home, slug, id, jobStopGrace+5*time.Second)
	if err != nil {
		return job, err
	}
	if job.State == "running" {
		return job, errorf("job %s is still running after its stop request", id)
	}
	if job.State == "lost" {
		return job, errorf("job %s is lost; its command may still be running or may have stopped", id)
	}
	return job, nil
}

func (a *App) printJob(job jobStatus, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(a.Stdout).Encode(job)
	}
	fmt.Fprintf(a.Stdout, "id: %s\nslug: %s\nsandbox: %s\nstate: %s\n", job.ID, config.Printable(job.Slug), config.Printable(job.Sandbox), job.State)
	if job.Status != nil {
		fmt.Fprintf(a.Stdout, "status: %d\n", *job.Status)
	}
	fmt.Fprintf(a.Stdout, "command: %s\nstarted_at: %s\n", config.Printable(strings.Join(job.Command, " ")), config.Printable(job.StartedAt))
	if job.EndedAt != "" {
		fmt.Fprintf(a.Stdout, "ended_at: %s\n", config.Printable(job.EndedAt))
	}
	fmt.Fprintf(a.Stdout, "output_bytes: %d\noutput_truncated: %t\n", job.OutputBytes, job.OutputTruncated)
	if job.State == "lost" {
		fmt.Fprintln(a.Stdout, "The holder is gone; its command may still be running or may have stopped.")
	}
	return nil
}

func waitJob(home, slug, id string, timeout time.Duration) (jobStatus, error) {
	deadline := time.Now().Add(timeout)
	for {
		job, err := readJob(home, slug, id)
		if err != nil || job.State != "running" || timeout > 0 && !time.Now().Before(deadline) {
			return job, err
		}
		pause := jobPoll
		if timeout > 0 {
			pause = min(pause, time.Until(deadline))
		}
		time.Sleep(pause)
	}
}

type jobWriter struct {
	file      *os.File
	remaining int64
	truncated bool
}

func (w *jobWriter) Write(p []byte) (int, error) {
	n := len(p)
	kept := min(int64(n), w.remaining)
	if kept > 0 {
		written, err := w.file.Write(p[:kept])
		w.remaining -= int64(written)
		if err != nil {
			return written, err
		}
	}
	if int64(n) > kept {
		w.truncated = true
	}
	return n, nil
}

func holdJob(home, slug, id string) error {
	syscall.CloseOnExec(3)
	lock := os.NewFile(3, "job lock")
	if lock == nil {
		return errorf("job hold requires its inherited lock")
	}
	defer lock.Close()
	if _, err := checkSlug(slug); err != nil {
		return err
	}
	if !jobIDPattern.MatchString(id) {
		return errorf("job hold requires its inherited lock")
	}
	dir := jobDir(home, slug, id)
	inherited, err := lock.Stat()
	if err != nil {
		return errorf("job hold requires its inherited lock")
	}
	expected, err := os.Stat(filepath.Join(dir, "lock"))
	if err != nil || !os.SameFile(inherited, expected) {
		return errorf("job hold requires its inherited lock")
	}
	// Prove fd 3 holds the exclusive lock, rather than merely naming the file.
	probe, err := os.Open(filepath.Join(dir, "lock"))
	if err != nil {
		return err
	}
	held := syscall.Flock(int(probe.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	probe.Close()
	if !errors.Is(held, syscall.EWOULDBLOCK) && !errors.Is(held, syscall.EAGAIN) {
		return errorf("job hold requires its inherited lock")
	}
	if err := syscall.Flock(3, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errorf("job hold requires its inherited lock")
	}
	task, err := loadTask(home, slug)
	if err != nil {
		return err
	}
	var record jobRecord
	data, err := os.ReadFile(filepath.Join(dir, "job.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	if record.ID != id || record.Slug != slug || record.Sandbox != task.Sandbox || len(record.Command) == 0 {
		return errorf("job hold: invalid record")
	}
	output, err := os.OpenFile(filepath.Join(dir, "output"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	writer := &jobWriter{file: output, remaining: jobOutputLimit}
	command := exec.Command("sbx", append([]string{"exec", "-u", "agent", "-w", task.RepoDir, task.Sandbox}, record.Command...)...)
	command.Stdout, command.Stderr = writer, writer
	command.WaitDelay = time.Second
	ended := jobExit{}
	if err := command.Start(); err != nil {
		fmt.Fprintf(writer, "stage: %s\n", err)
		code := 127
		ended.Status = &code
	} else {
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		ticker := time.NewTicker(jobPoll)
		defer ticker.Stop()
		var waitErr error
	waiting:
		for {
			select {
			case waitErr = <-done:
				break waiting
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
					// Prefer a real exit already observed over a concurrent stop request.
					select {
					case waitErr = <-done:
						break waiting
					default:
					}
					ended.Stopped = true
					_ = command.Process.Signal(syscall.SIGTERM)
					timer := time.NewTimer(jobStopGrace)
					select {
					case waitErr = <-done:
						if !timer.Stop() {
							<-timer.C
						}
					case <-timer.C:
						_ = command.Process.Kill()
						waitErr = <-done
					}
					break waiting
				}
			}
		}
		if !ended.Stopped {
			code := 0
			if waitErr != nil {
				var exit *exec.ExitError
				if errors.As(waitErr, &exit) {
					code = exit.ExitCode()
					if code < 0 {
						if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
							code = 128 + int(status.Signal())
						} else {
							code = 127
						}
					}
				} else {
					fmt.Fprintf(writer, "stage: %s\n", waitErr)
					code = 127
				}
			}
			ended.Status = &code
		}
	}
	if err := output.Sync(); err != nil {
		return err
	}
	ended.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
	ended.OutputTruncated = writer.truncated
	return writeJobJSON(filepath.Join(dir, "exit.json"), ended)
}

func escapeOutput(pending []byte, final bool) (string, []byte) {
	var escaped strings.Builder
	for len(pending) > 0 {
		if !final && !utf8.FullRune(pending) {
			break
		}
		r, size := utf8.DecodeRune(pending)
		text := string(r)
		if r == '\n' {
			text = "\n"
		} else if r == utf8.RuneError && size == 1 {
			text = fmt.Sprintf("\\x%02x", pending[0])
		} else {
			text = config.Printable(text)
		}
		escaped.WriteString(text)
		pending = pending[size:]
	}
	return escaped.String(), pending
}

func (a *App) outputJob(home, slug, id string, follow bool) error {
	file, err := os.Open(filepath.Join(jobDir(home, slug, id), "output"))
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, max(jobReadBuffer, utf8.UTFMax))
	pending := []byte{}
	chunk := make([]byte, max(jobReadBuffer, 1))
	emit := func(final bool) error {
		text, rest := escapeOutput(pending, final)
		pending = rest
		_, err := io.WriteString(a.Stdout, text)
		return err
	}
	for {
		n, readErr := reader.Read(chunk)
		pending = append(pending, chunk[:n]...)
		if err := emit(false); err != nil {
			return err
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if readErr == io.EOF {
			job, err := readJob(home, slug, id)
			if err != nil {
				return err
			}
			if !follow || job.State != "running" {
				// A terminal state is published after the last write; drain it once more.
				if follow {
					if _, err := reader.Peek(1); err == nil {
						continue
					} else if err != io.EOF {
						return err
					}
				}
				if err := emit(true); err != nil {
					return err
				}
				if job.OutputTruncated {
					fmt.Fprintln(a.Stderr, "stage: job output was truncated at its limit")
				}
				return nil
			}
			time.Sleep(jobPoll)
		}
	}
}
