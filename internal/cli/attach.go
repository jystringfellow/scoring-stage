package cli

import (
	"fmt"
	"regexp"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

var tmuxName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,63}$`)

func (a *App) cmdAttach(args []string, home string) (int, error) {
	fs := a.newFlags("attach", "<slug> <terminal> [--watch]")
	watch := fs.Bool("watch", false, "attach read-only, to see the agent's terminal without typing into it")
	positional, err := fs.parse(args, "slug", "terminal")
	if err != nil {
		return 0, err
	}
	slug, terminal := positional[0], positional[1]
	if !caoTerminalID.MatchString(terminal) {
		return 0, &usageError{"terminal must be the 8-character CAO terminal id that `stage list --json` shows: " + printable(terminal)}
	}
	task, err := loadTask(home, slug)
	if err != nil {
		return 0, err
	}
	if !a.IsTerminal() {
		return 0, errorf("stage attach needs a terminal")
	}
	if running, err := sandboxRunning(task.Sandbox); err != nil {
		return 0, err
	} else if !running {
		return 0, errorf("%s is not running, so no agent in it has a terminal to attach to", printable(slug))
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	plan := hallPlan(settings.Hall)
	plan.disk = false
	record, read := readStage(task, plan)
	if record.Error != "" {
		return 0, errorf("cannot read %s: %s", printable(slug), record.Error)
	}
	session, found := caoTerminalIn(read.sessions, terminal)
	if !found {
		return 0, errorf("no CAO terminal %s is running in %s; `stage list --json` shows its sessions", terminal, printable(slug))
	}
	if session.tmux == "" {
		return 0, errorf("CAO did not report a tmux session and window for terminal %s that stage can pass on safely", terminal)
	}
	tmux := []string{"tmux", "attach-session"}
	if *watch {
		tmux = append(tmux, "-r")
		fmt.Fprintf(a.Stderr, "stage: watching %s in %s, read-only; your tmux detach keys (Ctrl-b d by default) leave it\n", session.Name, printable(slug))
	} else {
		fmt.Fprintf(a.Stderr, "stage: attaching to %s in %s; your tmux detach keys (Ctrl-b d by default) leave it, and Ctrl-C goes to the agent\n", session.Name, printable(slug))
	}
	tmux = append(tmux, "-t", session.tmux)
	return runInteractive("sbx", append([]string{"exec", "-it", "-u", "agent", "-w", task.RepoDir, task.Sandbox}, tmux...)...)
}

func sandboxRunning(name string) (bool, error) {
	listed, err := sbx.List()
	if err != nil {
		return false, err
	}
	for _, sandbox := range listed {
		if sandbox.Name == name {
			return sandbox.Status == "running", nil
		}
	}
	return false, nil
}

func caoTerminalIn(sessions sessionsRecord, id string) (sessionRecord, bool) {
	for _, session := range sessions.Items {
		if session.Source == "cao" && session.ID == id {
			return session, true
		}
	}
	return sessionRecord{}, false
}
