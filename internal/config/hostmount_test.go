package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func hostMountKit(config string) string {
	return "capabilities:\n  - type: com.docker.sandbox/host-mount@1\n    config:\n" + config
}

func repositoryKitError(t *testing.T, kit string) error {
	t.Helper()
	repoDir := filepath.Join(t.TempDir(), "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "kits = [\"./kits/cache\"]\n")
	testutil.WriteFile(t, filepath.Join(repoDir, "kits", "cache", "kit.yaml"), kit)
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compose(load(t, filepath.Join(t.TempDir(), "home")), Selection{Repo: repo})
	return err
}

func TestAHostMountInABookOrRepositoryKitMayNameOnlyItsSandboxPathAndMode(t *testing.T) {
	for name, c := range map[string]struct {
		kit, message string
	}{
		"a host source":           {hostMountKit("      path: /home/agent/.cache/pip\n      source: /Users/someone/.scoring-stage\n"), "host-mount@1 config has source"},
		"a host path":             {hostMountKit("      path: /home/agent/.cache/pip\n      hostPath: /\n"), "host-mount@1 config has hostPath"},
		"a differently cased key": {hostMountKit("      Path: /home/agent/.cache/pip\n"), "host-mount@1 config has Path"},
		"no path":                 {hostMountKit("      mode: \"0755\"\n"), "host-mount@1 has no path"},
		"a path that is a list":   {hostMountKit("      path: [/home/agent/.cache/pip]\n"), "host-mount@1 has no path"},
		"a mode that is a number": {hostMountKit("      path: /home/agent/.cache/pip\n      mode: 755\n"), "host-mount@1 mode must be a string"},
		"another version":         {strings.Replace(hostMountKit("      path: /home/agent/.cache/pip\n"), "@1", "@2", 1), "host-mount@2, a version stage does not know"},
	} {
		_, err := LoadBook(BuiltinAgents(), bookWithKit(t, map[string]string{"kit.yaml": c.kit}))
		if err == nil || !strings.Contains(err.Error(), c.message) || !strings.Contains(err.Error(), "book org") {
			t.Errorf("book, %s: error %v, want %q", name, err, c.message)
		}
		if err := repositoryKitError(t, c.kit); err == nil || !strings.Contains(err.Error(), c.message) {
			t.Errorf("repository, %s: error %v, want %q", name, err, c.message)
		}
	}
	for _, kit := range []string{
		hostMountKit("      path: /home/agent/.cache/pip\n"),
		hostMountKit("      path: /home/agent/.cache/pip\n      mode: \"0755\"\n"),
	} {
		if _, err := LoadBook(BuiltinAgents(), bookWithKit(t, map[string]string{"kit.yaml": kit})); err != nil {
			t.Errorf("book refused %q: %v", kit, err)
		}
		if err := repositoryKitError(t, kit); err != nil {
			t.Errorf("repository refused %q: %v", kit, err)
		}
	}
}

func TestAHostMountSummarySaysThePathIsSharedWithTheHostAndEverySandboxUsingTheKit(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "kit.yaml"), hostMountKit("      path: /home/agent/.cache/pip\n      mode: \"0755\"\n"))
	got := strings.Join(SummarizeKit(dir), "\n")
	if want := "shares /home/agent/.cache/pip with the host and with every sandbox using this kit"; got != want {
		t.Errorf("summary %q, want %q", got, want)
	}
}
