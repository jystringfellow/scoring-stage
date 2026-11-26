package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestUpRefusesGlobalAllowRulesWhenSecretsManagerSecretsAreListedEvenWithTheOverride(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "[aws.secrets_manager]\nvault = [\"app/config\"]\n")
	h.sbx.SetPolicy(`{"rules": [{"id": "default-ai-services", "scope": "global", "decision": "allow", "resource_type": "network"}]}`)
	h.expect(1, "up", h.remote.Checkout, "fix", "--allow-global-policy")
	h.expectStderr("[aws.secrets_manager] lists secrets, so the global sbx policy must be deny-all: a global allow rule could open Secrets Manager")
	if h.anyCall("env create") {
		t.Fatal("created despite a global allow rule")
	}
}

func TestUpSaysWhichSecretsManagerSecretsTheSandboxMayRead(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "[aws.secrets_manager]\nvault = [\"app/db\", \"app/config\"]\n")
	h.expect(0, "up", h.remote.Checkout, "fix", "--plan")
	h.expectStdout("aws secrets manager (profile vault): app/config, app/db")
}

func TestUpGrantsTheListedSecretsToTheSandboxsBrokerTokenAndStartsTheBroker(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "[aws.secrets_manager]\nvault = [\"app/db\", \"app/config\"]\n")
	h.expect(0, "up", h.remote.Checkout, "fix")
	grants := filepath.Join(h.home, "broker", "grants")
	if got := testutil.ReadFile(t, filepath.Join(grants, "fix.secrets")); got != "region us-east-1\nsecret vault app/config\nsecret vault app/db\n" {
		t.Errorf("secrets grant %q", got)
	}
	if got := testutil.ReadFile(t, filepath.Join(grants, "fix.profiles")); got != "\n" {
		t.Errorf("profiles grant %q; a profile listed only for Secrets Manager is not granted", got)
	}
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	for _, fragment := range []string{`command: "/bin/cat ` + filepath.Join(grants, "fix.token") + `"`, `secrets_manager: "on"`, `profiles: ""`} {
		if !strings.Contains(env, fragment) {
			t.Errorf("env file lacks %s:\n%s", fragment, env)
		}
	}
	if len(h.brokerStarts) != 1 {
		t.Errorf("broker starts %v", h.brokerStarts)
	}
	h.expect(0, "down", "fix")
	if _, err := os.Stat(filepath.Join(grants, "fix.secrets")); !os.IsNotExist(err) {
		t.Errorf("down left the secrets grant: %v", err)
	}
}

func TestUpRefusesARunningBrokerThatDoesNotServeSecretsManager(t *testing.T) {
	h := setup(t)
	writeGitHubSetup(t, h.home, "[aws.secrets_manager]\nvault = [\"app/db\"]\n")
	h.brokerPID = 4242
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("the broker running (pid 4242) does not serve Secrets Manager on 127.0.0.1:9912; run `stage broker stop` and `stage broker start`")
	if h.anyCall("env create") {
		t.Fatal("created with a broker that cannot serve the listed secrets")
	}
	h.brokerServesSM = true
	h.expect(0, "up", h.remote.Checkout, "fix2")
	if len(h.brokerStarts) != 0 {
		t.Errorf("a running broker was started again: %v", h.brokerStarts)
	}
}
