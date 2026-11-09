package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestMain(m *testing.M) {
	ManagedPaths = func() []string { return nil }
	os.Exit(m.Run())
}

func managedFiles(t *testing.T, contents ...string) {
	t.Helper()
	var paths []string
	for i, content := range contents {
		paths = append(paths, testutil.WriteFile(t, filepath.Join(t.TempDir(), "managed", "m"+string(rune('0'+i))+".toml"), content))
	}
	original := ManagedPaths
	ManagedPaths = func() []string { return paths }
	t.Cleanup(func() { ManagedPaths = original })
}

func TestManagedSettingsLimitTheAgentsFromEverySource(t *testing.T) {
	managedFiles(t, "allowed_agents = [\"claude\", \"codex\"]\ndenied_agents = [\"codex\"]\n")
	home := filepath.Join(t.TempDir(), "home")
	settings := load(t, home)
	composeWith(t, settings, Selection{Agents: []string{"claude"}})
	_, err := Compose(settings, Selection{Agents: []string{"codex"}})
	expectError(t, err, "agent codex is denied by")
	_, err = Compose(settings, Selection{Agents: []string{"agy"}})
	expectError(t, err, "agent agy is not allowed by")
	_, err = Compose(settings, Selection{Agents: []string{"docker.io/someone/agent-mixin:1"}})
	expectError(t, err, "is not a known agent")

	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "agents = [\"agy\"]\n")
	_, err = Compose(load(t, home), Selection{})
	expectError(t, err, "config: agent agy is not allowed by")
}

func TestManagedAWSProfilePatternsApplyToEveryGrant(t *testing.T) {
	managedFiles(t, "[aws]\nallowed_profiles = [\"dev*\", \"sandbox\"]\ndenied_profiles = [\"*admin*\"]\n")
	settings := load(t, filepath.Join(t.TempDir(), "home"))
	composeWith(t, settings, Selection{AWSProfile: "dev-team"})
	_, err := Compose(settings, Selection{AWSProfile: "prod"})
	expectError(t, err, "AWS profile prod is not allowed by")
	_, err = Compose(settings, Selection{AWSProfile: "dev-admin"})
	expectError(t, err, "AWS profile dev-admin is denied by")
}

func TestASecondManagedFileOnlyNarrowsTheFirst(t *testing.T) {
	managedFiles(t,
		"allowed_agents = [\"claude\", \"codex\"]\n[aws]\nallowed_profiles = [\"dev*\"]\n",
		"allowed_agents = [\"codex\", \"agy\"]\nallow_global_policy = false\n[aws]\nallowed_profiles = [\"*-team\"]\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(managed.allowedAgents, []string{"codex"}) {
		t.Errorf("allowed agents %v", managed.allowedAgents)
	}
	if managed.CheckAWSProfile("dev-team") != nil || managed.CheckAWSProfile("dev") == nil || managed.CheckAWSProfile("ops-team") == nil {
		t.Error("profile patterns from both files must match")
	}
	if managed.AllowsGlobalPolicy() {
		t.Error("allow_global_policy = false was ignored")
	}
}

func TestManagedSettingsCanRefuseRepositoryKitsAndCopiedLogins(t *testing.T) {
	managedFiles(t, "repo_kits = \"deny\"\ncopy_logins = []\n")
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "kits = [\"./kit\"]\n")
	testutil.WriteFile(t, filepath.Join(repoDir, "kit", "kit.yaml"), "x: 1\n")
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compose(load(t, filepath.Join(dir, "home")), Selection{Repo: repo})
	expectError(t, err, "the repository's kits and books are refused by")

	home := filepath.Join(dir, "home2")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "copy_logins = [\"agy\"]\n")
	_, err = LoadSettings(home)
	expectError(t, err, "copying agy's login is not allowed by")
}

