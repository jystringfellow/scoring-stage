package cli

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/takes"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

type takeEntry struct {
	name, body, link string
	kind             byte
}

func takeArchive(t *testing.T, entries ...takeEntry) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		if err := writer.WriteHeader(&tar.Header{Name: e.name, Typeflag: kind, Linkname: e.link, Mode: 0o644, Size: int64(len(e.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}

func takingSandbox(t *testing.T, slug string) *harness {
	t.Helper()
	h := setup(t)
	writeGitHubSetup(t, h.home, "takes = [\"~/.tool/runs\"]\n")
	h.expect(0, "up", h.remote.Checkout, slug)
	h.sbx.SetSandboxes("stage-" + slug + "\n")
	return h
}

func (h *harness) kept(slug string) []string {
	h.t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.home, "takes", slug))
	if err != nil && !os.IsNotExist(err) {
		h.t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func (h *harness) callIndex(fragment string) int {
	for index, call := range h.sbx.Calls() {
		if strings.Contains(call, fragment) {
			return index
		}
	}
	return -1
}

func TestDownKeepsTheTakesOnTheHostBeforeRemovingTheSandbox(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/.tool/runs/one/result.json", body: "{}"}))

	h.expect(0, "down", "fix")

	kept := h.kept("fix")
	if len(kept) != 1 {
		t.Fatalf("kept %v, want one take", kept)
	}
	file := filepath.Join(h.home, "takes", "fix", kept[0], "home", "agent", ".tool", "runs", "one", "result.json")
	if got := testutil.ReadFile(t, file); got != "{}" {
		t.Fatalf("kept %q", got)
	}
	taking, removing := h.callIndex("exec -u agent stage-fix sh -c"), h.callIndex("env rm")
	if taking < 0 || removing < taking {
		t.Errorf("take at call %d, removal at call %d in %v", taking, removing, h.sbx.Calls())
	}
	if call := h.sbx.Calls()[taking]; !strings.HasSuffix(call, " sh ~/.tool/runs") {
		t.Errorf("the take did not name its paths as arguments: %q", call)
	}
	if !strings.Contains(h.stdout.String(), "kept 1 file (2 bytes) from stage-fix in "+filepath.Join(h.home, "takes", "fix", kept[0])) {
		t.Errorf("stdout %q", h.stdout.String())
	}
	if _, err := os.Stat(h.task("fix")); !os.IsNotExist(err) {
		t.Error("task dir remains")
	}
}

func TestDownLeavesTheSandboxWhenItsTakesCannotBeCopiedUnlessToldToDiscardThem(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.FailTakes("sandbox is not running")

	h.expect(1, "down", "fix")
	h.expectStderr("sandbox is not running")
	h.expectStderr("stage-fix is left in place")
	h.expectStderr("--discard-takes")
	if h.anyCall("env rm", "rm") {
		t.Fatal("removed a sandbox whose takes were not kept")
	}
	if _, err := os.Stat(h.task("fix")); err != nil {
		t.Fatal("task dir was deleted")
	}
	if kept := h.kept("fix"); len(kept) != 0 {
		t.Errorf("a failed take left %v", kept)
	}

	before := len(h.sbx.Calls())
	h.expect(0, "down", "fix", "--discard-takes")
	for _, call := range h.sbx.Calls()[before:] {
		if strings.Contains(call, "exec") {
			t.Errorf("discarding still asked the sandbox for its takes: %q", call)
		}
	}
	if !h.anyCall("env rm") {
		t.Error("the sandbox was not removed")
	}
}

func TestATakeEntryThatWouldLandOutsideItsDirectoryStopsDownAndKeepsNothing(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/ok.txt", body: "fine"}, takeEntry{name: "../../escape.txt", body: "out"}))

	h.expect(1, "down", "fix")
	h.expectStderr("outside the take")
	if kept := h.kept("fix"); len(kept) != 0 {
		t.Errorf("a refused take left %v", kept)
	}
	for _, outside := range []string{filepath.Join(h.home, "escape.txt"), filepath.Join(h.home, "takes", "escape.txt")} {
		if _, err := os.Lstat(outside); !os.IsNotExist(err) {
			t.Errorf("%s was written", outside)
		}
	}
	if h.anyCall("env rm", "rm") {
		t.Error("removed the sandbox")
	}
}

func TestLinksInATakeAreLeftBehindAndSaidSo(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.SetTakes(takeArchive(t,
		takeEntry{name: "home/agent/passwd", kind: tar.TypeSymlink, link: "/etc/passwd"},
		takeEntry{name: "home/agent/kept.txt", body: "kept"}))

	h.expect(0, "down", "fix")
	kept := h.kept("fix")
	if len(kept) != 1 {
		t.Fatalf("kept %v", kept)
	}
	if _, err := os.Lstat(filepath.Join(h.home, "takes", "fix", kept[0], "home", "agent", "passwd")); !os.IsNotExist(err) {
		t.Error("a link was kept")
	}
	if !strings.Contains(h.stdout.String(), "left 1 link or special file behind") {
		t.Errorf("stdout %q", h.stdout.String())
	}
}

func TestASandboxWithNothingToTakeIsRemovedWithoutAnEmptyTake(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.SetTakes(takeArchive(t))

	h.expect(0, "down", "fix")
	if kept := h.kept("fix"); len(kept) != 0 {
		t.Errorf("kept %v", kept)
	}
	if !strings.Contains(h.stdout.String(), "stage-fix has nothing to take") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	if !h.anyCall("env rm") {
		t.Error("the sandbox was not removed")
	}
}

func TestTakeCopiesOnDemandEachTimeAndLeavesTheSandboxUp(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/.tool/runs/result.json", body: "{}"}))

	h.expect(0, "take", "fix")
	h.expect(0, "take", "fix")
	if kept := h.kept("fix"); len(kept) != 2 {
		t.Fatalf("kept %v, want two takes", kept)
	}
	if h.anyCall("env rm", "rm") {
		t.Error("take removed the sandbox")
	}
	info, err := os.Stat(filepath.Join(h.home, "takes"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("takes directory mode %v, %v", info.Mode().Perm(), err)
	}
}

func TestTakeOnASandboxWithoutTakesSaysWhereToListThem(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.expect(1, "take", "fix")
	h.expectStderr("stage-fix has no takes")
	if h.anyCallContaining("exec") {
		t.Error("asked the sandbox for takes it does not have")
	}
}

func TestDownWithoutTakesAsksTheSandboxForNothing(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.sbx.SetSandboxes("stage-fix\n")
	h.expect(0, "down", "fix")
	if h.anyCallContaining("exec") {
		t.Errorf("down ran something in the sandbox: %v", h.sbx.Calls())
	}
}

func TestARepositorysTakesAreUsedOnlyAfterApproval(t *testing.T) {
	h := setup(t)
	h.commitToMain(map[string]string{".scoring-stage/config.toml": "takes = [\"~/.tool/runs\"]\n"})

	h.expect(1, "up", h.remote.Checkout, "one")
	h.expectStderr("asks for kits, books or takes you have not approved")
	h.expectStderr("copies ~/.tool/runs from the sandbox to your machine")
	if h.anyCall("env create") {
		t.Fatal("created a sandbox with unapproved takes")
	}

	h.expect(0, "up", h.remote.Checkout, "two", "--approve-repo-config")
	if task := testutil.ReadFile(t, filepath.Join(h.task("two"), "task.json")); !strings.Contains(task, `"~/.tool/runs"`) {
		t.Errorf("the task does not record its takes: %s", task)
	}
}

func TestATaskKeepsTheTakesItWasCreatedWithWhenTheConfigChanges(t *testing.T) {
	h := takingSandbox(t, "fix")
	writeGitHubSetup(t, h.home, "takes = [\"~/.ssh\"]\n")
	h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/.tool/runs/result.json", body: "{}"}))

	h.expect(0, "take", "fix")
	if h.anyCallContaining(".ssh") {
		t.Errorf("a later config change widened a running sandbox's takes: %v", h.sbx.Calls())
	}
}

func TestDownLeavesTheSandboxWhenItDoesNotHandOverItsTakesInTime(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.HangTakes()
	takeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { takeTimeout = 10 * time.Minute })

	started := time.Now()
	h.expect(1, "down", "fix")
	if waited := time.Since(started); waited > 10*time.Second {
		t.Errorf("waited %s for a sandbox that went quiet", waited)
	}
	h.expectStderr("did not finish within 200ms")
	h.expectStderr("stage-fix is left in place")
	if h.anyCall("env rm", "rm") {
		t.Fatal("removed a sandbox whose takes were not kept")
	}
}

func TestDownLeavesTheSandboxWhenItsTakesHoldOnlyLinks(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/.tool/runs", kind: tar.TypeSymlink, link: "/data/runs"}))

	h.expect(1, "down", "fix")
	h.expectStderr("stage-fix has only 1 link or special file at its takes")
	h.expectStderr("stage-fix is left in place")
	if h.anyCall("env rm", "rm") {
		t.Fatal("removed a sandbox whose takes were not kept")
	}
	if kept := h.kept("fix"); len(kept) != 0 {
		t.Errorf("kept %v", kept)
	}
}

func TestWhatASandboxSaysWhenATakeFailsIsEscapedBeforeItIsPrinted(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.FailTakes("gone\x1b]0;owned\x07\x1b[2J")

	h.expect(1, "take", "fix")
	if strings.ContainsAny(h.stderr.String(), "\x1b\x07") {
		t.Errorf("stderr carries the sandbox's control characters: %q", h.stderr.String())
	}
	h.expectStderr(`gone\x1b]0;owned\x07`)
}

func TestTakeMakesAnExistingTakesDirectoryPrivate(t *testing.T) {
	h := takingSandbox(t, "fix")
	h.sbx.SetTakes(takeArchive(t, takeEntry{name: "home/agent/.tool/runs/result.json", body: "{}"}))
	slugDir := filepath.Join(h.home, "takes", "fix")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Dir(slugDir), slugDir} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	h.expect(0, "take", "fix")
	for _, dir := range []string{filepath.Dir(slugDir), slugDir} {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %v, %v", dir, info.Mode().Perm(), err)
		}
	}
}

func TestTheScriptRunInTheSandboxArchivesEachListedPathOnceAndFollowsAListedLink(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(home, ".tool", "runs", "one", "result.json"), "{}")
	testutil.WriteFile(t, filepath.Join(home, ".tool", "notes.txt"), "notes")
	testutil.WriteFile(t, filepath.Join(outside, "log.txt"), "log")
	linkedTo := filepath.Join(outside, "elsewhere")
	testutil.WriteFile(t, filepath.Join(linkedTo, "kept.txt"), "linked")
	if err := os.Symlink(linkedTo, filepath.Join(home, "linked")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", takeScript, "sh", "~/.tool", "~/.tool/runs", "~/missing", "~/linked", filepath.Join(outside, "log.txt"))
	command.Env = append(os.Environ(), "HOME="+home, "COPYFILE_DISABLE=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	archive, err := command.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
	dir := t.TempDir()

	summary, err := takes.Extract(bytes.NewReader(archive), dir, takes.DefaultLimits)

	if err != nil {
		t.Fatal(err)
	}
	if summary != (takes.Summary{Files: 4, Bytes: 16}) {
		t.Errorf("summary = %+v", summary)
	}
	for _, kept := range []string{
		filepath.Join(home, ".tool", "runs", "one", "result.json"),
		filepath.Join(home, ".tool", "notes.txt"),
		filepath.Join(outside, "log.txt"),
		filepath.Join(linkedTo, "kept.txt"),
	} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s was not kept: %v", kept, err)
		}
	}
}
