package cli

import (
	"os"
	"path/filepath"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/gear"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

func requireSandbox(task *Task) error {
	existing, err := sbx.Sandboxes()
	if err != nil {
		return err
	}
	if !existing[task.Sandbox] {
		return errorf("%s does not exist; run stage down %s and stage up again", task.Sandbox, task.Slug)
	}
	return nil
}

func (a *App) cmdPut(args []string, home string) (int, error) {
	fs := a.newFlags("put", "<slug> <file> [<destination>]")
	positional, err := fs.parseOptional(args, 3)
	if err != nil {
		return 0, err
	}
	if len(positional) < 2 {
		return 0, &usageError{"the following arguments are required: slug, file"}
	}
	task, err := loadTask(home, positional[0])
	if err != nil {
		return 0, err
	}
	if err := putFile(a, home, task, positional[1:]); err != nil {
		return 0, errorf("put: %s", config.Printable(err.Error()))
	}
	return 0, nil
}

func putFile(a *App, home string, task *Task, args []string) error {
	settings, err := config.LoadSettings(home)
	if err != nil {
		return err
	}
	item, err := settings.CheckedPut(args[0])
	if err != nil {
		return err
	}
	info, err := os.Stat(item.HostPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errorf("%s must be a regular file", item.From)
	}
	item.To = "~/" + filepath.Base(config.ExpandUser(args[0]))
	if len(args) > 1 {
		item.To = args[1]
	}
	if err := settings.CheckPutDestination(item.To, task.RepoDir); err != nil {
		return err
	}
	survey, err := gear.Look([]config.Gear{item}, gearLimits)
	if err != nil {
		return err
	}
	defer survey.Close()
	if len(survey.Sources) != 1 {
		return errorf("%s disappeared before it could be copied", item.From)
	}
	if err := requireSandbox(task); err != nil {
		return err
	}
	if err := feedGear(survey.Sources[0], task.Sandbox, gearTimeout); err != nil {
		return errorf("could not copy file to %s: %v", item.To, err)
	}
	a.say("put %s (%d bytes) into %s at %s", config.Printable(item.From), survey.Bytes, task.Sandbox, config.Printable(item.To))
	return nil
}
