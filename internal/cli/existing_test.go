package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestUpOnAnExistingSandboxChangesNothingAndSaysHowToPickUpChangedCredentials(t *testing.T) {
	h := setup(t)
	h.sbx.SetSandboxes("stage-fix\n")
	before := len(h.sbx.Calls())
	h.expect(1, "up", h.remote.Checkout, "fix")
	for _, fragment := range []string{
		"sandbox stage-fix already exists",
		"stage up does not change a sandbox that exists",
		"secrets, bindings, kits and gear it was created with",
		"stage secrets refresh fix",
		"stage take fix",
		"stage down fix",
	} {
		h.expectStderr(fragment)
	}
	for _, call := range h.sbx.Calls()[before:] {
		if !strings.HasPrefix(call, "version") && !strings.HasPrefix(call, "ls") && !strings.HasPrefix(call, "policy ls") && !strings.HasPrefix(call, "secret ls") {
			t.Errorf("stage up on an existing sandbox called sbx %q", call)
		}
	}
}

func TestSecretsRefreshSetsEachSecretAgainForTheLiveSandboxFromItsEnvFile(t *testing.T) {
	h := setup(t)
	h.expect(0, "up", h.remote.Checkout, "fix")
	testutil.WriteFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"), "name: \"stage-fix\"\nsecrets:\n"+
		"  copilot:\n    command: \"/usr/bin/gh auth token\"\n    refresh: \"30m\"\n"+
		"  github:\n    command: \"/bin/cat /home/t/acme.token\"\n"+
		"  feed:\n    command: \"/bin/cat /books/feed\"\n")
	before := len(h.sbx.Calls())
	h.expect(0, "secrets", "refresh", "fix")
	calls := h.sbx.Calls()[before:]
	for _, want := range []string{
		"secret set copilot --sandbox stage-fix --command /usr/bin/gh auth token --refresh 30m --force --no-input",
		"secret set github --sandbox stage-fix --command /bin/cat /home/t/acme.token --force --no-input",
	} {
		found := false
		for _, call := range calls {
			found = found || call == want
		}
		if !found {
			t.Errorf("no sbx call %q in %q", want, calls)
		}
	}
	for _, call := range calls {
		if strings.Contains(call, " feed ") {
			t.Errorf("set a secret sbx secret set does not take: %q", call)
		}
	}
	h.expectStdout("copilot", "github")
	h.expectStderr("feed")
	h.expectStderr("stage down fix")
}

func TestSecretsRefreshNeedsATaskWithAnEnvFile(t *testing.T) {
	h := setup(t)
	h.expect(1, "secrets", "refresh", "nope")
	h.expectStderr("no task nope")
	h.expect(2, "secrets", "rotate", "nope")
}
