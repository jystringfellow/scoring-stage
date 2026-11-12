package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const exampleAgent = `
[agent.example]
kit = "registry.example.com/sbx/example-mixin:1.2.3"
login_files = ["/home/agent/.example/example-token"]

[agent.example.bindings.example]
apiKey = { domains = ["api.example.com"] }

[agent.example.secrets.example]
command = "cat /tmp/example-key"
`

func TestSetupSavesAnAgentTheUserDefinedAndHelpNamesIt(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), fineGrainedMode+exampleAgent)
	h.expect(0, "setup", "--agent", "example")
	if got := testutil.ReadFile(t, filepath.Join(h.home, "config.toml")); !strings.HasPrefix(got, "agents = [\"example\"]\n") {
		t.Fatalf("config.toml = %q", got)
	}
	if !strings.Contains(h.stdout.String(), "stage: agents: example") {
		t.Errorf("stdout %q", h.stdout.String())
	}
	for _, command := range []string{"setup", "up"} {
		h.stage(command, "--help")
		if !strings.Contains(h.stdout.String(), "agy, claude, codex, copilot, example") {
			t.Errorf("%s --help does not name the defined agent: %q", command, h.stdout.String())
		}
	}
}

func TestUpGivesADefinedAgentItsKitBindingAndSecret(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), fineGrainedMode+exampleAgent)
	h.expect(0, "up", h.remote.Checkout, "fix", "--agent", "example")
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	for _, want := range []string{"registry.example.com/sbx/example-mixin:1.2.3", "api.example.com", "cat /tmp/example-key"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s:\n%s", want, env)
		}
	}
}

func TestADefinedAgentsLoginFilesAreCopiedLikeABuiltInAgents(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "copy_logins = [\"example\"]\n"+fineGrainedMode+exampleAgent)
	saved := filepath.Join(h.home, "logins", "example", "example-token")
	token := h.sbx.BoxFile("example-token")

	h.expect(0, "up", h.remote.Checkout, "first", "--agent", "example")
	if !strings.Contains(h.stdout.String(), "no saved example login yet") {
		t.Fatalf("stdout %q", h.stdout.String())
	}
	testutil.WriteFile(t, token, "token-1")
	h.expect(0, "login", "first")
	if got := testutil.ReadFile(t, saved); got != "token-1" {
		t.Fatalf("saved %q", got)
	}
	os.Remove(token)
	h.expect(0, "up", h.remote.Checkout, "second", "--agent", "example")
	if got := testutil.ReadFile(t, token); got != "token-1" {
		t.Fatalf("restored %q", got)
	}
	if want := "/home/agent/.example/example-token"; !h.anyCallContaining(want) {
		t.Errorf("no call writing %s in %v", want, h.sbx.Calls())
	}
}

func TestUpSaysOnceWhenAnAgentsSecretIsLeftOutForWantOfGh(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix", "--agent", "copilot")
	want := "agent copilot's secret copilot needs the gh CLI, which was not found"
	if got := strings.Count(h.stderr.String(), want); got != 1 {
		t.Errorf("said it %d times: %q", got, h.stderr.String())
	}
}
