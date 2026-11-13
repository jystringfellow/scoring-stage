package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func launcherCommand(t *testing.T, shell, pidfile string, argv ...string) *exec.Cmd {
	t.Helper()
	command := exec.Command(shell, "-s", "stage-job-run", pidfile)
	command.Stdin = strings.NewReader(jobLauncher(argv))
	return command
}

func commandStatus(t *testing.T, command *exec.Cmd, want int) string {
	t.Helper()
	output, err := command.CombinedOutput()
	status := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		status = exit.ExitCode()
	}
	if status != want {
		t.Fatalf("status %d, want %d: %s", status, want, output)
	}
	return string(output)
}

func TestTheJobsGroupExistsAndIsLedByTheCommandItsPidfileNamesWhenTheCommandStarts(t *testing.T) {
	slots := make(chan struct{}, 8)
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			slots <- struct{}{}
			defer func() { <-slots }()
			file := filepath.Join(t.TempDir(), "pid")
			command := launcherCommand(t, "bash", file, "sh", "-c", `p=$(cat "$1") && [ "$p" = "$$" ] && kill -0 "-$p" && [ "$(ps -o pgid= -p $$ | tr -d ' ')" = "$p" ]`, "sh", file)
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			commandStatus(t, command, 0)
		})
	}
}

func TestTheLauncherAndSignalScriptRunUnderBashAndDash(t *testing.T) {
	for _, shell := range []string{"bash", "sh", "dash"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skip(err)
			}
			file := filepath.Join(t.TempDir(), "pid")
			commandStatus(t, launcherCommand(t, shell, file, "sh", "-c", "exit 7"), 7)
			if output := commandStatus(t, launcherCommand(t, shell, file, "printf", "%s", "hello"), 0); output != "hello" {
				t.Fatal(output)
			}
			if _, err := strconv.Atoi(strings.TrimSpace(testutil.ReadFile(t, file))); err != nil {
				t.Fatal(err)
			}
			signal := func(mode string, want int) string {
				return commandStatus(t, exec.Command(shell, "-c", jobSignalScript, "stage-job-signal", file, mode), want)
			}
			if output := signal("check", 0); output != "stage-job-gone\n" {
				t.Fatal(output)
			}
			signal("check", 3)
			gate := filepath.Join(t.TempDir(), "gate")
			var output bytes.Buffer
			command := launcherCommand(t, shell, file, "sh", "-c", `while [ ! -f "$1" ]; do sleep 0.02; done`, "sh", gate)
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				testutil.WriteFile(t, gate, "")
				if err := command.Wait(); err != nil {
					t.Errorf("launcher: %v: %s", err, &output)
				}
			})
			eventually(t, func() bool { _, err := os.Stat(file); return err == nil })
			signal("check", 1)
		})
	}
}

func TestTheSignalScriptReportsNotStartedNotGoneWhenNoGroupWasPublished(t *testing.T) {
	for _, shell := range []string{"bash", "sh", "dash"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skip(err)
			}
			file := filepath.Join(t.TempDir(), "pid")
			for _, mode := range []string{"term", "kill", "check"} {
				if output := commandStatus(t, exec.Command(shell, "-c", jobSignalScript, "stage-job-signal", file, mode), 3); output != "" {
					t.Fatal(output)
				}
			}
			commandStatus(t, launcherCommand(t, "bash", file, "true"), 0)
			if output := commandStatus(t, exec.Command(shell, "-c", jobSignalScript, "stage-job-signal", file, "check"), 0); output != "stage-job-gone\n" {
				t.Fatal(output)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatal("gone group's pidfile remains:", err)
			}
			for _, value := range []string{"", "garbage", "0", "1"} {
				testutil.WriteFile(t, file, value+"\n")
				commandStatus(t, exec.Command(shell, "-c", jobSignalScript, "stage-job-signal", file, "term"), 2)
			}
		})
	}
}

func TestADetachedCommandWithoutPerlFailsWithItsReasonBeforeRunningAnything(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file, marker := filepath.Join(dir, "pid"), filepath.Join(dir, "ran")
	command := launcherCommand(t, bash, file, "/bin/sh", "-c", `echo ran > "$1"`, "sh", marker)
	command.Env = append(os.Environ(), "PATH="+t.TempDir())
	if output := commandStatus(t, command, 127); !strings.Contains(output, "detached jobs need perl in the sandbox") {
		t.Fatal(output)
	}
	for _, name := range []string{file, marker} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("%s exists: %v", name, err)
		}
	}
}

func TestADetachedCommandStartsWithDefaultInterruptAndQuitHandling(t *testing.T) {
	for _, signal := range []string{"INT", "QUIT"} {
		t.Run(signal, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "pid")
			var output bytes.Buffer
			command := launcherCommand(t, "bash", file, "sh", "-c", `kill -`+signal+` $$; echo survived`)
			command.Stdout = &output
			// QUIT must not write a core dump into the worktree.
			command.Stdin = strings.NewReader("ulimit -c 0\n" + jobLauncher([]string{"sh", "-c", `kill -` + signal + ` $$; echo survived`}))
			err := command.Run()
			want := 128 + int(syscall.SIGINT)
			if signal == "QUIT" {
				want = 128 + int(syscall.SIGQUIT)
			}
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != want || output.Len() != 0 {
				t.Fatalf("status %v, want %d, output %q", err, want, &output)
			}
		})
	}
}

func TestADetachedCommandReportsMissingAndNonExecutableFirstWordsLikeAShell(t *testing.T) {
	dir := t.TempDir()
	for i, command := range []string{filepath.Join(dir, "missing"), testutil.WriteFile(t, filepath.Join(dir, "unexecutable"), "exit 0\n")} {
		output := commandStatus(t, launcherCommand(t, "bash", filepath.Join(dir, fmt.Sprintf("pid%d", i)), command), 127-i)
		if !strings.Contains(output, command) {
			t.Fatal(output)
		}
	}
}
