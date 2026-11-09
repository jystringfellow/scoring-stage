package config

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestGitHubTokenExpiryParsesTheFormatsGitHubSends(t *testing.T) {
	want := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	for _, value := range []string{
		"2026-12-01 10:00:00 UTC",
		"2026-12-01 10:00:00 GMT",
		"2026-12-01 10:00:00 +0000",
		"2026-12-01 02:00:00 -0800",
		"2026-12-01 02:00:00 -0800 PST",
		"2026-12-01T10:00:00Z",
		" 2026-12-01 10:00:00 UTC ",
	} {
		got, err := ParseGitHubTokenExpiry(value)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseGitHubTokenExpiry(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"", "tomorrow", "2026-12-01", "2026-12-01 10:00:00 PST"} {
		if _, err := ParseGitHubTokenExpiry(value); err == nil {
			t.Errorf("ParseGitHubTokenExpiry(%q) did not fail", value)
		}
	}
}

func TestGitHubTokenStatusComesFromTheStatusCodeAndExpiryHeader(t *testing.T) {
	var gotPath, gotAuth string
	transport := &testutil.HandlerTransport{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/repos/acme/api":
			w.Header().Set(GitHubTokenExpiryHeader, "2026-12-01 10:00:00 UTC")
		case "/repos/acme/odd":
			w.Header().Set(GitHubTokenExpiryHeader, "soon")
		case "/repos/acme/hidden":
			w.WriteHeader(http.StatusNotFound)
		case "/repos/acme/moved":
			http.Redirect(w, r, "https://example.com/elsewhere", http.StatusMovedPermanently)
		}
	})}
	api := GitHubAPI{BaseURL: "https://api.github.com/", Client: transport.Client()}

	status, err := api.CheckRepoAccess("github_pat_x", "acme", "api")
	if err != nil || status.StatusCode != 200 || !status.HasExpiry() || gotPath != "/repos/acme/api" || gotAuth != "Bearer github_pat_x" {
		t.Fatalf("status %+v, err %v, path %s, auth %s", status, err, gotPath, gotAuth)
	}
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	if !status.ExpiresSoon(now) || status.ExpiredAt(now) || status.ExpiresSoon(now.AddDate(0, 0, -30)) || !status.ExpiredAt(now.AddDate(0, 0, 3)) {
		t.Errorf("expiry checks wrong for %v", status.Expires)
	}

	status, err = api.CheckRepoAccess("github_pat_x", "acme", "odd")
	if err != nil || status.StatusCode != 200 || status.HasExpiry() || status.ExpiryProblem == "" {
		t.Errorf("unreadable expiry: %+v, %v", status, err)
	}
	if status, _ := api.CheckRepoAccess("github_pat_x", "acme", "hidden"); !status.CannotSee() || status.Rejected() {
		t.Errorf("404: %+v", status)
	}
	if status, _ := api.CheckRepoAccess("github_pat_x", "acme", "moved"); status.StatusCode != http.StatusMovedPermanently {
		t.Errorf("followed a redirect: %+v", status)
	}
	if status, _ := api.CheckToken("github_pat_x"); gotPath != "/rate_limit" || status.HasExpiry() {
		t.Errorf("CheckToken asked %s: %+v", gotPath, status)
	}
	transport.Unreachable()
	if _, err := api.CheckToken("github_pat_x"); err == nil || !strings.Contains(err.Error(), "cannot reach GitHub") || strings.Contains(err.Error(), "github_pat_x") {
		t.Errorf("unreachable: %v", err)
	}
}

func TestGitHubAPIDefaultsToApiGitHubCom(t *testing.T) {
	if api := NewGitHubAPI(); api.BaseURL != "https://api.github.com" || api.Client == nil || api.Client.Timeout == 0 {
		t.Errorf("NewGitHubAPI() = %+v", api)
	}
}
