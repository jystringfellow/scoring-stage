package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const twoGlobalSecrets = `{"secrets": [
	{"scope": "global", "type": "service", "name": "anthropic", "secret": "canary-anthropic-value"},
	{"scope": "global", "type": "service", "name": "openai", "secret": "canary-openai-value"},
	{"scope": "stage-other", "type": "service", "name": "copilot", "secret": "canary-scoped-value"}],
	"custom_secrets": [], "shadowed_services": [], "env_only_count": 0}`

func (h *harness) expectNoSecretValues() {
	h.t.Helper()
	if strings.Contains(h.stdout.String()+h.stderr.String(), "canary") {
		h.t.Fatalf("a secret's value was printed:\nstdout: %s\nstderr: %s", h.stdout.String(), h.stderr.String())
	}
}

func TestUpAndItsPlanStopWhileSbxHasGlobalSecretsAndSayHowToGoOn(t *testing.T) {
	h := setup(t)
	h.sbx.SetSecrets(twoGlobalSecrets)
	for _, args := range [][]string{{"up", h.remote.Checkout, "fix"}, {"up", h.remote.Checkout, "fix", "--plan"}} {
		h.expect(1, args...)
		h.expectStderr("sbx applies its global secrets to every sandbox, stage-fix included, whatever this stage's config says: anthropic, openai.")
		lines := strings.Split(strings.TrimSpace(h.stderr.String()), "\n")
		if len(lines) < 2 || !strings.Contains(lines[len(lines)-2], "--allow-global-secrets") ||
			!strings.Contains(lines[len(lines)-1], "sbx secret rm <name>") || !strings.Contains(lines[len(lines)-1], "[secrets.<name>]") {
			t.Errorf("%v: the refusal does not end with the flag and the way to scope a secret, one line each:\n%s", args, h.stderr.String())
		}
		if strings.Contains(h.stderr.String(), "copilot") {
			t.Errorf("named a secret scoped to another sandbox:\n%s", h.stderr.String())
		}
		h.expectNoSecretValues()
	}
	if h.anyCall("env create") || h.anyCallContaining("plan") {
		t.Errorf("went on despite global secrets: %v", h.sbx.Calls())
	}
}

func TestUpGoesOnWithGlobalSecretsOnlyWhenAskedAndSaysWhichReachTheSandbox(t *testing.T) {
	h := setup(t)
	h.sbx.SetSecrets(twoGlobalSecrets)
	h.expect(0, "up", h.remote.Checkout, "fix", "--allow-global-secrets")
	h.expectStderr("continuing with sbx's global secrets, which reach stage-fix whatever its config says (--allow-global-secrets): anthropic, openai")
	h.expectNoSecretValues()
	if !h.anyCall("env create") {
		t.Error("did not create the sandbox")
	}
}

func TestUpWithoutGlobalSecretsNeedsNoFlag(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	if strings.Contains(h.stderr.String(), "global secrets") {
		t.Errorf("warned without global secrets:\n%s", h.stderr.String())
	}
}

func TestTheFlagForGlobalSecretsNeverAllowsAGlobalGitHubSecret(t *testing.T) {
	h := setup(t)
	h.sbx.SetSecrets(`{"secrets": [{"scope": "global", "type": "service", "name": "github", "secret": "canary-github-value"}]}`)
	h.expect(1, "up", h.remote.Checkout, "fix", "--allow-global-secrets")
	h.expectStderr("sbx has a global github secret")
	h.expectNoSecretValues()
	if h.anyCall("env create") {
		t.Error("created a sandbox that could fall back to the global github secret")
	}
}

func TestUpNamesGlobalSecretsSafelyForATerminal(t *testing.T) {
	h := setup(t)
	h.sbx.SetSecrets(`{"secrets": [{"scope": "global", "type": "service", "name": "evil\u001b[2J", "secret": "(stored)"}]}`)
	h.expect(1, "up", h.remote.Checkout, "fix")
	if strings.Contains(h.stderr.String(), "\x1b") {
		t.Errorf("printed an escape sequence from sbx: %q", h.stderr.String())
	}
}

func TestMcpUpReportsGlobalSecretsAsSomethingOnlyThePersonCanAllow(t *testing.T) {
	h := setup(t)
	h.sbx.SetSecrets(twoGlobalSecrets)
	d := readyMcp(t, h)
	r := d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "agents": []string{"codex"}})
	toolError(t, r, "--allow-global-secrets")
	text := fmt.Sprint(r)
	for _, want := range []string{"needs_person:true", "global_secrets:[anthropic openai]", "stage up --agent=codex --allow-global-secrets -- "} {
		if !strings.Contains(text, want) {
			t.Errorf("result lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "canary") {
		t.Errorf("a secret's value reached the tool result: %s", text)
	}
	if h.anyCall("env create") {
		t.Error("created a sandbox the person had not allowed")
	}
	toolError(t, d.tool("up", map[string]any{"repo": h.remote.Checkout, "slug": "fix", "allow_global_secrets": true}), "")
}

func TestListNotesTheGlobalSecretsThatApplyToEachStageThatExists(t *testing.T) {
	h, _ := putReady(t)
	h.expect(0, "up", h.remote.Checkout, "old")
	h.sbx.SetSandboxes("stage-fix\n")
	h.sbx.SetSecrets(twoGlobalSecrets)
	for _, shallow := range [][]string{{"list", "--json", "--shallow"}, {"list", "--json"}} {
		h.expect(0, shallow...)
		listed := readListing(t, h)
		if fix := stageNamed(t, listed, "fix"); !slices.Equal(fix.GlobalSecrets, []string{"anthropic", "openai"}) {
			t.Errorf("%v: fix global secrets %v", shallow, fix.GlobalSecrets)
		}
		if old := stageNamed(t, listed, "old"); old.GlobalSecrets != nil {
			t.Errorf("%v: a gone stage has global secrets %v", shallow, old.GlobalSecrets)
		}
		h.expectNoSecretValues()
	}
}

func TestListStillListsStagesWhenItCannotReadGlobalSecrets(t *testing.T) {
	h, _ := putReady(t)
	h.sbx.SetSecrets(`{"items": []}`)
	h.expect(0, "list", "--json", "--shallow")
	h.expectStderr("cannot tell which global sbx secrets apply to stages")
	if fix := stageNamed(t, readListing(t, h), "fix"); fix.GlobalSecrets != nil {
		t.Errorf("fix global secrets %v", fix.GlobalSecrets)
	}
}

func TestHallNotesGlobalSecretsOnEachStageAndSaysWhenItCannotReadThem(t *testing.T) {
	h := readyToRead(t)
	h.sbx.SetSecrets(twoGlobalSecrets)
	hall := startHall(t, h)
	snapshot := readHall(t, hall)
	if len(snapshot.Stages) != 1 || !slices.Equal(snapshot.Stages[0].GlobalSecrets, []string{"anthropic", "openai"}) || !slices.Equal(snapshot.Stages[0].Attention, []string{"disk"}) {
		t.Errorf("stages %+v", snapshot.Stages)
	}
	hall.stop()

	h.sbx.SetSecrets(`{"items": []}`)
	hall = startHall(t, h)
	snapshot = readHall(t, hall)
	if !strings.Contains(snapshot.GlobalSecretsError, "cannot tell which global sbx secrets apply to stages") || len(snapshot.Stages) != 1 {
		t.Errorf("snapshot %+v", snapshot)
	}
}
