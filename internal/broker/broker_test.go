package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const credentials = `{"Version": 1, "AccessKeyId": "ASIADEV", "SecretAccessKey": "devsecret", "SessionToken": "devtoken", "Expiration": "2026-11-06T13:00:00+00:00"}`

var granted = map[string]bool{"dev": true, "staging": true}

type fakeAWS struct {
	result Result
	err    error
	calls  [][]string
}

func (f *fakeAWS) run(ctx context.Context, argv []string) (Result, error) {
	f.calls = append(f.calls, argv)
	if _, ok := ctx.Deadline(); !ok {
		return Result{}, errors.New("no timeout")
	}
	return f.result, f.err
}

func ok() *fakeAWS { return &fakeAWS{result: Result{Stdout: credentials}} }

func status(t *testing.T, err error) int {
	t.Helper()
	var brokerErr *Error
	if !errors.As(err, &brokerErr) {
		t.Fatalf("error %v is not a broker error", err)
	}
	return brokerErr.Status
}

func TestGrantedProfileIsExportedThroughTheAWSCLI(t *testing.T) {
	aws := ok()
	got, err := NewCredentialSource(aws.run).CredentialProcess("dev", granted)
	if err != nil {
		t.Fatal(err)
	}
	var decoded, want map[string]any
	json.Unmarshal(got, &decoded)
	json.Unmarshal([]byte(credentials), &want)
	if !reflect.DeepEqual(decoded, want) {
		t.Errorf("credentials = %s", got)
	}
	if !reflect.DeepEqual(aws.calls[0], []string{"aws", "configure", "export-credentials", "--profile", "dev", "--format", "process"}) {
		t.Errorf("argv = %v", aws.calls[0])
	}
}

func TestUngrantedProfileIsRefusedWithoutCallingTheAWSCLI(t *testing.T) {
	aws := ok()
	_, err := NewCredentialSource(aws.run).CredentialProcess("prod", granted)
	if status(t, err) != 403 || len(aws.calls) != 0 {
		t.Fatalf("err %v calls %v", err, aws.calls)
	}
}

func TestProfileNamesAreMatchedExactly(t *testing.T) {
	_, err := NewCredentialSource(ok().run).CredentialProcess("dev/../prod", granted)
	if status(t, err) != 403 {
		t.Fatal(err)
	}
}

func TestExpiredHostSessionAsksForAHostLogin(t *testing.T) {
	aws := &fakeAWS{result: Result{ExitCode: 255, Stderr: "noise\nError when retrieving token from sso: Token has expired\n"}}
	_, err := NewCredentialSource(aws.run).CredentialProcess("staging", granted)
	if status(t, err) != 503 || !strings.Contains(err.Error(), "Token has expired; refresh the profile's login on the host") {
		t.Fatal(err)
	}
}

func TestMissingOrHungAWSCLIIsAServiceError(t *testing.T) {
	for _, failure := range []error{exec.ErrNotFound, context.DeadlineExceeded} {
		_, err := NewCredentialSource((&fakeAWS{err: failure}).run).CredentialProcess("dev", granted)
		if status(t, err) != 503 {
			t.Errorf("%v: %v", failure, err)
		}
	}
}

func TestTheRealRunnerTimesOutAndReportsAMissingBinary(t *testing.T) {
	script := filepath.Join(t.TempDir(), "aws")
	os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755)
	source := NewCredentialSource(ExecRunner)
	source.AWS, source.Timeout = script, 200*time.Millisecond
	started := time.Now()
	_, err := source.CredentialProcess("dev", granted)
	if status(t, err) != 503 || time.Since(started) > 3*time.Second {
		t.Errorf("timeout: %v after %v", err, time.Since(started))
	}
	source.AWS = filepath.Join(t.TempDir(), "missing-aws")
	_, err = source.CredentialProcess("dev", granted)
	if status(t, err) != 503 || !strings.Contains(err.Error(), "aws cli failed") {
		t.Errorf("missing: %v", err)
	}
}

