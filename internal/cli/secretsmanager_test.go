package cli

import (
	"testing"
)

func TestUpRefusesGlobalAllowRulesWhenSecretsManagerSecretsAreListedEvenWithTheOverride(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "[aws.secrets_manager]\nvault = [\"app/config\"]\n")
	h.sbx.SetPolicy(`{"rules": [{"id": "default-ai-services", "scope": "global", "decision": "allow", "resource_type": "network"}]}`)
	h.expect(1, "up", h.remote.Checkout, "fix", "--allow-global-policy")
	h.expectStderr("[aws.secrets_manager] lists secrets the sandbox reads only through the stage broker, so the global sbx policy must be deny-all: a global allow rule could open Secrets Manager")
	if h.anyCall("env create") {
		t.Fatal("created despite a global allow rule")
	}
}

func TestUpSaysWhichSecretsManagerSecretsTheSandboxMayRead(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "[aws.secrets_manager]\nvault = [\"app/db\", \"app/config\"]\n")
	h.expect(0, "up", h.remote.Checkout, "fix", "--plan")
	h.expectStdout("aws secrets manager (read on your machine with profile vault): app/config, app/db")
}
