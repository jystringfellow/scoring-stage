package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/sbx"
)

const diskNearlyFullPercent = 90

var (
	diskTimeout     = 20 * time.Second
	listDiskTimeout = 5 * time.Second
	rootDiskSize    = regexp.MustCompile(`^[1-9][0-9]{0,3}g$`)
	dfLine          = regexp.MustCompile(`^.*?\s([0-9]{1,18})\s+([0-9]{1,18})\s+([0-9]{1,18})\s+(100|[0-9]{1,2})%\s+(/.*)$`)
)

type diskUse struct {
	Mount       string
	UsedPercent int
	AvailableKB int64
}

func (d diskUse) free() string {
	return fmt.Sprintf("%.1f GiB", float64(d.AvailableKB)/(1<<20))
}

func mountHolding(mount, repoDir string) bool {
	return mount == "/" || mount == repoDir || strings.HasPrefix(repoDir, mount+"/")
}

func sandboxDisk(task *Task, within time.Duration) []diskUse {
	output, err := sbx.FeedWithin(within, strings.NewReader(""), "exec", "-u", "agent", task.Sandbox, "df", "-Pk", "/", task.RepoDir)
	if err != nil {
		return nil
	}
	var use []diskUse
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		match := dfLine.FindStringSubmatch(line)
		if match == nil || seen[match[5]] || !mountHolding(match[5], task.RepoDir) {
			continue
		}
		seen[match[5]] = true
		percent, _ := strconv.Atoi(match[4])
		available, _ := strconv.ParseInt(match[3], 10, 64)
		use = append(use, diskUse{Mount: config.Printable(match[5]), UsedPercent: percent, AvailableKB: available})
		if len(use) == 2 {
			break
		}
	}
	return use
}

func listedAsRunning(line, sandbox string) bool {
	fields := strings.Fields(line)
	return len(fields) > 1 && fields[0] == sandbox && slices.Contains(fields[1:], "running")
}

func sandboxIsRunning(sandbox string) bool {
	output, err := sbx.Output("ls")
	if err != nil {
		return false
	}
	return slices.ContainsFunc(strings.Split(output, "\n"), func(line string) bool { return listedAsRunning(line, sandbox) })
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
