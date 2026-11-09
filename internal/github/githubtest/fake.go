package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jystringfellow/scoring-stage/internal/github"
)

type Fake struct {
	t             *testing.T
	mu            sync.Mutex
	Handler       http.Handler
	now           time.Time
	Slept         []time.Duration
	PollAnswers   []map[string]any
	Polls         int
	RefreshToken  string
	Refreshes     int
	RefreshStatus int
	Permissions   map[string]map[string]string
	AccessTokens  map[string]bool
	issued        int
}

func NewFake(t *testing.T) *Fake {
	f := &Fake{
		t:            t,
		now:          time.Now().UTC().Truncate(time.Second),
		RefreshToken: "ghr_first",
		Permissions:  map[string]map[string]string{"acme": {"contents": "read", "metadata": "read"}},
		AccessTokens: map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/device/code", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("client_id") != "Iv23test" || r.Header.Get("Accept") != "application/json" {
			writeJSON(w, map[string]any{"error": "incorrect_client_credentials"})
			return
		}
		writeJSON(w, map[string]any{"device_code": "dev123", "user_code": "ABCD-1234", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5})
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Accept") != "application/json" || r.FormValue("client_id") != "Iv23test" || r.FormValue("client_secret") != "" {
			t.Errorf("unexpected token request: %v %v", r.Header, r.Form)
		}
		switch r.FormValue("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			if r.FormValue("device_code") != "dev123" {
				t.Errorf("device_code = %q", r.FormValue("device_code"))
			}
			f.Polls++
			if len(f.PollAnswers) > 0 {
				answer := f.PollAnswers[0]
				f.PollAnswers = f.PollAnswers[1:]
				if answer != nil {
					writeJSON(w, answer)
					return
				}
			}
			writeJSON(w, f.issue())
		case "refresh_token":
			f.Refreshes++
			if f.RefreshStatus != 0 {
				w.WriteHeader(f.RefreshStatus)
				return
			}
			if r.FormValue("refresh_token") != f.RefreshToken {
				writeJSON(w, map[string]any{"error": "bad_refresh_token", "error_description": "The refresh token passed is incorrect or expired."})
				return
			}
			writeJSON(w, f.issue())
		default:
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
	})
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !f.AccessTokens[token] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var installations []any
		for account, permissions := range f.Permissions {
			installations = append(installations, map[string]any{"account": map[string]any{"login": account}, "permissions": permissions})
		}
		writeJSON(w, map[string]any{"total_count": len(installations), "installations": installations})
	})
	f.Handler = mux
	return f
}

func (f *Fake) issue() map[string]any {
	f.issued++
	access := fmt.Sprintf("ghu_access%d", f.issued)
	f.RefreshToken = fmt.Sprintf("ghr_refresh%d", f.issued)
	f.AccessTokens[access] = true
	return map[string]any{"access_token": access, "expires_in": 28800, "refresh_token": f.RefreshToken, "refresh_token_expires_in": 15724800, "token_type": "bearer"}
}

type handlerTransport struct{ f *Fake }

func (h handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	h.f.Handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

func (f *Fake) Client() *github.Client {
	client := github.NewClient()
	client.HTTP.Transport = handlerTransport{f}
	client.WebURL, client.APIURL = "https://github.test", "https://api.github.test"
	client.Now = f.Now
	client.Sleep = func(d time.Duration) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.Slept = append(f.Slept, d)
		f.now = f.now.Add(d)
	}
	return client
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
