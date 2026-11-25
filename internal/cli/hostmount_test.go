package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestTheEnvFileNamesNoHostDirectoryForAKitsHostMount(t *testing.T) {
	h := setup(t)
	h.commitToMain(map[string]string{
		".scoring-stage/config.toml":         "kits = [\"./.scoring-stage/kits/cache\"]\n",
		".scoring-stage/kits/cache/kit.yaml": "capabilities:\n  - type: com.docker.sandbox/host-mount@1\n    config:\n      path: /home/agent/.cache/pip\n",
	})
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("shares /home/agent/.cache/pip with the host and with every sandbox using this kit")

	h.expect(0, "up", h.remote.Checkout, "fix", "--approve-repo-config")
	var env map[string]any
	if err := yaml.Unmarshal([]byte(testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))), &env); err != nil {
		t.Fatal(err)
	}
	if _, found := env["mounts"]; found {
		t.Errorf("the env file chooses host directories for kit mounts: %v", env["mounts"])
	}
	for _, call := range h.sbx.Calls() {
		if strings.Contains(call, "--mount") {
			t.Errorf("sbx was called with a host mount: %q", call)
		}
	}
}
