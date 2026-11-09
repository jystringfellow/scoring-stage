package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/github"
)

func (a *App) cmdGitHub(args []string, home string) (int, error) {
	fs := a.newFlags("github", "login|token")
	positional, err := fs.parse(args, "action")
	if err != nil {
		return 0, err
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	switch positional[0] {
	case "login":
		return 0, a.gitHubAppLogin(settings)
	case "token":
		token, err := a.gitHubAppToken(settings)
		if err != nil {
			return 0, err
		}
		if token.RefreshError != nil {
			a.warn("%s; using the current token, which expires %s", token.RefreshError, token.Login.ExpiresAt.Local().Format(time.RFC3339))
		}
		fmt.Fprintln(a.Stdout, token.Login.AccessToken)
		return 0, nil
	}
	return 0, &usageError{fmt.Sprintf("argument action: invalid choice: %q (choose from login, token)", positional[0])}
}

func (a *App) gitHubAppClientID(settings *config.Settings) (string, error) {
	if settings.GitHubMode() != config.GitHubApp {
		return "", errorf("stage github works only with [github] mode = %q; set it in %s", config.GitHubApp, filepath.Join(settings.Home, "config.toml"))
	}
	if settings.GitHubAppClientID() == "" {
		return "", settings.CheckGitHub("", "")
	}
	return settings.GitHubAppClientID(), nil
}

func (a *App) gitHubAppLogin(settings *config.Settings) error {
	clientID, err := a.gitHubAppClientID(settings)
	if err != nil {
		return err
	}
	token, err := a.GitHub.Login(settings.GitHubAppStore(), clientID, func(code github.DeviceCode) {
		fmt.Fprintf(a.Stderr, "stage: open %s and enter the code %s\n", code.VerificationURI, code.UserCode)
	})
	if err != nil {
		return err
	}
	a.say("logged in to the GitHub App %s; saved to %s", clientID, settings.GitHubAppStore().Path)
	a.reportGitHubAppToken(token)
	return nil
}

func (a *App) gitHubAppToken(settings *config.Settings) (github.Token, error) {
	clientID, err := a.gitHubAppClientID(settings)
	if err != nil {
		return github.Token{}, err
	}
	return a.GitHub.Token(settings.GitHubAppStore(), clientID)
}

func (a *App) checkGitHubApp(settings *config.Settings) error {
	token, err := a.gitHubAppToken(settings)
	if err != nil {
		return err
	}
	if len(token.Installations) == 0 {
		a.warn("the GitHub App is not installed on any account; sandboxes can read only public repositories")
	}
	return nil
}

func (a *App) reportGitHubApp(settings *config.Settings) {
	if err := settings.CheckGitHub("", ""); err != nil {
		a.warn("%s; stage up will refuse", err)
		return
	}
	token, err := a.gitHubAppToken(settings)
	if err != nil {
		a.warn("%s; stage up will refuse", err)
		return
	}
	a.reportGitHubAppToken(token)
}

func (a *App) reportGitHubAppToken(token github.Token) {
	login := token.Login
	expiry := "does not expire"
	if login.Expires() {
		expiry = "expires " + login.ExpiresAt.Local().Format(time.RFC3339) + "; stage refreshes it on the host"
	}
	refresh := "no refresh token"
	if login.RefreshToken != "" {
		refresh = "refresh token valid until " + login.RefreshTokenExpiresAt.Local().Format(time.RFC3339)
		if login.RefreshTokenExpiresAt.IsZero() {
			refresh = "refresh token without an expiry"
		}
	}
	a.say("github: app %s, logged in; access token %s; %s", login.ClientID, expiry, refresh)
	if len(token.Installations) == 0 {
		a.warn("the GitHub App is not installed on any account; install it on each organization whose repositories sandboxes read")
		return
	}
	a.say("github: the app is installed on %s, read-only; sandboxes can read what it can read there", strings.Join(github.Accounts(token.Installations), ", "))
}
