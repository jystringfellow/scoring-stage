package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const globalBindings = `bindings:
    anthropic:
        oauth:
            domains:
                - platform.claude.com
                - api.anthropic.com
    github:
        apiKey:
            domains:
                - api.github.com
                - github.com
    nuget-feed:
        apiKey:
            domains:
                - pkgs.example.com
    stage-broker:
        apiKey:
            domains:
                - localhost
                - localhost:9911
`

func (h *harness) expectStdout(fragments ...string) {
	h.t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(h.stdout.String(), fragment) {
			h.t.Errorf("stdout %q does not contain %q", h.stdout.String(), fragment)
		}
	}
}

func TestSetupMarksGlobalBindingsExpectedStaleOrOther(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.expect(0, "setup", "--agent", "claude")
	h.expectStdout(
		"stage: global credential bindings in "+h.credentials,
		"any kit that declares credential@1 for a bound service can send its secret to any domain the kit names",
		"  anthropic (expected): platform.claude.com, api.anthropic.com\n    stage binds this for agent claude;",
		"  github (expected): api.github.com, github.com\n    stage binds this for [github] mode \"fine-grained\";",
		"  stage-broker (stale): localhost, localhost:9911\n    stage binds only localhost:9911 for the AWS broker; sbx never drops a binding domain, so delete localhost from the stage-broker entry in "+h.credentials,
		"  nuget-feed (other): pkgs.example.com\n    stage does not bind this; if nothing needs it, delete the nuget-feed entry from "+h.credentials+" by hand, or `sbx secret rm nuget-feed`",
		"never `sbx env rm --prune-bindings` a stage sandbox",
	)
	if got := testutil.ReadFile(t, h.credentials); got != globalBindings {
		t.Errorf("credentials.yaml changed to %q", got)
	}
	if h.anyCallContaining("prune-bindings") || h.anyCall("env rm", "secret rm") {
		t.Errorf("setup changed sbx state: %v", h.sbx.Calls())
	}
}

func TestSetupCallsAnAgentsBindingOtherWhenTheAgentIsNotConfigured(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.expect(0, "setup")
	h.expectStdout("  anthropic (other): platform.claude.com, api.anthropic.com\n    stage does not bind this;")
}

func TestUpPlanAuditsGlobalBindingsWithTheSandboxsOwn(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.expect(0, "up", h.remote.Checkout, "fix", "--plan", "--agent", "claude")
	h.expectStdout("  anthropic (expected): platform.claude.com, api.anthropic.com\n    stage binds this for this sandbox;")
}

func TestSetupReportsNoBindingsWhenCredentialsYamlIsMissing(t *testing.T) {
	h := setup(t)
	h.expect(0, "setup")
	h.expectStdout("stage: global credential bindings: none (" + h.credentials + " does not exist)")
	if strings.Contains(h.stderr.String(), "credential bindings") {
		t.Errorf("stderr %q", h.stderr.String())
	}
	if _, err := os.Stat(h.credentials); !os.IsNotExist(err) {
		t.Errorf("setup created %s", h.credentials)
	}
}

func TestSetupOnlyWarnsAboutAnUnparseableCredentialsYaml(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, "bindings: [unterminated\n")
	h.expect(0, "setup")
	h.expectStderr("stage: WARNING: cannot audit global credential bindings: cannot parse " + h.credentials)
	if got := testutil.ReadFile(t, h.credentials); got != "bindings: [unterminated\n" {
		t.Errorf("credentials.yaml changed to %q", got)
	}
}

func TestSetupEscapesControlCharactersInBindings(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, "bindings:\n  \"evil\\e[2J\":\n    apiKey:\n      domains:\n        - \"bad\\u202ehost.example\"\n")
	h.expect(0, "setup")
	h.expectStdout(`  evil\x1b[2J (other): bad\x202ehost.example`, "delete the evil\\x1b[2J entry", "`sbx secret rm 'evil\\x1b[2J'`")
	if strings.ContainsAny(h.stdout.String()+h.stderr.String(), "\x1b‮") {
		t.Errorf("output kept control characters: %q", h.stdout.String())
	}
}
