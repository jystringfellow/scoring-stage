package cli

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/sbx"
	"github.com/jystringfellow/scoring-stage/internal/source"
)

var (
	unkeptScript   = `cd "$1" || exit 0; git status --porcelain | wc -l; git rev-parse HEAD`
	unkeptTimeout  = 30 * time.Second
	unkeptAnswer   = regexp.MustCompile(`^\s*([0-9]{1,9})\s+([0-9a-f]{40}|[0-9a-f]{64})\s*$`)
	startingCommit = func(task *Task) (string, error) { return source.Git(task.RepoDir, "rev-parse", "HEAD") }
)

func (a *App) warnOfUnkeptWork(task *Task) {
	answer, err := sbx.FeedWithin(unkeptTimeout, strings.NewReader(""), "exec", "-u", "agent", task.Sandbox, "sh", "-c", unkeptScript, "sh", task.RepoDir)
	if err == nil && strings.TrimSpace(answer) == "" {
		return
	}
	match := unkeptAnswer.FindStringSubmatch(answer)
	if err != nil || match == nil {
		a.warn("could not check %s for work that was not shipped; stage down removes its repository with it", task.Sandbox)
		return
	}
	var unkept []string
	if files, _ := strconv.Atoi(match[1]); files > 0 {
		unkept = append(unkept, count(files, "uncommitted or untracked file")+" in the repository")
	}
	head := match[2]
	shipped := false
	for _, ship := range task.Ships {
		shipped = shipped || ship.SHA == head
	}
	if started, err := startingCommit(task); err == nil && head != started && !shipped {
		unkept = append(unkept, "commit "+head[:8]+" that was never shipped")
	}
	if len(unkept) > 0 {
		a.warn("%s has %s; stage down removes it, and only its takes and what stage ship pushed are kept", task.Sandbox, strings.Join(unkept, " and "))
	}
}
