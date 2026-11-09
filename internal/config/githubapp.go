package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/github"
)

const (
	GitHubApp = "app"

	GitHubAppSecretRefresh = "30m"
)

var (
	githubAppClientID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{3,63}$`)
	StageExecutable   = os.Executable
	gitHubAppNow      = time.Now
)

func decodeGitHubAppClientID(value any, path string) (string, error) {
	table, _ := value.(map[string]any)
	raw, present := table["app_client_id"]
	if !present {
		return "", nil
	}
	text, ok := raw.(string)
	if !ok || !githubAppClientID.MatchString(text) {
		return "", errorf("%s [github]: app_client_id must be a GitHub App's Client ID, such as \"Iv23li...\", from the app's settings page", path)
	}
	return text, nil
}

func (s *Settings) GitHubAppClientID() string {
	if pinned := s.Managed.GitHubAppClientID(); pinned != "" {
		return pinned
	}
	return s.githubApp
}

func (s *Settings) IgnoredGitHubAppClientID() string {
	pinned := s.Managed.GitHubAppClientID()
	if pinned == "" || s.githubApp == "" || s.githubApp == pinned {
		return ""
	}
	return fmt.Sprintf("%s [github]: app_client_id %s is ignored; %s pins the GitHub App %s, so remove app_client_id or set it to %s",
		filepath.Join(s.Home, "config.toml"), s.githubApp, s.Managed.appClientIDFile, pinned, pinned)
}

func (s *Settings) GitHubAppStore() github.Store {
	return github.Store{Path: filepath.Join(s.GitHubTokenDir(), "app.json")}
}

func (s *Settings) checkGitHubApp() error {
	clientID := s.GitHubAppClientID()
	if clientID == "" {
		return errorf("[github] mode = %q needs the Client ID of your GitHub App: set app_client_id = \"Iv23...\" under [github] in %s, "+
			"then run `stage github login`", GitHubApp, filepath.Join(s.Home, "config.toml"))
	}
	store := s.GitHubAppStore()
	login, err := store.Read()
	if err != nil {
		return err
	}
	if login.ClientID != clientID {
		return errorf("%s holds a login for GitHub App client ID %s, but [github] app_client_id is %s; run `stage github login`",
			store.Path, login.ClientID, clientID)
	}
	if !login.Usable(gitHubAppNow()) {
		return errorf("the GitHub App login in %s expired; run `stage github login`", store.Path)
	}
	return nil
}

func (s *Settings) GitHubAppTokenCommand() (string, error) {
	executable, err := StageExecutable()
	if err != nil {
		return "", errorf("cannot find the stage binary for the GitHub App token command: %v", err)
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	if onPath, err := exec.LookPath("stage"); err == nil {
		onPath, _ = filepath.Abs(onPath)
		if sameFile(onPath, absolute) {
			absolute = onPath
		}
	}
	return "/usr/bin/env STAGE_HOME=" + ShellQuote(s.Home) + " " + ShellQuote(absolute) + " github token", nil
}

func sameFile(a, b string) bool {
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}

func (s *Settings) gitHubAppSecret() (map[string]any, error) {
	command, err := s.GitHubAppTokenCommand()
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": command, "refresh": GitHubAppSecretRefresh}, nil
}
