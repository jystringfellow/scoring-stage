package github_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/github"
	"github.com/jystringfellow/scoring-stage/internal/github/githubtest"
)

func testStore(t *testing.T) github.Store {
	return github.Store{Path: filepath.Join(t.TempDir(), "secrets", "github", "app.json")}
}

func login(t *testing.T, f *githubtest.Fake, store github.Store) github.Token {
	t.Helper()
	token, err := f.Client().Login(store, "Iv23test", func(github.DeviceCode) {})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestDeviceFlowWaitsThroughPendingAndSlowDownThenSavesThePrivateLogin(t *testing.T) {
	f := githubtest.NewFake(t)
	f.PollAnswers = []map[string]any{{"error": "authorization_pending"}, {"error": "slow_down", "interval": 10}, {"error": "authorization_pending"}}
	store := testStore(t)
	var shown github.DeviceCode
	token, err := f.Client().Login(store, "Iv23test", func(code github.DeviceCode) { shown = code })
	if err != nil {
		t.Fatal(err)
	}
	if shown.UserCode != "ABCD-1234" || shown.VerificationURI != "https://github.com/login/device" {
		t.Errorf("shown %+v", shown)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	if fmt.Sprint(f.Slept) != fmt.Sprint(want) || f.Polls != 4 {
		t.Errorf("slept %v over %d polls; want %v over 4", f.Slept, f.Polls, want)
	}
	saved, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "ghu_access1" || saved.RefreshToken != "ghr_refresh1" || saved.ClientID != "Iv23test" {
		t.Errorf("saved %+v", saved)
	}
	if !saved.ExpiresAt.Equal(f.Now().Add(8*time.Hour)) || !saved.RefreshTokenExpiresAt.Equal(f.Now().Add(15724800*time.Second)) {
		t.Errorf("saved expiries %v %v", saved.ExpiresAt, saved.RefreshTokenExpiresAt)
	}
	if info, _ := os.Stat(store.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("app.json mode %o", info.Mode().Perm())
	}
	if fmt.Sprint(github.Accounts(token.Installations)) != "[acme]" {
		t.Errorf("installations %+v", token.Installations)
	}
}

func TestDeviceFlowStopsWhenTheCodeExpiresOrIsDenied(t *testing.T) {
	for answer, want := range map[string]string{"expired_token": "expired before it was entered", "access_denied": "cancelled on GitHub", "device_flow_disabled": "turn on Enable Device Flow"} {
		f := githubtest.NewFake(t)
		f.PollAnswers = []map[string]any{{"error": "authorization_pending"}, {"error": answer}}
		store := testStore(t)
		_, err := f.Client().Login(store, "Iv23test", func(github.DeviceCode) {})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", answer, err)
		}
		if _, statErr := os.Stat(store.Path); statErr == nil {
			t.Errorf("%s: saved a login", answer)
		}
	}
}

func TestDeviceFlowGivesUpAfterTheCodeLifetime(t *testing.T) {
	f := githubtest.NewFake(t)
	for range 400 {
		f.PollAnswers = append(f.PollAnswers, map[string]any{"error": "authorization_pending"})
	}
	_, err := f.Client().Login(testStore(t), "Iv23test", func(github.DeviceCode) {})
	if err == nil || !strings.Contains(err.Error(), "expired before it was entered") || f.Polls != 180 {
		t.Errorf("got %v after %d polls", err, f.Polls)
	}
}

func TestLoginRefusesAnAppThatCanWriteAndSavesNothing(t *testing.T) {
	f := githubtest.NewFake(t)
	f.Permissions["globex"] = map[string]string{"contents": "write", "metadata": "read", "administration": "admin"}
	store := testStore(t)
	_, err := f.Client().Login(store, "Iv23test", func(github.DeviceCode) {})
	if err == nil || !strings.Contains(err.Error(), "the installation on globex grants administration: admin, contents: write") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "acme") {
		t.Errorf("blamed the read-only installation: %v", err)
	}
	if _, statErr := os.Stat(store.Path); statErr == nil {
		t.Error("saved a login for an app that can write")
	}
}

func TestTokenReturnsTheStoredTokenWithoutCallingGitHubWhileItIsFresh(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	f.Handler = http.NotFoundHandler()
	token, err := f.Client().Token(store, "Iv23test", false)
	if err != nil || token.Login.AccessToken != "ghu_access1" || token.Refreshed {
		t.Fatalf("got %+v, %v", token, err)
	}
}

func TestTokenRefreshesNearExpiryAndStoresTheRotatedRefreshToken(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	f.Advance(8*time.Hour - github.RefreshMargin + time.Second)
	token, err := f.Client().Token(store, "Iv23test", false)
	if err != nil {
		t.Fatal(err)
	}
	if !token.Refreshed || !token.Checked || token.Login.AccessToken != "ghu_access2" {
		t.Errorf("got %+v", token)
	}
	saved, _ := store.Read()
	if saved.RefreshToken != "ghr_refresh2" || !saved.ExpiresAt.Equal(f.Now().Add(8*time.Hour)) {
		t.Errorf("saved %+v", saved)
	}
	again, err := f.Client().Token(store, "Iv23test", false)
	if err != nil || again.Refreshed || again.Login.AccessToken != "ghu_access2" || f.Refreshes != 1 {
		t.Errorf("second call %+v, %v after %d refreshes", again, err, f.Refreshes)
	}
}