func TestUnusableAWSCLIOutputIsAGatewayError(t *testing.T) {
	for _, stdout := range []string{"not json", `{"Version": 1, "AccessKeyId": "ASIA"}`, `["list"]`} {
		_, err := NewCredentialSource((&fakeAWS{result: Result{Stdout: stdout}}).run).CredentialProcess("dev", granted)
		if status(t, err) != 502 {
			t.Errorf("%q: %v", stdout, err)
		}
	}
}

func issued(t *testing.T) Grants {
	t.Helper()
	grants := Grants{Path: filepath.Join(t.TempDir(), "grants")}
	for owner, profiles := range map[string][]string{"api": {"dev", "staging"}, "web": {"dev"}} {
		if _, err := grants.Issue(owner, profiles); err != nil {
			t.Fatal(err)
		}
	}
	return grants
}

func tokenOf(t *testing.T, grants Grants, owner string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(grants.Path, owner+".token"))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func expectGrant(t *testing.T, grants Grants, token, wantOwner string, wantProfiles ...string) {
	t.Helper()
	owner, profiles, found := grants.Lookup(token)
	var names []string
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	if !found || owner != wantOwner || strings.Join(names, " ") != strings.Join(wantProfiles, " ") {
		t.Fatalf("lookup = %q %v %v; want %q %v", owner, names, found, wantOwner, wantProfiles)
	}
}

func TestEachTokenIdentifiesItsSandboxAndItsProfiles(t *testing.T) {
	grants := issued(t)
	expectGrant(t, grants, tokenOf(t, grants, "api"), "api", "dev", "staging")
	expectGrant(t, grants, tokenOf(t, grants, "web"), "web", "dev")
	if len(tokenOf(t, grants, "api")) != 64 {
		t.Error("token is not 32 random bytes in hex")
	}
}

func TestGrantFilesArePrivate(t *testing.T) {
	grants := issued(t)
	info, _ := os.Stat(grants.Path)
	if info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(grants.Path)
	for _, entry := range entries {
		info, _ := entry.Info()
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", entry.Name(), info.Mode().Perm())
		}
	}
}

func TestReissuingKeepsTheTokenAndReplacesTheProfiles(t *testing.T) {
	grants := issued(t)
	token := tokenOf(t, grants, "web")
	grants.Issue("web", []string{"staging"})
	expectGrant(t, grants, token, "web", "staging")
}

func TestRevokedTokensStopWorking(t *testing.T) {
	grants := issued(t)
	token := tokenOf(t, grants, "api")
	grants.Revoke("api")
	if _, _, found := grants.Lookup(token); found {
		t.Fatal("revoked token still works")
	}
	entries, _ := os.ReadDir(grants.Path)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, " ") != "web.profiles web.token" {
		t.Fatalf("left %v", names)
	}
}

func TestUnknownOrEmptyTokensIdentifyNobody(t *testing.T) {
	grants := issued(t)
	os.WriteFile(filepath.Join(grants.Path, "notes.txt"), []byte("wrong"), 0o600)
	os.WriteFile(filepath.Join(grants.Path, "empty.token"), nil, 0o600)
	for _, supplied := range []string{"", "wrong"} {
		if _, _, found := grants.Lookup(supplied); found {
			t.Errorf("%q identified someone", supplied)
		}
	}
}

func TestATokenWithoutAProfilesFileGrantsNothing(t *testing.T) {
	grants := issued(t)
	os.Remove(filepath.Join(grants.Path, "web.profiles"))
	expectGrant(t, grants, tokenOf(t, grants, "web"), "web")
}

type server struct {
	handler http.Handler
	lines   []string
}

func newServer(grants Grants) *server {
	s := &server{}
	s.handler = Handler(NewCredentialSource(ok().run), grants, func(line string) { s.lines = append(s.lines, line) })
	return s
}

