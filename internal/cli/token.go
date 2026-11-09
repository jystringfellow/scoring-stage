package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/config"
)

const maxTokenInput = 64 * 1024

func (a *App) cmdToken(args []string, home string) (int, error) {
	fs := a.newFlags("token", "set <owner>")
	positional, err := fs.parse(args, "action", "owner")
	if err != nil {
		return 0, err
	}
	if positional[0] != "set" {
		return 0, &usageError{fmt.Sprintf("argument action: invalid choice: %q (choose from set)", positional[0])}
	}
	owner := positional[1]
	if err := config.CheckGitHubOwner(owner); err != nil {
		return 0, err
	}
	settings, err := config.LoadSettings(home)
	if err != nil {
		return 0, err
	}
	token, err := a.readToken(owner)
	if err != nil {
		return 0, err
	}
	if token == "" {
		return 0, errorf("no token given; paste a fine-grained token (github_pat_...) for %s, or pipe it on stdin", owner)
	}
	if problem := config.GitHubTokenProblem(token); problem != "" {
		return 0, errorf("the token you gave %s. Create a fine-grained, read-only token (github_pat_...) with %s as its resource owner", problem, owner)
	}
	status, checkErr := a.GitHubAPI.CheckToken(token)
	switch {
	case checkErr != nil:
		a.warn("could not check the token with GitHub (%s); saving it unchecked", checkErr)
	case status.Rejected():
		return 0, errorf("GitHub rejected the token (401: expired, revoked or mistyped); nothing was saved. Create a new fine-grained token for %s and run `stage token set %s` again", owner, owner)
	case status.StatusCode >= 300:
		a.warn("GitHub answered %d when checking the token; saving it anyway", status.StatusCode)
	}
	file := settings.GitHubTokenFile(owner)
	_, statErr := os.Stat(file)
	replaced := statErr == nil
	if err := writeSecretFile(file, token+"\n"); err != nil {
		return 0, err
	}
	if replaced {
		a.say("replaced the GitHub token for %s at %s", strings.ToLower(owner), file)
	} else {
		a.say("saved the GitHub token for %s at %s", strings.ToLower(owner), file)
	}
	if checkErr == nil {
		a.reportTokenExpiry(strings.ToLower(owner), status, time.Now(), true)
	}
	if settings.GitHubMode() != config.GitHubFineGrained {
		a.warn("sandboxes use this token only with [github] mode = %q; run `stage setup --github %s` or set it in %s",
			config.GitHubFineGrained, config.GitHubFineGrained, filepath.Join(home, "config.toml"))
	}
	return 0, nil
}

func (a *App) readToken(owner string) (string, error) {
	if a.Stdin == nil {
		return "", errorf("no stdin to read the token from")
	}
	if a.StdinIsTerminal != nil && a.StdinIsTerminal() && a.ReadSecret != nil {
		fmt.Fprintf(a.Stderr, "GitHub fine-grained token for %s (input hidden): ", owner)
		secret, err := a.ReadSecret()
		fmt.Fprintln(a.Stderr)
		if err != nil {
			return "", errorf("cannot read the token from the terminal: %v", err)
		}
		return strings.TrimSpace(string(secret)), nil
	}
	content, err := io.ReadAll(io.LimitReader(a.Stdin, maxTokenInput+1))
	if err != nil {
		return "", errorf("cannot read the token from stdin: %v", err)
	}
	if len(content) > maxTokenInput {
		return "", errorf("stdin holds more than a token; pipe only the token, such as `pbpaste | stage token set %s`", owner)
	}
	return strings.TrimSpace(string(content)), nil
}

func writeSecretFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".token-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.WriteString(content)
	if err := errors.Join(writeErr, temporary.Chmod(0o600), temporary.Sync(), temporary.Close()); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func (a *App) checkGitHubTokenForRepo(settings *config.Settings, github string) error {
	if settings.GitHubMode() != config.GitHubFineGrained {
		return nil
	}
	owner, repo, _ := strings.Cut(github, "/")
	token, err := settings.GitHubToken(owner)
	if err != nil {
		return err
	}
	status, err := a.GitHubAPI.CheckRepoAccess(token, owner, repo)
	if err != nil {
		a.warn("could not check the GitHub token for %s (%s); continuing", strings.ToLower(owner), err)
		return nil
	}
	now := time.Now()
	if status.Rejected() {
		return errorf("GitHub rejected the token for %s at %s (401: expired or revoked). Create a new fine-grained token and run `stage token set %s`",
			strings.ToLower(owner), settings.GitHubTokenFile(owner), strings.ToLower(owner))
	}
	switch {
	case status.StatusCode == http.StatusNotFound:
		a.warn("the GitHub token for %s cannot see %s (GitHub answered 404), so the sandbox cannot read it. Add the repository to the token, "+
			"or check that the token was created with %s as its resource owner; `stage token set %s` replaces it",
			strings.ToLower(owner), github, owner, strings.ToLower(owner))
	case status.StatusCode == http.StatusForbidden:
		a.warn("the GitHub token for %s cannot see %s (GitHub answered 403), so the sandbox may not be able to read it. Likely causes: "+
			"the organization uses SAML SSO and the token is not authorized for it (authorize it in the token's settings), "+
			"the organization has not yet approved the token, a rate limit, or the token does not include the repository or "+
			"was created for another resource owner; `stage token set %s` replaces it",
			strings.ToLower(owner), github, strings.ToLower(owner))
	case status.StatusCode >= 300:
		a.warn("GitHub answered %d when checking the token for %s against %s; continuing", status.StatusCode, strings.ToLower(owner), github)
	}
	a.reportTokenExpiry(strings.ToLower(owner), status, now, false)
	return nil
}

func (a *App) reportTokenExpiry(owner string, status config.GitHubTokenStatus, now time.Time, verbose bool) {
	switch {
	case status.ExpiryProblem != "":
		a.warn("%s", status.ExpiryProblem)
	case status.ExpiredAt(now) && status.StatusCode >= 200 && status.StatusCode < 300:
		a.warn("GitHub accepted the token for %s, but its expiry header (%s) reads as past; check this machine's clock", owner, status.ExpiryHeader)
	case status.ExpiredAt(now):
		a.warn("GitHub answered %d for the token for %s, and its expiry header (%s) reads as past; if the token has expired, create a new one "+
			"and run `stage token set %s`, otherwise check this machine's clock", status.StatusCode, owner, status.ExpiryHeader, owner)
	case status.ExpiresSoon(now):
		a.warn("the GitHub token for %s expires %s; create a new one and run `stage token set %s`", owner, describeExpiry(status.Expires, now), owner)
	case !verbose:
	case status.HasExpiry():
		a.say("github token for %s expires %s", owner, describeExpiry(status.Expires, now))
	default:
		a.say("github token for %s has no expiry", owner)
	}
}

func (a *App) reportGitHubTokenStatus(settings *config.Settings, owner string) {
	token, err := settings.GitHubToken(owner)
	if err != nil {
		a.warn("cannot read the GitHub token for %s: %v", owner, err)
		return
	}
	status, err := a.GitHubAPI.CheckToken(token)
	if err != nil {
		a.warn("could not check the GitHub token for %s (%s)", owner, err)
		return
	}
	if status.Rejected() {
		a.warn("GitHub rejected the token for %s (401: expired or revoked); stage up will refuse. Create a new one and run `stage token set %s`", owner, owner)
		return
	}
	a.reportTokenExpiry(owner, status, time.Now(), true)
}

func describeExpiry(expires, now time.Time) string {
	when := expires.Format("2006-01-02 15:04 MST")
	days := int(expires.Sub(now).Hours() / 24)
	switch {
	case days < 1:
		return when + " (in less than a day)"
	case days == 1:
		return when + " (in 1 day)"
	default:
		return fmt.Sprintf("%s (in %d days)", when, days)
	}
}
