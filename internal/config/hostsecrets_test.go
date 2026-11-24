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
