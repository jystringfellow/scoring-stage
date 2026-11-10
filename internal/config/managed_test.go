package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
		"allowed_agent = [\"claude\"]\n":                   "unknown keys allowed_agent",
		"repo_kits = \"allow\"\n":                          "repo_kits must be",
		"allow_global_policy = \"no\"\n":                   "allow_global_policy must be true or false",
		"[aws]\nallowed = [\"dev\"]\n":                     "unknown keys allowed",
		"[github]\nmode = \"gh\"\n":                        "[github]: unknown keys mode",
		"github = \"gh\"\n":                                "github must be a table",
		"[github]\nallowed_modes = \"gh\"\n":               "allowed_modes must be a list of strings",
		"[github]\nallowed_modes = [\"readonly-token\"]\n": `allowed_modes has unknown mode "readonly-token"`,
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
	if !reflect.DeepEqual(managed.AllowedGitHubModes(), GitHubModes) || managed.DefaultGitHubMode() != "" {
		t.Fatalf("unmanaged GitHub modes %v, default %q", managed.AllowedGitHubModes(), managed.DefaultGitHubMode())
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
	for _, kit := range []string{"docker.io/sbx/newagent-mixin:1", "registry.hub.docker.com/sbx/codex-mixin:1", "mirror.example.com/sbx/codex-mixin:1"} {
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), "kits = [\""+kit+"\"]\n")
		_, err := Compose(load(t, home), Selection{})
		expectError(t, err, "is not a known agent")
	}
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

	managedFiles(t, "[aws]\ndenied_profiles = [\"team_[prod\"]\n")
	_, err = LoadManaged()
	expectError(t, err, "bad pattern")
}

