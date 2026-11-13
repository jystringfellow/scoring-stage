package cli

import (
	"context"
	"errors"
	"os/exec"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
)

var (
	jobSandboxTmp    = "/tmp"
	jobStopConfirm   = 3 * time.Second
	jobSignalTimeout = 10 * time.Second
)

func jobPidFile(id string) string {
	return path.Join(jobSandboxTmp, "stage-job-"+id+".pid")
}

func jobLauncher(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = config.ShellQuote(arg)
	}
	return `set -m
shift
umask 077
` + "(exec -- " + strings.Join(quoted, " ") + `) </dev/null &
pid=$!
printf '%s\n' "$pid" > "$1.tmp" && mv "$1.tmp" "$1" || { kill -KILL -- "-$pid"; wait "$pid"; exit 127; }
wait "$pid" 2>/dev/null
exit $?
`
}

const jobSignalScript = `
[ -f "$1" ] || exit 3
IFS= read -r pgid < "$1" || exit 2
case "$pgid" in ''|*[!0-9]*|0|1) exit 2 ;; esac
case "$2" in
  term) kill -TERM -- "-$pgid" 2>/dev/null ;;
  kill) kill -KILL -- "-$pgid" 2>/dev/null ;;
  check) ;;
  *) exit 2 ;;
esac
if probe=$(LC_ALL=C kill -0 -- "-$pgid" 2>&1); then exit 1; fi
case "$probe" in *"No such process"*) ;; *) exit 2 ;; esac
rm -f "$1" || exit 2
printf 'stage-job-gone\n'
`

func signalJobGroup(parent context.Context, sandbox, pidfile, mode string) (int, error) {
	ctx, cancel := context.WithTimeout(parent, jobSignalTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "sbx", "exec", "-u", "agent", sandbox, "bash", "-c", jobSignalScript, "stage-job-signal", pidfile, mode)
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err == nil {
		if string(output) != "stage-job-gone\n" {
			return 0, errors.New("sandbox did not confirm the job process group is gone")
		}
		return 0, nil
	}
	var exit *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exit) && (exit.ExitCode() == 1 || exit.ExitCode() == 3) {
		return exit.ExitCode(), nil
	}
	return 0, err
}

func endJobCommand(sandbox, pidfile string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), jobStopGrace+jobStopConfirm+jobSignalTimeout)
	defer cancel()
	for _, phase := range []struct {
		mode string
		wait time.Duration
	}{{"term", jobStopGrace}, {"kill", jobStopConfirm}} {
		deadline := time.Now().Add(phase.wait)
		mode := phase.mode
		for {
			state, err := signalJobGroup(ctx, sandbox, pidfile, mode)
			if err != nil {
				return false
			}
			if state == 0 {
				return true
			}
			if !time.Now().Before(deadline) {
				break
			}
			// A stop can arrive before the launcher has published its group id.
			if state != 3 {
				mode = "check"
			}
			time.Sleep(min(jobPoll, time.Until(deadline)))
		}
	}
	return false
}

func endJobClient(command *exec.Cmd, done <-chan error) {
	select {
	case <-done:
		return
	default:
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		_ = command.Process.Kill()
		<-done
	}
}
