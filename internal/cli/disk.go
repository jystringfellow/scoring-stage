package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const diskNearlyFullPercent = 90

var (
	diskTimeout = 20 * time.Second
	dfLine      = regexp.MustCompile(`^.*?\s([0-9]{1,18})\s+([0-9]{1,18})\s+([0-9]{1,18})\s+(100|[0-9]{1,2})%\s+(/.*)$`)
)

type diskUse struct {
	Mount       string
	UsedPercent int
	AvailableKB int64
}

func (d diskUse) free() string {
	return fmt.Sprintf("%.1f GiB", float64(d.AvailableKB)/(1<<20))
}

func sandboxDisk(task *Task) []diskUse {
	output, err := sbx.FeedWithin(diskTimeout, strings.NewReader(""), "exec", "-u", "agent", task.Sandbox, "df", "-Pk", "/", task.RepoDir)
	if err != nil {
		return nil
	}
	var use []diskUse
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		match := dfLine.FindStringSubmatch(line)
		if match == nil || seen[match[5]] {
			continue
		}
		seen[match[5]] = true
		percent, _ := strconv.Atoi(match[4])
		available, _ := strconv.ParseInt(match[3], 10, 64)
		use = append(use, diskUse{Mount: config.Printable(match[5]), UsedPercent: percent, AvailableKB: available})
	}
	return use
}

func describeDisk(use []diskUse) string {
	var parts []string
	for _, d := range use {
		parts = append(parts, fmt.Sprintf("%s %d%% used (%s free)", d.Mount, d.UsedPercent, d.free()))
	}
	return strings.Join(parts, ", ")
}

func (a *App) warnOfFullDisk(task *Task, use []diskUse) {
	for _, d := range use {
		if d.UsedPercent >= diskNearlyFullPercent {
			a.warn("%s has %s left at %s; a full disk fails the job", task.Sandbox, d.free(), d.Mount)
		}
	}
}