func TestProfilePatternsIgnoreCase(t *testing.T) {
	managedFiles(t, "[aws]\ndenied_profiles = [\"*prod*\"]\nallowed_profiles = [\"Team*\"]\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"Team_PROD", "team-Prod"} {
		expectError(t, managed.CheckAWSProfile(profile), "is denied by")
	}
	if err := managed.CheckAWSProfile("TEAM_dev"); err != nil {
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

func TestManagedGitHubModesNarrowAcrossFiles(t *testing.T) {
	managedFiles(t,
		"[github]\nallowed_modes = [\"fine-grained\", \"gh\"]\n",
		"[github]\nallowed_modes = [\"gh\", \"none\"]\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(managed.AllowedGitHubModes(), []string{GitHubGh}) || managed.DefaultGitHubMode() != GitHubGh {
		t.Errorf("allowed %v, default %q", managed.AllowedGitHubModes(), managed.DefaultGitHubMode())
	}
	expectError(t, managed.CheckGitHubMode(GitHubNone), `GitHub mode "none" is not allowed by`)
	if !contains(managed.Summary(), "  allowed GitHub modes: gh") {
		t.Errorf("summary %q", managed.Summary())
	}
}

func TestManagedSettingsWithoutAllowedModesRefuseOnlyGhMode(t *testing.T) {
	managedFiles(t, "repo_kits = \"ask\"\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, managed.CheckGitHubMode(GitHubGh), `only when [github] allowed_modes lists it`)
	if managed.CheckGitHubMode(GitHubFineGrained) != nil || managed.CheckGitHubMode(GitHubNone) != nil || managed.DefaultGitHubMode() != "" {
		t.Error("managed settings without allowed_modes refused more than gh mode, or chose a default")
	}

	managedFiles(t, "repo_kits = \"ask\"\n", "[github]\nallowed_modes = [\"gh\"]\n")
	if managed, err = LoadManaged(); err != nil {
		t.Fatal(err)
	}
	if err := managed.CheckGitHubMode(GitHubGh); err != nil {
		t.Fatal(err)
	}
}

func TestAnEmptyManagedGitHubModeListAllowsNoMode(t *testing.T) {
	managedFiles(t, "[github]\nallowed_modes = []\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range GitHubModes {
		expectError(t, managed.CheckGitHubMode(mode), "no GitHub mode is allowed")
	}
	if managed.DefaultGitHubMode() != "" {
		t.Error("an empty list chose a default mode")
	}
	if summary := strings.Join(managed.Summary(), "\n"); !strings.Contains(summary, "allowed GitHub modes: no mode") {
		t.Errorf("summary %q", summary)
	}
}

func TestANewGitHubModeIsRefusedUnderManagedSettingsUntilListed(t *testing.T) {
	original := GitHubModes
	GitHubModes = append(slices.Clone(GitHubModes), "future")
	t.Cleanup(func() { GitHubModes = original })
	managedFiles(t, "repo_kits = \"ask\"\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, managed.CheckGitHubMode("future"), `GitHub mode "future" is not allowed by`)

	managedFiles(t, "[github]\nallowed_modes = [\"future\", \"none\"]\n")
	if managed, err = LoadManaged(); err != nil {
		t.Fatal(err)
	}
	if err := managed.CheckGitHubMode("future"); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedAllowedGitHubModesCountOnce(t *testing.T) {
	managedFiles(t, "[github]\nallowed_modes = [\"none\", \"none\"]\n")
	managed, err := LoadManaged()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(managed.AllowedGitHubModes(), []string{GitHubNone}) || managed.DefaultGitHubMode() != GitHubNone {
		t.Errorf("allowed %v, default %q", managed.AllowedGitHubModes(), managed.DefaultGitHubMode())
	}
}

func TestAManagedGitHubAppClientIDIsUsedAndAUserCannotPointElsewhere(t *testing.T) {
	managedFiles(t, "[github]\nallowed_modes = [\"app\"]\napp_client_id = \"Iv23pinned\"\n")
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[github]\nmode = \"app\"\n")
	settings := load(t, home)
	if got := settings.GitHubAppClientID(); got != "Iv23pinned" {
		t.Errorf("client ID %q, want the pinned one", got)
	}
	if !slices.ContainsFunc(settings.Managed.Summary(), func(line string) bool { return strings.Contains(line, "GitHub App client ID (pinned): Iv23pinned") }) {
		t.Errorf("summary %v does not show the pin", settings.Managed.Summary())
	}

	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[github]\nmode = \"app\"\napp_client_id = \"Iv23pinned\"\n")
	load(t, home)

	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[github]\nmode = \"app\"\napp_client_id = \"Iv23other\"\n")
	settings = load(t, home)
	if got := settings.GitHubAppClientID(); got != "Iv23pinned" {
		t.Errorf("client ID %q, want the pinned one over the user's", got)
	}
	if ignored := settings.IgnoredGitHubAppClientID(); !strings.Contains(ignored, "app_client_id Iv23other is ignored") {
		t.Errorf("no warning about the ignored client ID: %q", ignored)
	}
}

func TestManagedSettingsWarnAboutAnUnpinnedAppAndAPinThatDoesNothing(t *testing.T) {
	warnings := func() string {
		managed, err := LoadManaged()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(managed.Warnings(), "\n")
	}
	managedFiles(t, "[github]\nallowed_modes = [\"app\"]\n")
	if got := warnings(); !strings.Contains(got, "no managed file pins app_client_id") {
		t.Errorf("warnings %q", got)
	}
	managedFiles(t, "[github]\napp_client_id = \"Iv23pinned\"\n")
	if got := warnings(); !strings.Contains(got, "app_client_id Iv23pinned") || !strings.Contains(got, "has no effect") {
		t.Errorf("warnings %q", got)
	}
	managedFiles(t, "[github]\nallowed_modes = [\"app\"]\napp_client_id = \"Iv23pinned\"\n")
	if got := warnings(); strings.Contains(got, "app_client_id") {
		t.Errorf("warned about a pinned, allowed app: %q", got)
	}
}

func TestManagedFilesMustAgreeOnTheGitHubAppClientID(t *testing.T) {
	managedFiles(t, "[github]\napp_client_id = \"Iv23first\"\n", "[github]\napp_client_id = \"Iv23first\"\n")
	if _, err := LoadManaged(); err != nil {
		t.Fatal(err)
	}
	managedFiles(t, "[github]\napp_client_id = \"Iv23first\"\n", "[github]\napp_client_id = \"Iv23second\"\n")
	_, err := LoadManaged()
	expectError(t, err, "pin different GitHub App client IDs (Iv23first, Iv23second)")
	managedFiles(t, "[github]\napp_client_id = \"not a client id\"\n")
	_, err = LoadManaged()
	expectError(t, err, "app_client_id must be a GitHub App's Client ID")
}