func (s *server) fetch(method, path, authorization string) (int, map[string]any) {
	request := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	var body map[string]any
	json.Unmarshal(recorder.Body.Bytes(), &body)
	return recorder.Code, body
}

func TestMain(m *testing.M) {
	now = func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) }
	os.Exit(m.Run())
}

func TestOnlyTemporaryCredentialsThatExpireSoonAreServed(t *testing.T) {
	for stdout, fragment := range map[string]string{
		`{"Version": 1, "AccessKeyId": "AKIAUSER", "SecretAccessKey": "s"}`:                                                                 "long-lived keys are not served",
		`{"Version": 1, "AccessKeyId": "AKIAUSER", "SecretAccessKey": "s", "SessionToken": "t", "Expiration": "2026-11-06T13:00:00+00:00"}`: "long-lived keys are not served",
		`{"Version": 1, "AccessKeyId": "ASIA", "SecretAccessKey": "s", "Expiration": "2026-11-06T13:00:00+00:00"}`:                          "long-lived keys are not served",
		`{"Version": 1, "AccessKeyId": "ASIA", "SecretAccessKey": "s", "SessionToken": "t", "Expiration": "tomorrow"}`:                      "unreadable Expiration",
		`{"Version": 1, "AccessKeyId": "ASIA", "SecretAccessKey": "s", "SessionToken": "t", "Expiration": "2026-11-06T11:00:00+00:00"}`:     "have expired",
		`{"Version": 1, "AccessKeyId": "ASIA", "SecretAccessKey": "s", "SessionToken": "t", "Expiration": "2026-11-07T12:00:00+00:00"}`:     "stay valid for 24h0m0s",
	} {
		_, err := NewCredentialSource((&fakeAWS{result: Result{Stdout: stdout}}).run).CredentialProcess("dev", granted)
		if status(t, err) != 403 || !strings.Contains(err.Error(), fragment) {
			t.Errorf("%s: %v, want %q", stdout, err, fragment)
		}
	}
}

func TestHTTPRequiresASandboxBearerToken(t *testing.T) {
	grants := issued(t)
	s := newServer(grants)
	token := tokenOf(t, grants, "api")
	for _, authorization := range []string{"", "Bearer wrong", token, "Basic " + token} {
		code, body := s.fetch("GET", "/credential-process/dev", authorization)
		if code != 401 || body["AccessKeyId"] != nil {
			t.Errorf("%q: %d %v", authorization, code, body)
		}
	}
}

func TestHTTPServesGrantedCredentialsAndLogsTheSandboxWithoutSecrets(t *testing.T) {
	grants := issued(t)
	s := newServer(grants)
	code, body := s.fetch("GET", "/credential-process/staging", "Bearer "+tokenOf(t, grants, "api"))
	if code != 200 || body["AccessKeyId"] != "ASIADEV" {
		t.Fatalf("%d %v", code, body)
	}
	if !reflect.DeepEqual(s.lines, []string{"broker: api GET /credential-process/staging 200"}) {
		t.Fatalf("log = %v", s.lines)
	}
}

func TestHTTPRefusesAProfileGrantedOnlyToAnotherSandbox(t *testing.T) {
	grants := issued(t)
	if code, _ := newServer(grants).fetch("GET", "/credential-process/staging", "Bearer "+tokenOf(t, grants, "web")); code != 403 {
		t.Fatal(code)
	}
}

func TestHTTPUnknownPathIsNotFound(t *testing.T) {
	grants := issued(t)
	if code, _ := newServer(grants).fetch("GET", "/other", "Bearer "+tokenOf(t, grants, "web")); code != 404 {
		t.Fatal(code)
	}
}

func TestHTTPOnlyServesGET(t *testing.T) {
	grants := issued(t)
	if code, _ := newServer(grants).fetch("POST", "/credential-process/dev", "Bearer "+tokenOf(t, grants, "web")); code != 501 {
		t.Fatal(code)
	}
}
