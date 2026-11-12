package cli

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/gear"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func carrying(t *testing.T, gearSetting string) (h *harness, user string) {
	t.Helper()
	h = setup(t)
	user = config.Resolve(t.TempDir())
	t.Setenv("HOME", user)
	writeGitHubSetup(t, h.home, "gear = "+gearSetting+"\n")
	return h, user
}

func (h *harness) received(name string) map[string]string {
	h.t.Helper()
	file, err := os.Open(h.sbx.BoxFile(name))
	if err != nil {
		h.t.Fatal(err)
	}
	defer file.Close()
	bodies := map[string]string{}
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return bodies
		}
		if err != nil {
			h.t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			h.t.Fatal(err)
		}
		bodies[header.Name] = string(body)
	}
}

func TestUpCopiesGearIntoTheNewSandboxOnceItIsCreatedAndSaysWhatItCopied(t *testing.T) {
	presets := filepath.Join(config.Resolve(t.TempDir()), "presets")
	testutil.WriteFile(t, filepath.Join(presets, "quiet.json"), "{}")
	h, user := carrying(t, `["~/.tool/config.toml", { from = "`+presets+`", to = "~/.tool/mine" }]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "theme = \"dark\"\n")

	h.expect(0, "up", h.remote.Checkout, "fix")

	if got := h.received("config.toml"); len(got) != 1 || got["config.toml"] != "theme = \"dark\"\n" {
		t.Errorf("the sandbox received %v for the file", got)
	}
	if got := h.received("mine"); len(got) != 2 || got["mine/quiet.json"] != "{}" {
		t.Errorf("the sandbox received %v for the directory", got)
	}
	creating, copying := h.callIndex("env create"), h.callIndex("exec -i -u agent stage-fix sh -c")
	if creating < 0 || copying < creating {
		t.Errorf("created at call %d, copied at call %d in %v", creating, copying, h.sbx.Calls())
	}
	if call := h.sbx.Calls()[copying]; !strings.HasSuffix(call, " sh ~/.tool/config.toml") || !strings.Contains(call, "tar -xf - --no-overwrite-dir") {
		t.Errorf("the copy did not unpack at its destination: %q", call)
	}
	if h.anyCallContaining("theme") {
		t.Error("a file's content was put on a command line")
	}
	said := "copied 2 files (17 bytes) of gear into stage-fix: ~/.tool/config.toml, " + presets + " -> ~/.tool/mine\n"
	up := "stage-fix is up"
	out := h.stdout.String()
	if !strings.Contains(out, said) || strings.Index(out, said) > strings.Index(out, up) {
		t.Errorf("stdout %q", out)
	}
}

func TestUpFailsAndNamesTheCleanupWhenGearCannotBeCopied(t *testing.T) {
	h, user := carrying(t, `["~/.tool/config.toml"]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "theme = \"dark\"\n")
	h.sbx.FailFeeds("tar: config.toml: Cannot open: Permission denied")

	h.expect(1, "up", h.remote.Checkout, "fix")

	h.expectStderr("could not copy gear to ~/.tool/config.toml:")
	h.expectStderr("Cannot open: Permission denied")
	h.expectStderr("stage-fix is created and may hold only part of your gear")
	h.expectStderr("`stage down fix`")
	if strings.Contains(h.stdout.String(), "is up") {
		t.Errorf("up reported a sandbox without its gear as up: %q", h.stdout.String())
	}
	if _, err := os.Stat(filepath.Join(h.task("fix"), "task.json")); err != nil {
		t.Errorf("no task record is left for stage down: %v", err)
	}
}

func TestGearOverItsLimitStopsUpBeforeTheSandboxIsCreated(t *testing.T) {
	h, user := carrying(t, `["~/.tool/config.toml"]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "theme = \"dark\"\n")
	original := gearLimits
	gearLimits = gear.Limits{Bytes: 4, Entries: 10}
	t.Cleanup(func() { gearLimits = original })

	h.expect(1, "up", h.remote.Checkout, "fix")

	h.expectStderr("gear: the gear is larger than 4 bytes")
	if h.anyCall("env create", "exec") {
		t.Errorf("up went on to sbx: %v", h.sbx.Calls())
	}
}

func TestGearThatIsNotOnThisMachineIsPassedOverWithOneLine(t *testing.T) {
	h, _ := carrying(t, `["~/.tool/absent.toml"]`)

	h.expect(0, "up", h.remote.Checkout, "fix")

	if out := h.stdout.String(); strings.Count(out, "gear ~/.tool/absent.toml is not on this machine; passed over\n") != 1 || strings.Contains(out, "copied") {
		t.Errorf("stdout %q", out)
	}
	if h.anyCall("exec") {
		t.Errorf("copied nothing, yet reached into the sandbox: %v", h.sbx.Calls())
	}
}

func TestLinksLeftOutOfGearAreSaid(t *testing.T) {
	h, user := carrying(t, `["~/.tool"]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "x")
	testutil.WriteFile(t, filepath.Join(user, "elsewhere"), "not listed")
	if err := os.Symlink(filepath.Join(user, "elsewhere"), filepath.Join(user, ".tool", "link")); err != nil {
		t.Fatal(err)
	}

	h.expect(0, "up", h.remote.Checkout, "fix")

	if !strings.Contains(h.stdout.String(), "left 1 link or special file out of the gear: stage copies only files and directories") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	for name, body := range h.received(".tool") {
		if name == ".tool/link" || body == "not listed" {
			t.Errorf("the sandbox received %s = %q", name, body)
		}
	}
}

