package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	GitHubService = "github"

	GitHubFineGrained = "fine-grained"
	GitHubGh          = "gh"
	GitHubNone        = "none"
)

var (
	GitHubModes = []string{GitHubFineGrained, GitHubApp, GitHubGh, GitHubNone}
	githubKeys  = []string{"mode", "app_client_id"}
	githubOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_-]*[A-Za-z0-9])?$`)
)

const gitHubTokenSuffix = ".token"

func decodeGitHub(value any, path string) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		if replacement, known := map[string]string{"readonly-token": GitHubFineGrained, GitHubNone: GitHubNone}[typed]; known {
			return "", errorf("%s: github is a table now; replace github = %q with [github] mode = %q", path, typed, replacement)
		}
		return "", errorf("%s: github must be a table such as [github] mode = %q (modes: %s)", path, GitHubFineGrained, quotedList(GitHubModes))
	case map[string]any:
		if err := checkKeys(typed, githubKeys, path+" [github]"); err != nil {
			return "", err
		}
		mode, present := typed["mode"]
		if !present {
			return "", nil
		}
		text, ok := mode.(string)
		if !ok || !contains(GitHubModes, text) {
			return "", errorf("%s [github]: mode must be one of %s", path, quotedList(GitHubModes))
		}
		return text, nil
	default:
		return "", errorf("%s: github must be a table such as [github] mode = %q", path, GitHubFineGrained)
	}
}

func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = `"` + value + `"`
	}
	return strings.Join(quoted, ", ")
}

func (s *Settings) GitHubMode() string { return s.githubMode }

func (s *Settings) GitHubTokenDir() string { return filepath.Join(s.Home, "secrets", "github") }

func (s *Settings) GitHubTokenFile(owner string) string {
	return filepath.Join(s.GitHubTokenDir(), strings.ToLower(owner)+gitHubTokenSuffix)
}

func (s *Settings) LegacyGitHubToken() string {
	return filepath.Join(s.Home, "secrets", "github-readonly-token")
}

func (s *Settings) noModeError() error {
	return errorf("choose how sandboxes reach GitHub: run `stage setup --github MODE` or set [github] mode in %s "+
		"(fine-grained: a read-only token per owner; app: your own read-only GitHub App; gh: your gh login, which can write; none: no GitHub credential)",
		filepath.Join(s.Home, "config.toml"))
}

func (s *Settings) CheckGitHub(owner, gh string) error {
	switch s.githubMode {
	case "":
		return s.noModeError()
	case GitHubNone:
		return nil
	case GitHubApp:
		return s.checkGitHubApp()
	case GitHubGh:
		if len(s.Managed.orUnmanaged().Paths) > 0 {
			return errorf("[github] mode = %q is refused while managed settings are in place (%s); use %q or %q",
				GitHubGh, s.Managed.where(), GitHubFineGrained, GitHubNone)
		}
		if gh == "" {
			return errorf("[github] mode = %q needs the gh CLI on the host, logged in with `gh auth login`", GitHubGh)
		}
		return nil
	}
	if !githubOwner.MatchString(owner) {
		return errorf("%q is not a GitHub owner name", owner)
	}
	file := s.GitHubTokenFile(owner)
	if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
		return s.missingTokenError(owner, file)
	}
	return checkTokenFile(file)
}

func (s *Settings) missingTokenError(owner, file string) error {
	create := "Create a fine-grained token with " + owner + " as its resource owner"
	if _, err := os.Stat(s.LegacyGitHubToken()); err == nil {
		create = "If " + s.LegacyGitHubToken() + " is a token for " + owner + ", move it there. Otherwise create a fine-grained token with " + owner + " as its resource owner"
	}
	return errorf("no GitHub token for %s at %s. %s, limited to the repositories you work on, with Contents: read and Metadata: read, "+
		"and save it there (mode 600). A fine-grained token covers one owner, so each owner you work in needs its own file; "+
		"or set [github] mode = %q to give sandboxes no GitHub credential", owner, file, create, GitHubNone)
}

func checkTokenFile(file string) error {
	info, statErr := os.Stat(file)
	content, err := os.ReadFile(file)
	token := strings.TrimSpace(string(content))
	if statErr != nil || err != nil {
		return errorf("cannot read %s: %v", file, errors.Join(statErr, err))
	}
	if token == "" {
		return errorf("%s is empty. Save a fine-grained, read-only token (github_pat_...) there", file)
	}
	if problem := GitHubTokenProblem(token); problem != "" {
		return errorf("%s %s. Replace it with a fine-grained, read-only token (github_pat_...)", file, problem)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errorf("%s is readable by other users (mode %o); run chmod 600 on it", file, info.Mode().Perm())
	}
	return nil
}

func (s *Settings) GitHubTokenOwners() ([]string, error) {
	entries, err := os.ReadDir(s.GitHubTokenDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var owners []string
	for _, entry := range entries {
		if owner, ok := strings.CutSuffix(entry.Name(), gitHubTokenSuffix); ok && !entry.IsDir() {
			owners = append(owners, strings.ToLower(owner))
		}
	}
	sort.Strings(owners)
	return owners, nil
}

var fineGrainedToken = regexp.MustCompile(`^github_pat_[A-Za-z0-9_]+$`)

func GitHubTokenProblem(token string) string {
	switch {
	case fineGrainedToken.MatchString(token):
		return ""
	case strings.HasPrefix(token, "github_pat_"):
		return "holds more than a single fine-grained token"
	case strings.HasPrefix(token, "gho_"):
		return "holds a gh CLI login token (gho_), which carries your full repository access"
	case strings.HasPrefix(token, "ghp_"):
		return "holds a classic personal access token (ghp_); a classic token cannot read private repositories without write access"
	case strings.HasPrefix(token, "ghu_"), strings.HasPrefix(token, "ghs_"), strings.HasPrefix(token, "ghr_"):
		return "holds a GitHub App token, whose permissions stage cannot check"
	default:
		return "does not hold a GitHub fine-grained token"
	}
}

func (s *Settings) gitHubCredential(selection Selection, secrets, bindings map[string]any) {
	if s.CheckGitHub(selection.GitHubOwner, selection.Gh) != nil {
		return
	}
	switch s.githubMode {
	case GitHubFineGrained:
		secrets[GitHubService] = map[string]any{"command": "/bin/cat " + ShellQuote(s.GitHubTokenFile(selection.GitHubOwner))}
	case GitHubGh:
		secrets[GitHubService] = map[string]any{"command": ShellQuote(selection.Gh) + " auth token"}
	case GitHubApp:
		secret, err := s.gitHubAppSecret()
		if err != nil {
			return
		}
		secrets[GitHubService] = secret
	default:
		return
	}
	bindings[GitHubService] = domains("apiKey", "api.github.com", "github.com")
}

func SaveGitHubMode(home, mode string) error {
	if !contains(GitHubModes, mode) {
		return errorf("--github must be one of %s", strings.Join(GitHubModes, ", "))
	}
	path := filepath.Join(home, "config.toml")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(existing) > 0 {
		data, err := readTOML(path)
		if err != nil {
			return err
		}
		if _, set := data["github"]; set {
			return errorf("%s already sets github; edit it there", path)
		}
		if !strings.HasSuffix(string(existing), "\n") {
			existing = append(existing, '\n')
		}
		existing = append(existing, '\n')
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(existing, []byte("[github]\nmode = \""+mode+"\"\n")...), 0o600)
}
