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
	jobStartWait     = 10 * time.Second
	jobSignalTimeout = 10 * time.Second
)

func jobPidFile(id string) string {
	return path.Join(jobSandboxTmp, "stage-job-"+id+".pid")
}

const jobGroupProgram = `my $file = shift;
$SIG{INT} = 'DEFAULT'; $SIG{QUIT} = 'DEFAULT';
setpgrp(0, 0) or do { print STDERR "stage: setpgrp: $!\n"; exit 127 };
my $f;
open($f, '>', "$file.tmp") and print($f "$$\n") and close($f) and rename("$file.tmp", $file)
  or do { print STDERR "stage: cannot publish the job's process group: $!\n"; exit 127 };
exec { $ARGV[0] } @ARGV;
print STDERR "stage: $ARGV[0]: $!\n";
exit($!{ENOENT} ? 127 : 126);
`

func jobLauncher(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = config.ShellQuote(arg)
	}
	return `shift
umask 077
command -v perl >/dev/null 2>&1 || { echo 'stage: detached jobs need perl in the sandbox' >&2; exit 127; }
` + "perl -e " + config.ShellQuote(jobGroupProgram) + ` -- "$1" ` + strings.Join(quoted, " ") + ` </dev/null &
pid=$!
wait "$pid" 2>/dev/null
exit $?
`
}

const jobSignalScript = `
[ -f "$1" ] || exit 3
IFS= read -r pgid < "$1" || exit 2
case "$pgid" in ''|*[!0-9]*|0|1) exit 2 ;; esac
case "$2" in
  term) kill -TERM "-$pgid" 2>/dev/null ;;
  kill) kill -KILL "-$pgid" 2>/dev/null ;;
  check) ;;
  *) exit 2 ;;
esac
if probe=$(LC_ALL=C kill -0 "-$pgid" 2>&1); then exit 1; fi
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
		if !jobGoneConfirmed(output) {
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
	ctx, cancel := context.WithTimeout(context.Background(), jobStartWait+jobStopGrace+jobStopConfirm+jobSignalTimeout)
	defer cancel()
	deadline := time.Now().Add(jobStartWait)
	for {
		state, err := signalJobGroup(ctx, sandbox, pidfile, "term")
		if err != nil {
			return false
		}
		if state == 0 {
			return true
		}
		if state == 1 {
			break
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(min(jobPoll, time.Until(deadline)))
	}
	for _, phase := range []struct {
		mode string
		wait time.Duration
	}{{"check", jobStopGrace}, {"kill", jobStopConfirm}} {
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
			mode = "check"
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

func jobGoneConfirmed(output []byte) bool {
	return string(output) == "stage-job-gone\n" || strings.HasSuffix(string(output), "\nstage-job-gone\n")
}