func TestManagedSettingsAreDecodedStrictly(t *testing.T) {
	for content, fragment := range map[string]string{
		"allowed_agent = [\"claude\"]\n": "unknown keys allowed_agent",
		"repo_kits = \"allow\"\n":        "repo_kits must be",
		"allow_global_policy = \"no\"\n": "allow_global_policy must be true or false",
		"[aws]\nallowed = [\"dev\"]\n":   "unknown keys allowed",
	} {
		managedFiles(t, content)
		_, err := LoadManaged()
		expectError(t, err, fragment)
	}
}

func TestWithoutManagedSettingsNothingIsLimited(t *testing.T) {
	managedFiles(t)
	managed, err := LoadManaged()
	if err != nil || len(managed.Paths) != 0 || managed.Summary() != nil {
		t.Fatalf("managed %+v, %v", managed, err)
	}
	if managed.CheckAgent("agy", true, "x") != nil || managed.CheckAWSProfile("prod") != nil || !managed.AllowsGlobalPolicy() || managed.RepoKitsDenied() {
		t.Fatal("unmanaged settings refused something")
	}
}

func TestManagedFilesWithNoAgentInCommonAllowNone(t *testing.T) {
	managedFiles(t, "allowed_agents = [\"claude\"]\n", "allowed_agents = [\"codex\"]\n")
	settings := load(t, filepath.Join(t.TempDir(), "home"))
	for _, agent := range []string{"claude", "codex", "agy"} {
		_, err := Compose(settings, Selection{Agents: []string{agent}})
		expectError(t, err, "agent "+agent+" is not allowed by")
	}
}

func TestAgentPolicyCannotBeSidesteppedByReferenceAliasesOrPlainKits(t *testing.T) {
	managedFiles(t, "denied_agents = [\"codex\"]\n")
	for _, config := range []string{
		"agents = [\"sbx/codex-mixin:0.167.0\"]\n",
		"agents = [\"index.docker.io/sbx/codex-mixin\"]\n",
		"agents = [\"registry-1.docker.io/sbx/codex-mixin:1\"]\n",
		"agents = [\"docker.io/SBX/Codex-Mixin:1\"]\n",
		"kits = [\"docker.io/sbx/codex-mixin:0.167.0\"]\n",
	} {
		home := filepath.Join(t.TempDir(), "home")
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), config)
		_, err := Compose(load(t, home), Selection{})
		if err == nil || !strings.Contains(err.Error(), "agent codex is denied by") {
			t.Errorf("%s: %v", strings.TrimSpace(config), err)
		}
	}
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "kits = [\"docker.io/sbx/newagent-mixin:1\"]\n")
	_, err := Compose(load(t, home), Selection{})
	expectError(t, err, "is not a known agent")
}

func TestAnUnreadableManagedFileOrBadPatternRefusesToRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locked")
	file := testutil.WriteFile(t, filepath.Join(dir, "managed.toml"), "denied_agents = [\"codex\"]\n")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	original := ManagedPaths
	ManagedPaths = func() []string { return []string{file} }
	t.Cleanup(func() { ManagedPaths = original })
	_, err := LoadManaged()
	expectError(t, err, "refusing to run unmanaged")

	managedFiles(t, "[aws]\ndenied_profiles = [\"arcus_[prod\"]\n")
	_, err = LoadManaged()
	expectError(t, err, "bad pattern")
}

func TestProfilePatternsIgnoreCase(t *testing.T) {
	managedFiles(t, "[aws]\ndenied_profiles = [\"*prod*\"]\nallowed_profiles = [\"Arcus*\"]\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"Arcus_PROD", "arcus-Prod"} {
		expectError(t, managed.CheckAWSProfile(profile), "is denied by")
	}
	if err := managed.CheckAWSProfile("ARCUS_dev"); err != nil {
		t.Fatal(err)
	}
}

func TestSetupWarnsAboutManagedFilesOthersCouldWrite(t *testing.T) {
	managedFiles(t, "repo_kits = \"deny\"\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	if warnings := strings.Join(managed.Warnings(), "\n"); !strings.Contains(warnings, "is not owned by root") {
		t.Fatalf("warnings %q", warnings)
	}
}
