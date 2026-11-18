package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const fakeWakeTool = `#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" > "$FAKE_WAKE_STATE/$$"
exec sleep 300
`

func fakeWakeTools(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	bin := t.TempDir()
	for _, name := range []string{"caffeinate", "systemd-inhibit"} {
		path := testutil.WriteFile(t, filepath.Join(bin, name), fakeWakeTool)
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_WAKE_STATE", state)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

func heldAssertions(t *testing.T, state string) map[int]string {
	t.Helper()
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	held := map[int]string{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if syscall.Kill(pid, 0) == nil {
			held[pid] = strings.TrimSpace(testutil.ReadFile(t, filepath.Join(state, entry.Name())))
		}
	}
	return held
}

func TestADetachedJobHoldsAWakeAssertionOnTheHostUntilItEnds(t *testing.T) {
	h, holders := jobsReady(t)
	state := fakeWakeTools(t)
	gate, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(heldAssertions(t, state)) == 1 })
	holder := strconv.Itoa((*holders)[0].Process.Pid)
	want := map[string]string{
		"darwin": "caffeinate -i -w " + holder,
		"linux":  "systemd-inhibit --what=sleep:idle --who=stage --why=a detached stage job is running --mode=block tail --pid=" + holder + " -f /dev/null",
	}[runtime.GOOS]
	for _, asked := range heldAssertions(t, state) {
		if asked != want {
			t.Errorf("the holder ran %q, want %q", asked, want)
		}
	}
	if j := statusOf(t, h, "1"); j.State != "running" {
		t.Fatalf("%+v", j)
	}

	testutil.WriteFile(t, gate, "")
	h.expect(3, "job", "wait", "fix", "1", "--timeout", "5s")
	eventually(t, func() bool { return len(heldAssertions(t, state)) == 0 })
}

func TestTheHostIsKeptAwakeUntilTheLastDetachedJobEnds(t *testing.T) {
	h, _ := jobsReady(t)
	state := fakeWakeTools(t)
	firstGate, first := gated(t, h)
	secondGate, second := gated(t, h)
	h.expect(0, first...)
	h.expect(0, second...)
	eventually(t, func() bool { return len(heldAssertions(t, state)) == 2 })

	testutil.WriteFile(t, firstGate, "")
	h.expect(3, "job", "wait", "fix", "1", "--timeout", "5s")
	eventually(t, func() bool { return len(heldAssertions(t, state)) == 1 })
	if j := statusOf(t, h, "2"); j.State != "running" {
		t.Fatalf("%+v", j)
	}

	testutil.WriteFile(t, secondGate, "")
	h.expect(3, "job", "wait", "fix", "2", "--timeout", "5s")
	eventually(t, func() bool { return len(heldAssertions(t, state)) == 0 })
}

func TestAStoppedJobReleasesItsWakeAssertion(t *testing.T) {
	h, _ := jobsReady(t)
	state := fakeWakeTools(t)
	_, args := gated(t, h)
	h.expect(0, args...)
	eventually(t, func() bool { return len(heldAssertions(t, state)) == 1 })
	h.expect(0, "job", "stop", "fix", "1")
	eventually(t, func() bool { return len(heldAssertions(t, state)) == 0 })
}

func TestTheWakeAssertionFollowsTheHolderAndIsSkippedWhereTheHostHasNoToolForIt(t *testing.T) {
	found := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	for _, c := range []struct {
		goos string
		look func(string) (string, error)
		want []string
	}{
		{"darwin", found, []string{"/usr/bin/caffeinate", "-i", "-w", "4321"}},
		{"linux", found, []string{"/usr/bin/systemd-inhibit", "--what=sleep:idle", "--who=stage", "--why=a detached stage job is running", "--mode=block", "tail", "--pid=4321", "-f", "/dev/null"}},
		{"darwin", missing, nil},
		{"linux", missing, nil},
		{"freebsd", found, nil},
	} {
		if got := wakeAssertion(c.goos, 4321, c.look); !slices.Equal(got, c.want) {
			t.Errorf("wakeAssertion(%s) = %q, want %q", c.goos, got, c.want)
		}
	}
}
