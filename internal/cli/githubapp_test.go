package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/github/githubtest"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const appMode = "[github]\nmode = \"app\"\napp_client_id = \"Iv23test\"\n"

func appSetup(t *testing.T) *harness {
	t.Helper()
	h := setup(t)
	h.githubApp = githubtest.NewFake(t)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), appMode)
	stage := testutil.WriteFile(t, filepath.Join(t.TempDir(), "bin", "stage"), "")
	original := config.StageExecutable
	config.StageExecutable = func() (string, error) { return stage, nil }
	t.Cleanup(func() { config.StageExecutable = original })
	return h
}

func (h *harness) appLogin() string { return filepath.Join(h.home, "secrets", "github", "app.json") }

func TestGitHubLoginShowsTheCodeSavesTheLoginAndListsTheAccounts(t *testing.T) {
	h := appSetup(t)
	h.githubApp.Permissions["globex"] = map[string]string{"contents": "read"}
	h.expect(0, "github", "login")
	h.expectStderr("open https://github.com/login/device and enter the code ABCD-1234")
	for _, fragment := range []string{"logged in to the GitHub App Iv23test", "installed on acme, globex, read-only"} {
		if !strings.Contains(h.stdout.String(), fragment) {
			t.Errorf("stdout lacks %q:\n%s", fragment, h.stdout.String())
		}
	}
	info, err := os.Stat(h.appLogin())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("app.json: %v %v", info, err)
	}
	if strings.Contains(testutil.ReadFile(t, h.appLogin()), "client_secret") {
		t.Error("stored a client secret")
	}
}

func TestGitHubTokenPrintsTheTokenAndRefreshesItNearExpiry(t *testing.T) {
	h := appSetup(t)
	h.expect(1, "github", "token")
	h.expectStderr("not logged in to the GitHub App")
	h.expectStderr("run `stage github login`")
	h.expect(0, "github", "login")
	h.expect(0, "github", "token")
	if h.stdout.String() != "ghu_access1\n" {
		t.Errorf("stdout %q", h.stdout.String())
	}
	h.githubApp.Advance(8 * time.Hour)
	h.expect(0, "github", "token")
	if h.stdout.String() != "ghu_access2\n" || h.githubApp.Refreshes != 1 {
		t.Errorf("stdout %q after %d refreshes", h.stdout.String(), h.githubApp.Refreshes)
	}
}

func TestManagedSettingsRefuseAppModeUntilAllowedModesListsIt(t *testing.T) {
	h := appSetup(t)
	h.expect(0, "github", "login")
	testutil.WriteFile(t, h.managed, "repo_kits = \"ask\"\n")
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr(`GitHub mode "app" is not allowed`)
	h.expect(1, "github", "token")
	h.expectStderr(`GitHub mode "app" is not allowed`)
	if h.stdout.String() != "" {
		t.Errorf("printed a token while app mode is refused: %q", h.stdout.String())
	}
	h.expect(1, "github", "login")
	if h.anyCall("env create") {
		t.Error("created an app-mode sandbox that managed settings do not allow")
	}
	testutil.WriteFile(t, h.managed, "[github]\nallowed_modes = [\"app\"]\n")
	h.expect(0, "github", "token")
	h.expect(0, "up", h.remote.Checkout, "fix")
}

func TestAPinnedGitHubAppIsUsedWithoutAUserClientIDAndOtherAppsLoginsAreRefused(t *testing.T) {
	h := appSetup(t)
	testutil.WriteFile(t, h.managed, "[github]\nallowed_modes = [\"app\"]\napp_client_id = \"Iv23test\"\n")
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"app\"\n")
	h.expect(0, "github", "login")
	h.expect(0, "up", h.remote.Checkout, "fix")

	testutil.WriteFile(t, h.managed, "[github]\nallowed_modes = [\"app\"]\napp_client_id = \"Iv23other\"\n")
	h.expect(1, "github", "token")
	h.expectStderr("holds a login for GitHub App client ID Iv23test, but [github] app_client_id is Iv23other")
	if h.stdout.String() != "" {
		t.Errorf("printed a token for an app the managed settings do not pin: %q", h.stdout.String())
	}
	h.expect(1, "up", h.remote.Checkout, "other")
}

