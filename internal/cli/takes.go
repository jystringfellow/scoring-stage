package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
	"github.com/jystringfellow/scoring-stage/internal/takes"
)

const takeScript = `cd / && for p; do case "$p" in "~/"*) p="$HOME/${p#??}";; esac; [ -e "$p" ] && printf '%s\0' "${p#/}"; done | tar -cf - --null -T -`

func takesDir(home, slug string) string { return filepath.Join(home, "takes", slug) }

var takeTimeout = 10 * time.Minute

func (a *App) keepTakes(home string, task *Task) error {
	if err := a.copyTakes(home, task); err != nil {
		return errorf("%s", config.Printable(err.Error()))
	}
	return nil
}

func (a *App) copyTakes(home string, task *Task) error {
	parent := takesDir(home, task.Slug)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(parent), parent} {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	partial, err := os.MkdirTemp(parent, ".partial-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(partial)

	var summary takes.Summary
	command := append([]string{"exec", "-u", "agent", task.Sandbox, "sh", "-c", takeScript, "sh"}, task.Takes...)
	err = sbx.Stream(takeTimeout, func(archive io.Reader) error {
		var err error
		summary, err = takes.Extract(archive, partial, takes.DefaultLimits)
		return err
	}, command...)
	if err != nil {
		return err
	}
	if summary.Files == 0 && summary.Skipped > 0 {
		return fmt.Errorf("%s has only %s at its takes, and stage keeps only files and directories; list the paths they point to",
			task.Sandbox, count(summary.Skipped, "link or special file"))
	}
	if summary.Files == 0 {
		a.say("%s has nothing to take", task.Sandbox)
		return nil
	}
	kept, err := publishTake(partial, parent)
	if err != nil {
		return err
	}
	a.say("kept %s (%d bytes) from %s in %s", count(summary.Files, "file"), summary.Bytes, task.Sandbox, kept)
	if summary.Skipped > 0 {
		a.say("left %s behind: stage keeps only files and directories", count(summary.Skipped, "link or special file"))
	}
	return nil
}

func publishTake(partial, parent string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for attempt := 1; ; attempt++ {
		kept := filepath.Join(parent, stamp)
		if attempt > 1 {
			kept = fmt.Sprintf("%s-%d", kept, attempt)
		}
		if _, err := os.Lstat(kept); err == nil {
			continue
		}
		return kept, os.Rename(partial, kept)
	}
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (a *App) cmdTake(args []string, home string) (int, error) {
	fs := a.newFlags("take", "<slug>")
	positional, err := fs.parse(args, "slug")
	if err != nil {
		return 0, err
	}
	task, err := loadTask(home, positional[0])
	if err != nil {
		return 0, err
	}
	if len(task.Takes) == 0 {
		return 0, errorf("%s has no takes; list sandbox paths in takes in %s, a book or the repository's %s before stage up",
			task.Sandbox, filepath.Join(home, "config.toml"), config.RepoConfigPath)
	}
	return 0, a.keepTakes(home, task)
}
