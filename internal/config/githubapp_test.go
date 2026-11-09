package config

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/github"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const appMode = "[github]\nmode = \"app\"\napp_client_id = \"Iv23test\"\n"

func fakeStage(t *testing.T) string {
	t.Helper()
	path := testutil.WriteFile(t, filepath.Join(t.TempDir(), "bin dir", "stage"), "")
	original := StageExecutable
	StageExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { StageExecutable = original })
	return Resolve(path)
}

func appHome(t *testing.T, config string, login *github.Login) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), config)
	if login != nil {
		if err := (github.Store{Path: filepath.Join(home, "secrets", "github", "app.json")}).Write(*login); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func freshLogin(clientID string) *github.Login {
	now := time.Now()
	return &github.Login{ClientID: clientID, AccessToken: "ghu_x", ExpiresAt: now.Add(8 * time.Hour),
		RefreshToken: "ghr_x", RefreshTokenExpiresAt: now.Add(180 * 24 * time.Hour)}
}

func TestAppModeSendsTheStageTokenCommandWithARefreshPolicyForAnyOwner(t *testing.T) {
	stage := fakeStage(t)
	home := appHome(t, appMode, freshLogin("Iv23test"))
	settings := load(t, home)
	if settings.GitHubAppClientID() != "Iv23test" {
		t.Errorf("client ID %q", settings.GitHubAppClientID())
	}
	for _, owner := range []string{"acme", "globex", ""} {
		composition := composeWith(t, settings, Selection{GitHubOwner: owner})
		want := map[string]any{"command": "/usr/bin/env STAGE_HOME=" + ShellQuote(home) + " " + ShellQuote(stage) + " github token", "refresh": "30m"}
		if got := composition.Secrets["github"]; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: github = %v, want %v", owner, got, want)
		}
		if composition.Bindings["github"] == nil {
			t.Errorf("%q: no github binding", owner)
		}
	}
}

func TestAppModeGivesNoGitHubCredentialWithoutAUsableLogin(t *testing.T) {
	fakeStage(t)
	expired := freshLogin("Iv23test")
	expired.ExpiresAt = time.Now().Add(-time.Hour)
	expired.RefreshTokenExpiresAt = time.Now().Add(-time.Minute)
	for name, c := range map[string]struct {
		config   string
		login    *github.Login
		fragment string
	}{
		"no client ID":       {"[github]\nmode = \"app\"\n", freshLogin("Iv23test"), `needs the Client ID of your GitHub App: set app_client_id = "Iv23..."`},
		"not logged in":      {appMode, nil, "not logged in to the GitHub App"},
		"another client ID":  {appMode, freshLogin("Iv23other"), "client ID Iv23other, but [github] app_client_id is Iv23test; run `stage github login`"},
		"expired everything": {appMode, expired, "expired; run `stage github login`"},
	} {
		settings := load(t, appHome(t, c.config, c.login))
		expectError(t, settings.CheckGitHub("acme", ""), c.fragment)
		composition := composeWith(t, settings, Selection{GitHubOwner: "acme"})
		if composition.Secrets["github"] != nil || composition.Bindings["github"] != nil {
			t.Errorf("%s: secrets %v bindings %v", name, composition.Secrets, composition.Bindings)
		}
	}
}

func TestAnExpiredAccessTokenWithALiveRefreshTokenPassesTheCheck(t *testing.T) {
	login := freshLogin("Iv23test")
	login.ExpiresAt = time.Now().Add(-time.Hour)
	if err := load(t, appHome(t, appMode, login)).CheckGitHub("acme", ""); err != nil {
		t.Error(err)
	}
}

func TestAppClientIDMustLookLikeAClientID(t *testing.T) {
	for _, value := range []string{"42", `"has space"`, `""`} {
		home := appHome(t, "[github]\nmode = \"app\"\napp_client_id = "+value+"\n", nil)
		_, err := LoadSettings(home)
		expectError(t, err, "app_client_id must be a GitHub App's Client ID")
	}
}

func TestTheAppLoginIsNotMistakenForAnOwnersToken(t *testing.T) {
	home := appHome(t, appMode, freshLogin("Iv23test"))
	writeToken(t, filepath.Join(home, "secrets", "github", "acme.token"), "github_pat_x")
	testutil.WriteFile(t, filepath.Join(home, "secrets", "github", "app.lock"), "")
	owners, err := load(t, home).GitHubTokenOwners()
	if err != nil || !reflect.DeepEqual(owners, []string{"acme"}) {
		t.Errorf("owners %v, %v", owners, err)
	}
}