func TestGitHubCommandsNeedAppMode(t *testing.T) {
	h := setup(t)
	h.expect(1, "github", "login")
	h.expectStderr(`stage github works only with [github] mode = "app"`)
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), "[github]\nmode = \"app\"\n")
	h.expect(1, "github", "token")
	h.expectStderr("set app_client_id = \"Iv23...\" under [github]")
}

func TestUpInAppModeGivesTheSandboxTheStageTokenCommand(t *testing.T) {
	h := appSetup(t)
	h.expect(0, "github", "login")
	h.expect(0, "up", h.remote.Checkout, "fix")
	env := testutil.ReadFile(t, filepath.Join(h.task("fix"), "sbxenv.yaml"))
	stage, _ := config.StageExecutable()
	for _, fragment := range []string{"STAGE_HOME=" + config.ShellQuote(h.home), config.ShellQuote(stage) + " github token", `refresh: "30m"`, "api.github.com"} {
		if !strings.Contains(env, fragment) {
			t.Errorf("env lacks %q:\n%s", fragment, env)
		}
	}
	if strings.Contains(env, "ghu_") || strings.Contains(env, "ghr_") {
		t.Errorf("env holds a token:\n%s", env)
	}
}

func TestUpInAppModeRefusesAnAppThatCanWriteAndRemovesTheLogin(t *testing.T) {
	h := appSetup(t)
	h.expect(0, "github", "login")
	h.githubApp.Permissions["acme"]["contents"] = "write"
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("the installation on acme grants contents: write")
	if h.anyCall("env create") {
		t.Error("created a sandbox for an app that can write")
	}
	if _, err := os.Stat(h.appLogin()); err == nil {
		t.Error("kept the login of an app that can write")
	}
}

func TestUpInAppModeRefusesWithoutALoginOrWithAnotherAppsLogin(t *testing.T) {
	h := appSetup(t)
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("not logged in to the GitHub App")
	h.expect(0, "github", "login")
	testutil.WriteFile(t, filepath.Join(h.home, "config.toml"), strings.Replace(appMode, "Iv23test", "Iv23other", 1))
	h.expect(1, "up", h.remote.Checkout, "fix")
	h.expectStderr("client ID Iv23test, but [github] app_client_id is Iv23other; run `stage github login`")
	if h.anyCall("env create") {
		t.Error("created a sandbox without a usable app login")
	}
}

func TestSetupInAppModeReportsTheLoginOrSaysToLogIn(t *testing.T) {
	h := appSetup(t)
	h.expect(0, "setup")
	h.expectStderr("not logged in to the GitHub App")
	h.expectStderr("stage up will refuse")
	h.expect(0, "github", "login")
	h.expect(0, "setup")
	for _, fragment := range []string{"github: app Iv23test, logged in; access token expires", "refresh token valid until", "installed on acme, read-only"} {
		if !strings.Contains(h.stdout.String(), fragment) {
			t.Errorf("stdout lacks %q:\n%s", fragment, h.stdout.String())
		}
	}
	delete(h.githubApp.Permissions, "acme")
	h.expect(0, "setup")
	h.expectStderr("the GitHub App is not installed on any account")
}

func TestGitHubTokenRefusesOnceAnInstallationCanWriteWithoutWaitingForARefresh(t *testing.T) {
	h := appSetup(t)
	h.expect(0, "github", "login")
	h.expect(0, "github", "token")
	h.githubApp.Permissions["acme"]["contents"] = "write"
	h.expect(1, "github", "token")
	h.expectStderr("the installation on acme grants contents: write")
	h.expectStderr("run `stage down` on running sandboxes")
	if h.stdout.String() != "" || h.githubApp.Refreshes != 0 {
		t.Errorf("stdout %q after %d refreshes", h.stdout.String(), h.githubApp.Refreshes)
	}
	if _, err := os.Stat(h.appLogin()); err == nil {
		t.Error("kept the login of an app that can write")
	}
}

func TestGitHubTokenWarnsAndKeepsTheCurrentTokenWhileGitHubCannotRefreshIt(t *testing.T) {
	h := appSetup(t)
	h.expect(0, "github", "login")
	h.githubApp.RefreshStatus = http.StatusServiceUnavailable
	h.githubApp.Advance(8*time.Hour - 20*time.Minute)
	h.expect(0, "github", "token")
	if h.stdout.String() != "ghu_access1\n" {
		t.Errorf("stdout %q", h.stdout.String())
	}
	h.expectStderr("503 Service Unavailable; using the current token, which expires")
}
