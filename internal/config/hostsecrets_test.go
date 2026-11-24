package config

import (
	"path/filepath"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestSecretsReadFromYourHostLoginsAskSbxToResolveThemAgainEvery30Minutes(t *testing.T) {
	for mode, config := range map[string]string{"gh": "[github]\nmode = \"gh\"\n", "fine-grained": fineGrained} {
		home := filepath.Join(t.TempDir(), "home")
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), config)
		writeToken(t, filepath.Join(home, "secrets", "github", "acme.token"), "github_pat_readonly")
		composition := composeWith(t, load(t, home), Selection{Agents: []string{"copilot"}, Gh: "/usr/bin/gh", GitHubOwner: "acme"})
		for _, service := range []string{"copilot", "github"} {
			secret, _ := composition.Secrets[service].(map[string]any)
			if secret["refresh"] != HostLoginSecretRefresh {
				t.Errorf("%s mode: %s secret %v has no refresh of %s", mode, service, secret, HostLoginSecretRefresh)
			}
		}
	}
}

func TestAKitSummaryNamesACapabilityOnceWhenTheKitDeclaresItTwice(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "kit.yaml"), "capabilities:\n  - type: com.docker.sandbox/agent-context@1\n    config: {}\n  - type: com.docker.sandbox/agent-context@2\n    config: {}\n")
	details := SummarizeKit(dir)
	count := 0
	for _, detail := range details {
		if detail == "agent-context" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("summary %q names agent-context %d times", details, count)
	}
}
