package cli

import (
	"errors"
	"io"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/gear"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const gearScript = `umask 077; d=$1; case "$d" in "~/"*) d="$HOME/${d#??}";; esac; mkdir -p "$(dirname "$d")" && tar -xf - -C "$(dirname "$d")"`

var (
	gearLimits  = gear.DefaultLimits
	gearTimeout = 5 * time.Minute
)

func (a *App) surveyGear(settings *config.Settings) (gear.Survey, error) {
	survey, err := gear.Look(settings.Gear, gearLimits)
	if err != nil {
		return survey, errorf("gear: %s", config.Printable(err.Error()))
	}
	for _, missing := range survey.Missing {
		a.say("gear %s is not on this machine; passed over", config.Printable(missing.From))
	}
	return survey, nil
}

func carried(survey gear.Survey) string {
	items := make([]config.Gear, len(survey.Sources))
	for i, source := range survey.Sources {
		items[i] = source.Gear
	}
	return config.GearSummary(items)
}

func (a *App) planGear(survey gear.Survey) {
	if len(survey.Sources) > 0 {
		a.say("gear: %s (%d bytes) to copy in once the sandbox is created: %s", count(survey.Files, "file"), survey.Bytes, carried(survey))
	}
}

func (a *App) copyGear(survey gear.Survey, task *Task) error {
	if len(survey.Sources) == 0 {
		return nil
	}
	for _, source := range survey.Sources {
		if err := feedGear(source, task.Sandbox); err != nil {
			return errorf("could not copy gear to %s in %s: %s; the sandbox exists without it: run `stage down %s` to clean up",
				config.Printable(source.To), task.Sandbox, config.Printable(err.Error()), task.Slug)
		}
	}
	a.say("copied %s (%d bytes) of gear into %s: %s", count(survey.Files, "file"), survey.Bytes, task.Sandbox, carried(survey))
	if survey.Skipped > 0 {
		a.say("left %s out of the gear: stage copies only files and directories", count(survey.Skipped, "link or special file"))
	}
	return nil
}

func feedGear(source gear.Source, sandbox string) error {
	reader, writer := io.Pipe()
	archived := make(chan error, 1)
	go func() {
		err := source.Archive(writer)
		writer.CloseWithError(err)
		archived <- err
	}()
	fed := sbx.FeedWithin(gearTimeout, reader, "exec", "-i", "-u", "agent", sandbox, "sh", "-c", gearScript, "sh", source.To)
	reader.Close()
	if err := <-archived; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return fed
}