func TestPlanAndSetupShowTheGearWithoutCopyingIt(t *testing.T) {
	h, user := carrying(t, `["~/.tool/config.toml", { from = "~/notes.md", to = "/srv/notes.md" }]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "theme = \"dark\"\n")

	h.expect(0, "up", h.remote.Checkout, "fix", "--plan")
	for _, want := range []string{
		"gear ~/notes.md is not on this machine; passed over\n",
		"gear: 1 file (15 bytes) to copy in once the sandbox is created: ~/.tool/config.toml\n",
	} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("plan output lacks %q:\n%s", want, h.stdout.String())
		}
	}

	h.expect(0, "setup")
	if want := "gear, copied into every new sandbox for the agent to read: ~/.tool/config.toml, ~/notes.md -> /srv/notes.md\n"; !strings.Contains(h.stdout.String(), want) {
		t.Errorf("setup output lacks %q:\n%s", want, h.stdout.String())
	}
	if h.anyCall("exec", "env create") {
		t.Errorf("plan or setup reached into a sandbox: %v", h.sbx.Calls())
	}
}

func TestARepositoryThatListsGearStopsUpBeforeTheSandboxIsCreated(t *testing.T) {
	h, user := carrying(t, `[]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "theme = \"dark\"\n")
	h.commitToMain(map[string]string{config.RepoConfigPath: "gear = [\"~/.tool/config.toml\"]\n"})

	h.expect(1, "up", h.remote.Checkout, "fix")

	h.expectStderr("a repository cannot list gear")
	if h.anyCall("env create", "exec") {
		t.Errorf("up went on to sbx: %v", h.sbx.Calls())
	}
}

func TestManagedSettingsThatRefuseGearStopUpAndShowInSetupButLeaveDownWorking(t *testing.T) {
	h, user := carrying(t, `["~/.tool/config.toml"]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "theme = \"dark\"\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	h.sbx.SetSandboxes("stage-fix\n")
	testutil.WriteFile(t, h.managed, "allow_gear = false\n")
	before := len(h.sbx.Calls())

	h.expect(1, "up", h.remote.Checkout, "other")
	h.expectStderr("gear is refused by " + h.managed)
	for _, call := range h.sbx.Calls()[before:] {
		if strings.HasPrefix(call, "env create") || strings.HasPrefix(call, "exec") {
			t.Errorf("up went on to sbx: %q", call)
		}
	}

	h.expect(0, "setup")
	h.expectStderr("gear is refused by " + h.managed)
	h.expectStderr("stage up will refuse")

	h.expect(0, "list")
	h.expect(0, "down", "fix")
	if !h.anyCall("env rm") {
		t.Error("down did not remove the sandbox")
	}
}

func TestUpFailsWhenSbxExitsCleanlyWithoutUnpackingTheGear(t *testing.T) {
	for name, body := range map[string]string{"a small file": "x", "a file larger than a pipe holds": strings.Repeat("x", 1<<20)} {
		h, user := carrying(t, `["~/.tool/config.toml"]`)
		testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), body)
		h.sbx.IgnoreFeeds()

		h.expect(1, "up", h.remote.Checkout, "fix")

		if !strings.Contains(h.stderr.String(), "could not copy gear to ~/.tool/config.toml") || !strings.Contains(h.stderr.String(), "sbx finished without") {
			t.Errorf("%s: stderr %q", name, h.stderr.String())
		}
		if strings.Contains(h.stdout.String(), "copied") {
			t.Errorf("%s: up said it copied gear the sandbox never unpacked: %q", name, h.stdout.String())
		}
	}
}

func TestUpStopsAGearCopyThatDoesNotFinishInTime(t *testing.T) {
	h, user := carrying(t, `["~/.tool/config.toml", "~/.tool/other.toml"]`)
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "x")
	testutil.WriteFile(t, filepath.Join(user, ".tool", "other.toml"), "y")
	h.sbx.HangFeeds()
	original := gearTimeout
	gearTimeout = 300 * time.Millisecond
	t.Cleanup(func() { gearTimeout = original })

	started := time.Now()
	h.expect(1, "up", h.remote.Checkout, "fix")

	h.expectStderr("the copy of all gear did not finish within 300ms")
	h.expectStderr("`stage down fix`")
	if took := time.Since(started); took > 20*time.Second {
		t.Errorf("up waited %s for an sbx that never read its input", took)
	}
}

func TestGearThatWouldLandInTheWorkspaceStopsUpBeforeTheSandboxIsCreated(t *testing.T) {
	h := setup(t)
	user := config.Resolve(t.TempDir())
	t.Setenv("HOME", user)
	writeGitHubSetup(t, h.home, `gear = [{ from = "~/.tool", to = "`+filepath.Join(h.task("fix"), "src", ".tool")+`" }]`+"\n")
	testutil.WriteFile(t, filepath.Join(user, ".tool", "config.toml"), "x")

	for _, args := range [][]string{{"up", h.remote.Checkout, "fix"}, {"up", h.remote.Checkout, "fix", "--plan"}} {
		h.expect(1, args...)
		h.expectStderr("is in the sandbox's workspace")
	}
	if h.anyCall("env create", "env plan", "exec") {
		t.Errorf("up went on to sbx: %v", h.sbx.Calls())
	}
}
