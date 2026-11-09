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
    openai:
        oauth:
            domains:
                - auth.openai.com
                - chatgpt.com
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

const globalSecrets = `{"secrets": [
	{"scope": "global", "type": "service", "name": "anthropic", "secret": "(stored)"},
	{"scope": "global", "type": "service", "name": "nuget-feed", "secret": "(stored)"},
	{"scope": "stage-other", "type": "service", "name": "openai", "secret": "(stored)"}
]}`

func TestSetupMarksGlobalBindingsExpectedStaleOrOther(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.sbx.SetSecrets(globalSecrets)
	h.expect(0, "setup", "--agent", "claude")
	h.expectStdout(
		"stage: global credential bindings in "+h.credentials,
		"Any kit that declares credential@1 for a bound service can send that service's secret to any domain the kit names; outside stage sandboxes a binding is inert unless its service has a global secret",
		"  anthropic (expected, global secret set): platform.claude.com, api.anthropic.com\n    stage binds this for agent claude;",
		"  openai (stage (not configured)): auth.openai.com, chatgpt.com\n    stage binds this when you use agent codex; remove it only if you no longer do\n",
		"  github (expected): api.github.com, github.com\n    stage binds this for [github] mode \"fine-grained\";",
		"  stage-broker (stale): localhost, localhost:9911\n    stage binds only localhost:9911 for the AWS broker; sbx never drops a binding domain, so delete localhost from the stage-broker entry in "+h.credentials,
		"  nuget-feed (other, global secret set): pkgs.example.com\n    `sbx secret rm nuget-feed` removes the global secret any kit can send anywhere; stage does not bind this, so if nothing needs it, delete the nuget-feed entry from "+h.credentials+" by hand",
		"never `sbx env rm --prune-bindings` a stage sandbox",
	)
	if got := testutil.ReadFile(t, h.credentials); got != globalBindings {
		t.Errorf("credentials.yaml changed to %q", got)
	}
	if h.anyCallContaining("prune-bindings") || h.anyCall("env rm", "secret rm") {
		t.Errorf("setup changed sbx state: %v", h.sbx.Calls())
	}
}

func TestSetupMarksAStageServiceWhoseAgentIsNotConfigured(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.expect(0, "setup")
	h.expectStdout(
		"  anthropic (stage (not configured)): platform.claude.com, api.anthropic.com\n    stage binds this when you use agent claude; remove it only if you no longer do\n",
		"  nuget-feed (other): pkgs.example.com\n    stage does not bind this; if nothing needs it, delete the nuget-feed entry from "+h.credentials+" by hand",
	)
}

func TestSetupStillAuditsBindingsWhenGlobalSecretsAreUnreadable(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.sbx.SetSecrets("not json")
	h.expect(0, "setup", "--agent", "claude")
	h.expectStderr("stage: WARNING: cannot tell which bound services have a global secret")
	h.expectStdout("  anthropic (expected): platform.claude.com")
}

func TestSetupSkipsAnUnrecognisedBindingAndListsTheRest(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, "bindings:\n  odd: [1, 2]\n  nuget-feed:\n    apiKey:\n      domains: [pkgs.example.com]\n")
	h.expect(0, "setup")
	h.expectStdout(
		"  odd (unrecognised entry): \n    stage cannot read this entry's domains; inspect the odd entry in "+h.credentials,
		"  nuget-feed (other): pkgs.example.com",
	)
}

func TestUpPlanAuditsGlobalBindingsWithTheSandboxsOwn(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.expect(0, "up", h.remote.Checkout, "fix", "--plan", "--agent", "claude")
	h.expectStdout("  anthropic (expected): platform.claude.com, api.anthropic.com\n    stage binds this for this sandbox;")
}

func TestUpPlanAuditsGlobalBindingsEvenWhenThePlanFails(t *testing.T) {
	h := setup(t)
	testutil.WriteFile(t, h.credentials, globalBindings)
	h.expect(1, "up", h.remote.Checkout, "fix", "--plan", "--agent", "gemeni")
	h.expectStderr("unknown agent gemeni")
	h.expectStdout("  nuget-feed (other): pkgs.example.com")
}

func TestSetupReportsNoBindingsWhenCredentialsYamlIsMissing(t *testing.T) {
	h := setup(t)
	h.expect(0, "setup")
	h.expectStdout("stage: global credential bindings: not found at " + h.credentials + " (path inferred from sbx; only $HOME/.config verified)")
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
	h.sbx.SetSecrets(`{"secrets": [{"scope": "global", "type": "service", "name": "evil\u001b[2J", "secret": "(stored)"}]}`)
	h.expect(0, "setup")
	h.expectStdout(`  evil\x1b[2J (other, global secret set): bad\x202ehost.example`, "delete the evil\\x1b[2J entry", "`sbx secret rm 'evil\\x1b[2J'`")
	if strings.ContainsAny(h.stdout.String()+h.stderr.String(), "\x1b‮") {
		t.Errorf("output kept control characters: %q", h.stdout.String())
	}
}
