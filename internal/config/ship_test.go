package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func shipConfig(t *testing.T, ship string) (*Settings, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[ship]\n"+ship)
	return LoadSettings(home)
}

func TestWithoutShipSettingsBranchesStartWithAgentTrailersAreNotCheckedAndNoPathsAreAdded(t *testing.T) {
	ship := load(t, filepath.Join(t.TempDir(), "home")).Ship
	want := ShipSettings{BranchPrefix: "agent/", PRBody: DefaultPRBody}
	if !reflect.DeepEqual(ship, want) {
		t.Fatalf("ship settings = %+v, want %+v", ship, want)
	}
	if !strings.Contains(DefaultPRBody, "not yet been reviewed by a human") {
		t.Errorf("default body = %q", DefaultPRBody)
	}
}

func TestTheUserConfigSetsTheShipBranchPrefixTrailerWarningBodyFooterAndExtraProtectedPaths(t *testing.T) {
	settings, err := shipConfig(t, `
branch_prefix = "bot/"
warn_missing_trailer = true
pr_body = "Review against the team checklist."
pr_footer = "Sent by a robot."
protected_paths = ["deploy/", "Makefile"]
`)
	if err != nil {
		t.Fatal(err)
	}
	want := ShipSettings{
		BranchPrefix:       "bot/",
		WarnMissingTrailer: true,
		PRBody:             "Review against the team checklist.",
		PRFooter:           "Sent by a robot.",
		ProtectedPaths:     []string{"deploy/", "Makefile"},
	}
	if !reflect.DeepEqual(settings.Ship, want) {
		t.Fatalf("ship settings = %+v, want %+v", settings.Ship, want)
	}
}

func TestAnEmptyPRBodyLeavesOnlyWhatStageAlwaysWrites(t *testing.T) {
	settings, err := shipConfig(t, "pr_body = \"\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if settings.Ship.PRBody != "" {
		t.Fatalf("body = %q", settings.Ship.PRBody)
	}
}

func TestAShipBranchPrefixThatIsEmptyOrHasHiddenCharactersIsRefused(t *testing.T) {
	for prefix, want := range map[string]string{
		``:        "branch_prefix cannot be empty",
		`agent‮/`: "branch_prefix has control or invisible characters",
		`agent​/`: "branch_prefix has control or invisible characters",
		`a\tb/`:   "branch_prefix has control or invisible characters",
	} {
		_, err := shipConfig(t, `branch_prefix = "`+prefix+`"`+"\n")
		expectError(t, err, want)
	}
}

func TestShipSettingsOfTheWrongTypeAreRefused(t *testing.T) {
	for setting, want := range map[string]string{
		`branch_prefix = 1`:          "branch_prefix must be a string",
		`warn_missing_trailer = "y"`: "warn_missing_trailer must be true or false",
		`pr_body = ["x"]`:            "pr_body must be a string",
		`pr_footer = 1`:              "pr_footer must be a string",
		`protected_paths = "deploy"`: "protected_paths must be a list of strings",
	} {
		_, err := shipConfig(t, setting+"\n")
		expectError(t, err, want)
	}
}

func TestAddedProtectedPathsAreRepositoryPathsWithoutHiddenCharacters(t *testing.T) {
	for path, want := range map[string]string{
		``:            "protected_paths entries cannot be empty",
		`/etc/passwd`: "is not a path inside the repository",
		`../other`:    "is not a path inside the repository",
		`deploy‮/`:    "has control or invisible characters",
	} {
		_, err := shipConfig(t, `protected_paths = ["`+path+`"]`+"\n")
		expectError(t, err, want)
	}
}

func TestOnlyTheUserConfigSetsShipSettings(t *testing.T) {
	repoDir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "[ship]\nbranch_prefix = \"main\"\n")
	_, err := LoadRepoConfig(repoDir)
	expectError(t, err, "a repository cannot set ship")

	dir := filepath.Join(t.TempDir(), "org")
	testutil.WriteFile(t, filepath.Join(dir, "book.toml"), "name = \"org\"\n\n[ship]\nbranch_prefix = \"main\"\n")
	_, err = LoadBook(dir)
	expectError(t, err, "unknown keys ship")
}

func TestTheCopyLoginsRefusalNamesTheAgentsWithoutALoginFileFromTheAgentTable(t *testing.T) {
	agents["example"] = agentSpec{kit: "docker.io/sbx/example-mixin:1.0.0"}
	t.Cleanup(func() { delete(agents, "example") })
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "copy_logins = [\"example\"]\n")
	_, err := LoadSettings(home)
	expectError(t, err, "copy_logins: example cannot be copied (copyable: agy); claude, codex, copilot and example sign in through the proxy")
}