func TestConcurrentTokenCallsRefreshOnceUnderTheLock(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	f.Advance(8 * time.Hour)
	var wait sync.WaitGroup
	tokens := make([]string, 8)
	errs := make([]error, 8)
	for i := range tokens {
		wait.Add(1)
		go func() {
			defer wait.Done()
			token, err := f.Client().Token(store, "Iv23test", false)
			tokens[i], errs[i] = token.Login.AccessToken, err
		}()
	}
	wait.Wait()
	for i := range tokens {
		if errs[i] != nil || tokens[i] != "ghu_access2" {
			t.Errorf("call %d: %q, %v", i, tokens[i], errs[i])
		}
	}
	if f.Refreshes != 1 {
		t.Errorf("refreshed %d times", f.Refreshes)
	}
}

func TestTokenAsksForANewLoginOnceTheRefreshTokenExpired(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	f.Advance(15724800 * time.Second)
	_, err := f.Client().Token(store, "Iv23test", false)
	if err == nil || !strings.Contains(err.Error(), "expired; run `stage github login`") || f.Refreshes != 0 {
		t.Errorf("got %v after %d refreshes", err, f.Refreshes)
	}
}

func TestTokenAsksForANewLoginWhenGitHubRefusesTheRefreshToken(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	f.RefreshToken = "ghr_revoked"
	f.Advance(8 * time.Hour)
	_, err := f.Client().Token(store, "Iv23test", false)
	if err == nil || !strings.Contains(err.Error(), "refused the stored refresh token") || !strings.Contains(err.Error(), "stage github login") {
		t.Errorf("got %v", err)
	}
}

func TestTokenRemovesTheLoginWhenARefreshFindsWritePermissions(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	f.Permissions["acme"]["pull_requests"] = "write"
	f.Advance(8 * time.Hour)
	_, err := f.Client().Token(store, "Iv23test", false)
	if err == nil || !strings.Contains(err.Error(), "the installation on acme grants pull_requests: write") {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(store.Path); statErr == nil {
		t.Error("kept the login of an app that can write")
	}
}

func TestTokenChecksPermissionsWhenAsked(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	f.Permissions["acme"]["issues"] = "write"
	if _, err := f.Client().Token(store, "Iv23test", false); err != nil {
		t.Fatalf("unchecked token: %v", err)
	}
	if _, err := f.Client().Token(store, "Iv23test", true); err == nil || !strings.Contains(err.Error(), "issues: write") {
		t.Errorf("checked token: %v", err)
	}
}

func TestTokenRefusesALoginForAnotherClientID(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	_, err := f.Client().Token(store, "Iv23other", false)
	if err == nil || !strings.Contains(err.Error(), "client ID Iv23test, but [github] app_client_id is Iv23other") {
		t.Errorf("got %v", err)
	}
}

func TestTokenWithoutALoginSaysToLogIn(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	_, err := f.Client().Token(store, "Iv23test", false)
	if err == nil || !strings.Contains(err.Error(), "not logged in to the GitHub App") || !strings.Contains(err.Error(), "stage github login") {
		t.Errorf("got %v", err)
	}
}

func TestAnAppWithoutTokenExpiryNeverRefreshes(t *testing.T) {
	f := githubtest.NewFake(t)
	f.PollAnswers = []map[string]any{{"access_token": "ghu_forever", "token_type": "bearer"}}
	f.AccessTokens["ghu_forever"] = true
	store := testStore(t)
	login(t, f, store)
	saved, _ := store.Read()
	if saved.Expires() || saved.RefreshToken != "" {
		t.Fatalf("saved %+v", saved)
	}
	f.Advance(365 * 24 * time.Hour)
	token, err := f.Client().Token(store, "Iv23test", false)
	if err != nil || token.Login.AccessToken != "ghu_forever" || f.Refreshes != 0 {
		t.Errorf("got %+v, %v", token, err)
	}
}

func TestLoginRefusesATokenThatIsNotAGitHubAppUserToken(t *testing.T) {
	f := githubtest.NewFake(t)
	f.PollAnswers = []map[string]any{{"access_token": "gho_oauthapp", "token_type": "bearer"}}
	_, err := f.Client().Login(testStore(t), "Iv23test", func(github.DeviceCode) {})
	if err == nil || !strings.Contains(err.Error(), "not a GitHub App user token") {
		t.Errorf("got %v", err)
	}
}

func TestStoreRefusesALoginOthersCanRead(t *testing.T) {
	f := githubtest.NewFake(t)
	store := testStore(t)
	login(t, f, store)
	if err := os.Chmod(store.Path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("got %v", err)
	}
}
