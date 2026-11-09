package config

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	GitHubAPIURL             = "https://api.github.com"
	GitHubTokenExpiryHeader  = "GitHub-Authentication-Token-Expiration"
	GitHubTokenExpiryWarning = 7 * 24 * time.Hour
)

type GitHubAPI struct {
	BaseURL string
	Client  *http.Client
}

type GitHubTokenStatus struct {
	StatusCode    int
	Expires       time.Time
	ExpiryProblem string
}

func (s GitHubTokenStatus) HasExpiry() bool { return !s.Expires.IsZero() }

func (s GitHubTokenStatus) Rejected() bool { return s.StatusCode == http.StatusUnauthorized }

func (s GitHubTokenStatus) CannotSee() bool {
	return s.StatusCode == http.StatusNotFound || s.StatusCode == http.StatusForbidden
}

func (s GitHubTokenStatus) ExpiredAt(now time.Time) bool {
	return s.HasExpiry() && !s.Expires.After(now)
}

func (s GitHubTokenStatus) ExpiresSoon(now time.Time) bool {
	return s.HasExpiry() && s.Expires.Sub(now) < GitHubTokenExpiryWarning
}

func NewGitHubAPI() GitHubAPI {
	return GitHubAPI{BaseURL: GitHubAPIURL, Client: &http.Client{Timeout: 10 * time.Second}}
}

func (api GitHubAPI) CheckRepoAccess(token, owner, repo string) (GitHubTokenStatus, error) {
	return api.get(token, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo))
}

func (api GitHubAPI) CheckToken(token string) (GitHubTokenStatus, error) {
	return api.get(token, "/rate_limit")
}

func (api GitHubAPI) get(token, path string) (GitHubTokenStatus, error) {
	base := api.BaseURL
	if base == "" {
		base = GitHubAPIURL
	}
	request, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(base, "/")+path, nil)
	if err != nil {
		return GitHubTokenStatus{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := http.Client{Timeout: 10 * time.Second}
	if api.Client != nil {
		client = *api.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return GitHubTokenStatus{}, errorf("cannot reach GitHub: %v", err)
	}
	response.Body.Close()
	status := GitHubTokenStatus{StatusCode: response.StatusCode}
	if value := response.Header.Get(GitHubTokenExpiryHeader); value != "" {
		if status.Expires, err = ParseGitHubTokenExpiry(value); err != nil {
			status.ExpiryProblem = err.Error()
		}
	}
	return status, nil
}

var gitHubExpiryLayouts = []string{
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05 -0700 MST",
	"2006-01-02 15:04:05 UTC",
	"2006-01-02 15:04:05 GMT",
	time.RFC3339,
}

func ParseGitHubTokenExpiry(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range gitHubExpiryLayouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errorf("GitHub sent a token expiry stage cannot read: %q", value)
}

func CheckGitHubOwner(owner string) error {
	if !githubOwner.MatchString(owner) {
		return errorf("%q is not a GitHub owner name; use the user or organization that owns the repositories, such as acme in github.com/acme/api", owner)
	}
	return nil
}

func (s *Settings) GitHubToken(owner string) (string, error) {
	content, err := os.ReadFile(s.GitHubTokenFile(owner))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}
