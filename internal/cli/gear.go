package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/gear"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const gearUnpacked = "stage-gear-unpacked"

const gearScript = `umask 077; d=$1; case "$d" in "~/"*) d="$HOME/${d#??}";; esac; mkdir -p "$(dirname "$d")" && tar -xf - --no-overwrite-dir -C "$(dirname "$d")" && echo ` + gearUnpacked

var (
	gearLimits  = gear.DefaultLimits
	gearTimeout = 5 * time.Minute
)

func (a *App) surveyGear(settings *config.Settings, workspace string) (gear.Survey, error) {
	checked, err := settings.CheckedGear()
	if err != nil {
		return gear.Survey{}, err
	}
	for _, item := range checked {
		if config.GearLandsIn(item, workspace) {
			return gear.Survey{}, errorf("gear: to %s is in the sandbox's workspace (%s) or holds it, where stage ship would find your files among the agent's work; copy it to a place outside the repository",
				config.Printable(item.To), workspace)
		}
	}
	survey, err := gear.Look(checked, gearLimits)
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
	deadline := time.Now().Add(gearTimeout)
	for _, source := range survey.Sources {
		err := feedGear(source, task.Sandbox, time.Until(deadline))
		if err != nil && !time.Now().Before(deadline) {
			err = fmt.Errorf("the copy of all gear did not finish within %s", gearTimeout)
		}
		if err != nil {
			return errorf("could not copy gear to %s: %s; %s is created and may hold only part of your gear: fix that, then run `stage down %s` and stage up again",
				config.Printable(source.To), config.Printable(err.Error()), task.Sandbox, task.Slug)
		}
	}
	a.say("copied %s (%d bytes) of gear into %s: %s", count(survey.Files, "file"), survey.Bytes, task.Sandbox, carried(survey))
	if survey.Skipped > 0 {
		a.say("left %s out of the gear: stage copies only files and directories", count(survey.Skipped, "link or special file"))
	}
	return nil
}

func feedGear(source gear.Source, sandbox string, within time.Duration) error {
	reader, writer := io.Pipe()
	archived := make(chan error, 1)
	go func() {
		err := source.Archive(writer)
		writer.CloseWithError(err)
		archived <- err
	}()
	said, fed := sbx.FeedWithin(within, reader, "exec", "-i", "-u", "agent", sandbox, "sh", "-c", gearScript, "sh", source.To)
	reader.Close()
	archiving := <-archived
	cutShort := errors.Is(archiving, io.ErrClosedPipe)
	switch {
	case archiving != nil && !cutShort:
		return archiving
	case fed != nil:
		return fed
	case cutShort || !strings.Contains(said, gearUnpacked):
		return errors.New("sbx finished without reading all of it and confirming that the sandbox unpacked it")
	}
	return nil
}
