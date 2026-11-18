package cli

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

func wakeAssertion(goos string, pid int, look func(string) (string, error)) []string {
	switch goos {
	case "darwin":
		if tool, err := look("caffeinate"); err == nil {
			return []string{tool, "-i", "-w", strconv.Itoa(pid)}
		}
	case "linux":
		if tool, err := look("systemd-inhibit"); err == nil {
			return []string{tool, "--what=sleep:idle", "--who=stage", "--why=a detached stage job is running", "--mode=block",
				"tail", "--pid=" + strconv.Itoa(pid), "-f", "/dev/null"}
		}
	}
	return nil
}

func holdHostAwake() (release func()) {
	argv := wakeAssertion(runtime.GOOS, os.Getpid(), exec.LookPath)
	if argv == nil {
		return func() {}
	}
	command := exec.Command(argv[0], argv[1:]...)
	if err := command.Start(); err != nil {
		return func() {}
	}
	return func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
}
