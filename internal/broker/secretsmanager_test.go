package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const secretValue = "s3cr3t-value-marker"

type catalogEntry struct {
	name, description string
	tags              map[string]string
	created           float64
}

var catalog = []catalogEntry{
	{"app/config", "Main Config", map[string]string{"team": "core"}, 300},
	{"app/config-old", "", nil, 100},
	{"app/db", "Database", map[string]string{"team": "data"}, 200},
	{"shared/key", "Signing key", map[string]string{"owner": "core"}, 400},
	{"other/secret", "", nil, 500},
}

func arnOf(name string) string {
	return "arn:aws:secretsmanager:us-east-1:111122223333:secret:" + name + "-AbC123"
}

type awsCall struct {
	target, authorization, token string
	body                         map[string]any
}

type fakeSecretsManager struct {
	calls    []awsCall
	answer   func(target string, body map[string]any) (int, any)
	nameFor  func(id string) string
	pageSize int

	redirect    string
	endlessList bool
	batchPage   int
	batchErrors map[string]map[string]any
}

func (f *fakeSecretsManager) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	json.Unmarshal(raw, &body)
	target := r.Header.Get("X-Amz-Target")
	f.calls = append(f.calls, awsCall{target, r.Header.Get("Authorization"), r.Header.Get("X-Amz-Security-Token"), body})
	if f.redirect != "" {
		http.Redirect(w, r, f.redirect, http.StatusTemporaryRedirect)
		return
	}
	status, payload := f.respond(target, body)
	encoded, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("X-Amzn-Requestid", "request-1")
	w.WriteHeader(status)
	w.Write(encoded)
}

func (f *fakeSecretsManager) name(id string) string {
	if f.nameFor != nil {
		return f.nameFor(id)
	}
	if strings.HasPrefix(id, "arn:") {
		resource := id[strings.Index(id, ":secret:")+len(":secret:"):]
		return resource[:len(resource)-7]
	}
	return id
}

func (f *fakeSecretsManager) value(id string) map[string]any {
	name := f.name(id)
	return map[string]any{"ARN": arnOf(name), "Name": name, "SecretString": secretValue, "VersionId": "v1", "VersionStages": []string{"AWSCURRENT"}, "CreatedDate": 1.7e9}
}

func (f *fakeSecretsManager) respond(target string, body map[string]any) (int, any) {
	if f.answer != nil {
		return f.answer(target, body)
	}
	switch target {
	case "secretsmanager.GetSecretValue":
		return 200, f.value(body["SecretId"].(string))
	case "secretsmanager.BatchGetSecretValue":
		values, failures := []any{}, []any{}
		for _, id := range body["SecretIdList"].([]any) {
			if failure, failed := f.batchErrors[id.(string)]; failed {
				failures = append(failures, failure)
				continue
			}
			values = append(values, f.value(id.(string)))
		}
		page := map[string]any{"SecretValues": values, "Errors": failures}
		if f.batchPage > 0 {
			start := 0
			if token, _ := body["NextToken"].(string); token != "" {
				fmt.Sscan(token, &start)
			}
			end := min(start+f.batchPage, len(values))
			page["SecretValues"] = values[start:end]
			if end < len(values) {
				page["NextToken"] = fmt.Sprint(end)
			}
		}
		return 200, page
	case "secretsmanager.ListSecrets":
		if f.endlessList {
			return 200, map[string]any{"SecretList": []any{}, "NextToken": "more"}
		}
		var prefixes []string
		for _, filter := range body["Filters"].([]any) {
			for _, value := range filter.(map[string]any)["Values"].([]any) {
				prefixes = append(prefixes, value.(string))
			}
		}
		var matches []any
		for _, entry := range catalog {
			if slices.ContainsFunc(prefixes, func(prefix string) bool { return strings.HasPrefix(entry.name, prefix) }) {
				var tags []any
				for key, value := range entry.tags {
					tags = append(tags, map[string]any{"Key": key, "Value": value})
				}
				matches = append(matches, map[string]any{"ARN": arnOf(entry.name), "Name": entry.name, "Description": entry.description, "Tags": tags, "CreatedDate": entry.created})
			}
		}
		start := 0
		if token, _ := body["NextToken"].(string); token != "" {
			fmt.Sscan(token, &start)
		}
		size := f.pageSize
		if size == 0 {
			size = 2
		}
		end := min(start+size, len(matches))
		page := map[string]any{"SecretList": matches[start:end]}
		if end < len(matches) {
			page["NextToken"] = fmt.Sprint(end)
		}
		return 200, page
	}
	return 400, map[string]any{"__type": "UnknownOperationException"}
}

type secretsBroker struct {
	t       *testing.T
	aws     *fakeSecretsManager
	cli     *fakeAWS
	handler http.Handler
	lines   []string
	token   string

	otherToken string
}

func newSecretsBroker(t *testing.T) *secretsBroker {
	t.Helper()
	grants := Grants{filepath.Join(t.TempDir(), "grants")}
	if _, err := grants.Issue("api", []string{"build"}); err != nil {
		t.Fatal(err)
	}
	if err := grants.IssueSecretsManager("api", "us-east-1", map[string][]string{"build": {"app/config", "app/db"}, "vault": {"shared/key"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Issue("web", []string{"build"}); err != nil {
		t.Fatal(err)
	}
	b := &secretsBroker{t: t, aws: &fakeSecretsManager{}, cli: ok()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { b.aws.serve(w, r) }))
	t.Cleanup(server.Close)
	api := NewSecretsManager(NewCredentialSource(b.cli.run))
	api.Endpoint = func(region string) string { return server.URL + "/" + region }
	b.handler = SecretsManagerHandler(api, grants, func(line string) { b.lines = append(b.lines, line) })
	b.token, b.otherToken = tokenOf(t, grants, "api"), tokenOf(t, grants, "web")
	return b
}

func (b *secretsBroker) send(target string, body any, headers ...string) (int, map[string]any, string) {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest("POST", "/", strings.NewReader(string(encoded)))
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", "secretsmanager."+target)
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=stage-broker/20261106/us-east-1/secretsmanager/aws4_request, SignedHeaders=host, Signature=00")
	request.Header.Set(TokenHeader, b.token)
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			request.Header.Del(headers[i])
		} else {
			request.Header.Set(headers[i], headers[i+1])
		}
	}
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	var decoded map[string]any
	json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded, recorder.Body.String()
}

func (b *secretsBroker) expectRefused(code int, decoded map[string]any, fragment string) {
	b.t.Helper()
	message, _ := decoded["Message"].(string)
	if code != 400 || decoded["__type"] != "AccessDeniedException" || !strings.Contains(message, fragment) {
		b.t.Errorf("got %d %v, want 400 AccessDeniedException containing %q", code, decoded, fragment)
	}
}

func (b *secretsBroker) expectNoAWSCalls() {
	b.t.Helper()
	if len(b.aws.calls) != 0 {
		b.t.Errorf("AWS was called: %v", b.aws.calls)
	}
}

func (b *secretsBroker) profilesExported() []string {
	var profiles []string
	for _, argv := range b.cli.calls {
		profiles = append(profiles, argv[4])
	}
	return profiles
}

func names(entries any) []string {
	var found []string
	list, _ := entries.([]any)
	for _, entry := range list {
		found = append(found, entry.(map[string]any)["Name"].(string))
	}
	return found
}

func TestSecretsManagerCallsNeedTheSandboxsTokenInTheBrokersOwnHeader(t *testing.T) {
	b := newSecretsBroker(t)
	for name, headers := range map[string][]string{
		"no token":            {TokenHeader, ""},
		"wrong token":         {TokenHeader, "wrong"},
		"token as a bearer":   {TokenHeader, "", "Authorization", "Bearer " + b.token},
		"bearer-shaped token": {TokenHeader, "Bearer " + b.token},
	} {
		code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": "app/config"}, headers...)
		if code != 401 || decoded["__type"] != "UnrecognizedClientException" {
			t.Errorf("%s: %d %v", name, code, decoded)
		}
	}
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"SecretId":"app/config"}`))
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")
	request.Header.Add(TokenHeader, b.token)
	request.Header.Add(TokenHeader, b.token)
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	if recorder.Code != 401 {
		t.Errorf("two token headers: %d", recorder.Code)
	}
	b.expectNoAWSCalls()
}

func TestASandboxWithoutASecretsManagerGrantReadsNothing(t *testing.T) {
	b := newSecretsBroker(t)
	code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": "app/config"}, TokenHeader, b.otherToken)
	b.expectRefused(code, decoded, "app/config is not listed for this sandbox")
	b.expectNoAWSCalls()
}

func TestGetSecretValueReadsAListedSecretWithTheProfileItIsListedUnder(t *testing.T) {
	b := newSecretsBroker(t)
	code, decoded, raw := b.send("GetSecretValue", map[string]any{"SecretId": "shared/key", "VersionStage": "AWSCURRENT"})
	if code != 200 || decoded["SecretString"] != secretValue || decoded["Name"] != "shared/key" {
		t.Fatalf("%d %s", code, raw)
	}
	if !reflect.DeepEqual(b.profilesExported(), []string{"vault"}) {
		t.Errorf("profiles exported %v", b.profilesExported())
	}
	call := b.aws.calls[0]
	if call.target != "secretsmanager.GetSecretValue" || !reflect.DeepEqual(call.body, map[string]any{"SecretId": "shared/key", "VersionStage": "AWSCURRENT"}) {
		t.Errorf("AWS got %v", call)
	}
	if !strings.HasPrefix(call.authorization, "AWS4-HMAC-SHA256 Credential=ASIADEV/20261106/us-east-1/secretsmanager/aws4_request, SignedHeaders=content-type;host;x-amz-date;x-amz-security-token;x-amz-target, Signature=") || call.token != "devtoken" {
		t.Errorf("AWS got authorization %q, token %q; want the broker's own signature with the host profile's session", call.authorization, call.token)
	}
}

func TestGetSecretValueTakesTheFullARNOfAListedSecretInTheGrantedRegionOnly(t *testing.T) {
	b := newSecretsBroker(t)
	code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": arnOf("app/db")})
	if code != 200 || decoded["Name"] != "app/db" {
		t.Fatalf("%d %v", code, decoded)
	}
	b.aws.calls = nil
	for _, id := range []string{
		"arn:aws:secretsmanager:eu-west-1:111122223333:secret:app/db-AbC123",
		"arn:aws:secretsmanager:us-east-1:111122223333:secret:app/db",
		"arn:aws:secretsmanager:us-east-1:111122223333:secret:app/db-AbC12",
		"arn:aws:secretsmanager:us-east-1:111122223333:secret:app/dbx-AbC123",
		"arn:aws:s3:us-east-1:111122223333:secret:app/db-AbC123",
	} {
		code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": id})
		b.expectRefused(code, decoded, "is not listed for this sandbox")
	}
	b.expectNoAWSCalls()
}

func TestGetSecretValueRefusesAnUnlistedSecretWithoutAskingAWS(t *testing.T) {
	b := newSecretsBroker(t)
	for _, id := range []string{"other/secret", "app/con", "APP/CONFIG", "app/config-old", "", "app/config\n"} {
		code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": id})
		b.expectRefused(code, decoded, "is not listed for this sandbox")
	}
	b.expectNoAWSCalls()
	if len(b.cli.calls) != 0 {
		t.Errorf("host credentials were read: %v", b.cli.calls)
	}
}

func TestGetSecretValueDropsAnAnswerThatNamesAnotherSecret(t *testing.T) {
	b := newSecretsBroker(t)
	b.aws.nameFor = func(string) string { return "app/config-old" }
	code, decoded, raw := b.send("GetSecretValue", map[string]any{"SecretId": "app/config"})
	b.expectRefused(code, decoded, "secret app/config is not listed for this sandbox")
	if !strings.Contains(strings.Join(b.lines, "\n"), "dropped an answer for app/config-old") {
		t.Errorf("the host log does not say an answer was dropped: %v", b.lines)
	}
	if strings.Contains(raw, secretValue) {
		t.Fatalf("the other secret's value was returned: %s", raw)
	}
}

const awsDenial = "User: arn:aws:sts::111122223333:assumed-role/dev/person@example.com is not authorized to perform: secretsmanager:GetSecretValue"

func TestAWSErrorsComeBackAsTheirTypeWithAWSsMessageOnlyInTheHostLog(t *testing.T) {
	b := newSecretsBroker(t)
	b.aws.answer = func(string, map[string]any) (int, any) {
		return 400, map[string]any{"__type": "com.amazonaws.secretsmanager#AccessDeniedException:http://internal", "Message": awsDenial}
	}
	code, decoded, raw := b.send("GetSecretValue", map[string]any{"SecretId": "app/db"})
	if code != 400 || decoded["__type"] != "AccessDeniedException" || strings.Contains(raw, "person@example.com") || strings.Contains(raw, "arn:aws:sts") {
		t.Fatalf("%d %s", code, raw)
	}
	if !strings.Contains(decoded["Message"].(string), "Secrets Manager answered AccessDeniedException for app/db") {
		t.Errorf("message %v", decoded["Message"])
	}
	if !strings.Contains(strings.Join(b.lines, "\n"), awsDenial) {
		t.Errorf("the host log lacks AWS's message: %v", b.lines)
	}
	b.aws.answer = func(string, map[string]any) (int, any) {
		return 500, map[string]any{"__type": "not a type!", "message": "x"}
	}
	code, decoded, _ = b.send("GetSecretValue", map[string]any{"SecretId": "app/db"})
	if code != 500 || decoded["__type"] != "InternalServiceError" {
		t.Errorf("an unreadable type: %d %v", code, decoded)
	}
	b.aws.answer = func(string, map[string]any) (int, any) {
		return 400, map[string]any{"__type": strings.Repeat("A", 200)}
	}
	if _, decoded, _ = b.send("GetSecretValue", map[string]any{"SecretId": "app/db"}); decoded["__type"] != "InternalServiceError" {
		t.Errorf("an overlong type: %v", decoded["__type"])
	}
}

func TestBatchErrorsFromAWSNameTheClientsIdAndKeepAWSsMessageOnTheHost(t *testing.T) {
	b := newSecretsBroker(t)
	b.aws.batchErrors = map[string]map[string]any{"app/db": {"SecretId": "app/db", "ErrorCode": "AccessDeniedException", "Message": awsDenial}}
	_, decoded, raw := b.send("BatchGetSecretValue", map[string]any{"SecretIdList": []string{"app/config", arnOf("app/db")}})
	if strings.Contains(raw, "person@example.com") {
		t.Fatalf("AWS's message reached the sandbox: %s", raw)
	}
	errors, _ := decoded["Errors"].([]any)
	if len(errors) != 1 || errors[0].(map[string]any)["SecretId"] != arnOf("app/db") || errors[0].(map[string]any)["ErrorCode"] != "AccessDeniedException" {
		t.Errorf("errors %v", errors)
	}
	if !strings.Contains(strings.Join(b.lines, "\n"), awsDenial) {
		t.Errorf("the host log lacks AWS's message: %v", b.lines)
	}
}

func TestAnARNOnlyNamesAListedSecretAndAWSIsAskedForTheListedName(t *testing.T) {
	b := newSecretsBroker(t)
	code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": arnOf("app/db")})
	if code != 200 || decoded["Name"] != "app/db" || b.aws.calls[0].body["SecretId"] != "app/db" {
		t.Fatalf("own ARN: %d %v; AWS asked %v", code, decoded, b.aws.calls[0].body)
	}
	for _, id := range []string{
		"arn:aws:secretsmanager:us-east-1:999999999999:secret:app/db-AbC123",
		"arn:aws-cn:secretsmanager:us-east-1:111122223333:secret:app/db-AbC123",
		"arn:aws:secretsmanager:us-east-1:111122223333:secret:app/db-ZZZZZZ",
		"arn:aws:secretsmanager:us-east-1:111122223333:secret:app/db-backup",
	} {
		b.aws.calls = nil
		code, decoded, raw := b.send("GetSecretValue", map[string]any{"SecretId": id})
		b.expectRefused(code, decoded, "is not listed for this sandbox")
		if strings.Contains(raw, secretValue) {
			t.Errorf("%s: a value came back: %s", id, raw)
		}
		for _, call := range b.aws.calls {
			if call.body["SecretId"] != "app/db" {
				t.Errorf("%s: AWS was asked for %v", id, call.body["SecretId"])
			}
		}
	}
	b.aws.calls = nil
	_, decoded, raw := b.send("BatchGetSecretValue", map[string]any{"SecretIdList": []string{"arn:aws:secretsmanager:us-east-1:999999999999:secret:app/db-AbC123"}})
	if names(decoded["SecretValues"]) != nil || strings.Contains(raw, secretValue) {
		t.Errorf("batch with another account's ARN: %s", raw)
	}
	for _, call := range b.aws.calls {
		if !reflect.DeepEqual(call.body["SecretIdList"], []any{"app/db"}) {
			t.Errorf("AWS was asked for %v", call.body["SecretIdList"])
		}
	}
}

func TestTheBrokerDoesNotFollowRedirectsFromSecretsManager(t *testing.T) {
	b := newSecretsBroker(t)
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = append(elsewhere, r.Header.Get("X-Amz-Security-Token"))
	}))
	defer other.Close()
	b.aws.redirect = other.URL + "/"
	code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": "app/db"})
	if code != 503 || decoded["__type"] != "ServiceUnavailableException" || len(elsewhere) != 0 {
		t.Errorf("%d %v; the redirect target saw %v", code, decoded, elsewhere)
	}
}

func TestBatchGetSecretValueFollowsAWSsPages(t *testing.T) {
	b := newSecretsBroker(t)
	b.aws.batchPage = 1
	_, decoded, raw := b.send("BatchGetSecretValue", map[string]any{"SecretIdList": []string{"app/config", "app/db"}})
	if got := names(decoded["SecretValues"]); !reflect.DeepEqual(got, []string{"app/config", "app/db"}) {
		t.Errorf("values %v (%s)", got, raw)
	}
	if errors, _ := decoded["Errors"].([]any); len(errors) != 0 {
		t.Errorf("errors %v", errors)
	}
}

func TestListSecretsStopsAfterAHundredPages(t *testing.T) {
	b := newSecretsBroker(t)
	b.aws.endlessList = true
	code, decoded, _ := b.send("ListSecrets", map[string]any{})
	if code != 502 || len(b.aws.calls) > 100 {
		t.Errorf("%d %v after %d calls", code, decoded, len(b.aws.calls))
	}
}

type slowCredentials struct{ release chan struct{} }

func (s slowCredentials) CredentialProcess(profile string, granted map[string]bool) (json.RawMessage, error) {
	if profile == "build" {
		<-s.release
	}
	return json.RawMessage(credentials), nil
}

func TestASlowHostLoginForOneProfileDoesNotHoldUpAnother(t *testing.T) {
	slow := slowCredentials{make(chan struct{})}
	defer close(slow.release)
	api := NewSecretsManager(slow)
	go api.key(context.Background(), "build")
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() { api.key(context.Background(), "vault"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("vault waited for build's login")
	}
}

func TestTheBrokerSaysItServesSecretsManagerAndOtherListenersDoNot(t *testing.T) {
	b := newSecretsBroker(t)
	serving := httptest.NewServer(b.handler)
	defer serving.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer other.Close()
	if !ServesSecretsManager(serving.URL + "/") {
		t.Error("the broker's own handler was not recognised")
	}
	if ServesSecretsManager(other.URL+"/") || ServesSecretsManager("http://127.0.0.1:1/") {
		t.Error("another listener passed as the broker")
	}
}

func TestBatchGetSecretValueReportsUnlistedSecretsInErrorsWithoutAskingAWSForThem(t *testing.T) {
	b := newSecretsBroker(t)
	code, decoded, raw := b.send("BatchGetSecretValue", map[string]any{"SecretIdList": []string{"app/config", "other/secret", "shared/key", arnOf("app/db")}})
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if got := names(decoded["SecretValues"]); !reflect.DeepEqual(got, []string{"app/config", "shared/key", "app/db"}) {
		t.Errorf("values for %v, want them in the order asked for", got)
	}
	errors, _ := decoded["Errors"].([]any)
	if len(errors) != 1 {
		t.Fatalf("errors %v", errors)
	}
	entry := errors[0].(map[string]any)
	if entry["SecretId"] != "other/secret" || entry["ErrorCode"] != "AccessDeniedException" || !strings.Contains(entry["Message"].(string), "not listed for this sandbox") {
		t.Errorf("error entry %v", entry)
	}
	var asked []any
	for _, call := range b.aws.calls {
		asked = append(asked, call.body["SecretIdList"].([]any)...)
	}
	if slices.Contains(asked, any("other/secret")) || len(asked) != 3 {
		t.Errorf("AWS was asked for %v", asked)
	}
	if got := b.profilesExported(); !reflect.DeepEqual(got, []string{"build", "vault"}) {
		t.Errorf("profiles exported %v", got)
	}
}

func TestBatchGetSecretValueDropsAValueThatNamesAnUnlistedSecret(t *testing.T) {
	b := newSecretsBroker(t)
	b.aws.nameFor = func(id string) string {
		if id == "app/db" {
			return "other/secret"
		}
		return id
	}
	_, decoded, raw := b.send("BatchGetSecretValue", map[string]any{"SecretIdList": []string{"app/config", "app/db"}})
	if got := names(decoded["SecretValues"]); !reflect.DeepEqual(got, []string{"app/config"}) {
		t.Errorf("values for %v (%s)", got, raw)
	}
	if strings.Count(raw, secretValue) != 1 {
		t.Errorf("response %s", raw)
	}
	errors, _ := decoded["Errors"].([]any)
	if len(errors) != 1 || errors[0].(map[string]any)["SecretId"] != "app/db" {
		t.Errorf("errors %v", errors)
	}
}

func TestBatchGetSecretValueTakesOnlyAListOfUpToTwentySecretIds(t *testing.T) {
	b := newSecretsBroker(t)
	many := make([]string, 21)
	for i := range many {
		many[i] = "app/config"
	}
	for fragment, body := range map[string]map[string]any{
		"Filters":                    {"Filters": []any{map[string]any{"Key": "name", "Values": []string{"app/"}}}},
		"MaxResults":                 {"SecretIdList": []string{"app/config"}, "MaxResults": 5},
		"NextToken":                  {"SecretIdList": []string{"app/config"}, "NextToken": "x"},
		"SecretIdList is required":   {},
		"at most 20 secret ids":      {"SecretIdList": many},
		"unknown field \"Surprise\"": {"SecretIdList": []string{"app/config"}, "Surprise": true},
	} {
		code, decoded, _ := b.send("BatchGetSecretValue", body)
		b.expectRefused(code, decoded, fragment)
	}
	b.expectNoAWSCalls()
}

func TestListSecretsListsOnlyListedSecretsInOnePage(t *testing.T) {
	b := newSecretsBroker(t)
	code, decoded, raw := b.send("ListSecrets", map[string]any{"MaxResults": 1})
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if got := names(decoded["SecretList"]); !reflect.DeepEqual(got, []string{"app/db", "app/config", "shared/key"}) {
		t.Errorf("listed %v, want the listed secrets by creation date", got)
	}
	if _, more := decoded["NextToken"]; more || strings.Contains(raw, secretValue) {
		t.Errorf("response %s", raw)
	}
	for _, call := range b.aws.calls {
		filters := call.body["Filters"].([]any)
		values := filters[0].(map[string]any)["Values"].([]any)
		if len(filters) != 1 || filters[0].(map[string]any)["Key"] != "name" {
			t.Errorf("AWS filters %v", filters)
		}
		for _, value := range values {
			if !slices.Contains([]any{"app/config", "app/db", "shared/key"}, value) {
				t.Errorf("AWS was asked about %v", value)
			}
		}
	}
	if len(b.aws.calls) < 3 {
		t.Errorf("the broker did not follow AWS's pages: %d calls", len(b.aws.calls))
	}
}

func TestListSecretsAppliesTheClientsFiltersOnTheHostToTheListedSecretsAlone(t *testing.T) {
	filter := func(pairs ...[]any) map[string]any {
		var filters []any
		for _, pair := range pairs {
			filters = append(filters, map[string]any{"Key": pair[0], "Values": pair[1:]})
		}
		return map[string]any{"Filters": filters, "SortBy": "name"}
	}
	for name, test := range map[string]struct {
		body map[string]any
		want []string
	}{
		"name prefix":                   {filter([]any{"name", "app/"}), []string{"app/config", "app/db"}},
		"name prefix is case-sensitive": {filter([]any{"name", "App/"}), nil},
		"negated name":                  {filter([]any{"name", "!app/"}), []string{"shared/key"}},
		"values in a filter are any":    {filter([]any{"name", "app/c", "shared/"}), []string{"app/config", "shared/key"}},
		"filters are all":               {filter([]any{"name", "app/"}, []any{"tag-value", "data"}), []string{"app/db"}},
		"unlisted names stay out":       {filter([]any{"name", "other/", "app/config-"}), nil},
		"description ignores case":      {filter([]any{"description", "main"}), []string{"app/config"}},
		"tag key":                       {filter([]any{"tag-key", "own"}), []string{"shared/key"}},
		"sorted by name descending":     {map[string]any{"SortBy": "name", "SortOrder": "desc"}, []string{"shared/key", "app/db", "app/config"}},
	} {
		b := newSecretsBroker(t)
		code, decoded, raw := b.send("ListSecrets", test.body)
		if got := names(decoded["SecretList"]); code != 200 || !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: %d %v, want %v (%s)", name, code, got, test.want, raw)
		}
		for _, call := range b.aws.calls {
			for _, entry := range call.body["Filters"].([]any) {
				if entry.(map[string]any)["Key"] != "name" {
					t.Errorf("%s: a client filter reached AWS: %v", name, call.body)
				}
			}
		}
	}
}

func TestListSecretsRefusesFilterKeysItDoesNotApplyAndAnyNextToken(t *testing.T) {
	b := newSecretsBroker(t)
	for fragment, body := range map[string]map[string]any{
		"Filters key all is not applied by the stage broker":            {"Filters": []any{map[string]any{"Key": "all", "Values": []string{"app"}}}},
		"Filters key primary-region is not applied by the stage broker": {"Filters": []any{map[string]any{"Key": "primary-region", "Values": []string{"us"}}}},
		"returns every listed secret in one page":                       {"NextToken": "2"},
		"SortBy owner":                 {"SortBy": "owner"},
		"a Filters entry needs Values": {"Filters": []any{map[string]any{"Key": "name"}}},
		"unknown field \"Surprise\"":   {"Surprise": 1},
	} {
		code, decoded, _ := b.send("ListSecrets", body)
		b.expectRefused(code, decoded, fragment)
	}
	b.expectNoAWSCalls()
}

func TestOnlyTheThreeReadOperationsAreServed(t *testing.T) {
	b := newSecretsBroker(t)
	for _, target := range []string{"PutSecretValue", "DeleteSecret", "DescribeSecret", "GetRandomPassword", "getsecretvalue"} {
		code, decoded, _ := b.send(target, map[string]any{"SecretId": "app/config"})
		b.expectRefused(code, decoded, target+" is not served")
	}
	code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": "app/config"}, "X-Amz-Target", "kms.GetSecretValue")
	b.expectRefused(code, decoded, "kms.GetSecretValue is not served")
	code, decoded, _ = b.send("GetSecretValue", map[string]any{"SecretId": "app/config"}, "Content-Type", "application/json")
	b.expectRefused(code, decoded, "application/x-amz-json-1.1")
	code, decoded, _ = b.send("GetSecretValue", map[string]any{"SecretId": "app/config", "Surprise": 1})
	b.expectRefused(code, decoded, `unknown field "Surprise"`)
	code, decoded, _ = b.send("GetSecretValue", map[string]any{"SecretId": strings.Repeat("a", 70000)})
	b.expectRefused(code, decoded, "larger than")
	for _, target := range []string{"/v1/anything?Action=PutSecretValue", "/?Action=PutSecretValue", "/other"} {
		request := httptest.NewRequest("POST", target, strings.NewReader(`{"SecretId":"app/config"}`))
		request.Header.Set("Content-Type", "application/x-amz-json-1.1")
		request.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")
		request.Header.Set(TokenHeader, b.token)
		recorder := httptest.NewRecorder()
		b.handler.ServeHTTP(recorder, request)
		var refused map[string]any
		json.Unmarshal(recorder.Body.Bytes(), &refused)
		b.expectRefused(recorder.Code, refused, "only POST / is served")
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set(TokenHeader, b.token)
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	if recorder.Code != 405 {
		t.Errorf("GET: %d", recorder.Code)
	}
	b.expectNoAWSCalls()
}

func TestHostCredentialsAreReadOncePerProfileUntilTheyNearExpiry(t *testing.T) {
	b := newSecretsBroker(t)
	b.send("GetSecretValue", map[string]any{"SecretId": "app/config"})
	b.send("GetSecretValue", map[string]any{"SecretId": "app/db"})
	if got := b.profilesExported(); !reflect.DeepEqual(got, []string{"build"}) {
		t.Errorf("profiles exported %v", got)
	}
}

func TestAnExpiredHostLoginIsAServiceErrorNamingTheProfile(t *testing.T) {
	b := newSecretsBroker(t)
	b.cli.result = Result{ExitCode: 255, Stderr: "Error when retrieving token from sso: Token has expired\n"}
	code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": "app/config"})
	message, _ := decoded["Message"].(string)
	if code != 503 || decoded["__type"] != "ServiceUnavailableException" || !strings.Contains(message, "cannot get AWS credentials for profile build on the host") || strings.Contains(message, "Token has expired") {
		t.Errorf("%d %v", code, decoded)
	}
	if !strings.Contains(strings.Join(b.lines, "\n"), "build: Error when retrieving token from sso: Token has expired; refresh the profile's login on the host") {
		t.Errorf("the host log lacks the reason: %v", b.lines)
	}
	b.expectNoAWSCalls()
}

func TestTheBrokersLogNamesTheSandboxOperationAndSecretsButNoValue(t *testing.T) {
	b := newSecretsBroker(t)
	b.send("GetSecretValue", map[string]any{"SecretId": "app/config"})
	b.send("BatchGetSecretValue", map[string]any{"SecretIdList": []string{"app/db", "other/secret"}})
	b.send("ListSecrets", map[string]any{})
	b.send("GetSecretValue", map[string]any{"SecretId": "other/secret\x1b[31m"})
	want := []string{
		"broker: api secretsmanager GetSecretValue app/config 200",
		"broker: api secretsmanager BatchGetSecretValue app/db 200",
		"broker: api secretsmanager ListSecrets - 200",
		"broker: api secretsmanager GetSecretValue - 400",
	}
	if !reflect.DeepEqual(b.lines, want) {
		t.Errorf("log\n%s\nwant\n%s", strings.Join(b.lines, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(strings.Join(b.lines, "\n"), secretValue) {
		t.Error("a value reached the log")
	}
}

func TestASecretsManagerGrantRecordsTheRegionAndEachSecretsProfile(t *testing.T) {
	grants := Grants{filepath.Join(t.TempDir(), "grants")}
	token, err := grants.Issue("api", nil)
	if err != nil {
		t.Fatal(err)
	}
	supplied := strings.TrimSpace(readFile(t, token))
	if err := grants.IssueSecretsManager("api", "eu-west-1", map[string][]string{"build": {"app/db"}, "vault": {"shared/key"}}); err != nil {
		t.Fatal(err)
	}
	owner, grant, found := grants.LookupSecretsManager(supplied)
	want := SecretsManagerGrant{Region: "eu-west-1", Profiles: map[string]string{"app/db": "build", "shared/key": "vault"}}
	if !found || owner != "api" || !reflect.DeepEqual(grant, want) {
		t.Fatalf("lookup %q %v %v", owner, grant, found)
	}
	info, _ := os.Stat(filepath.Join(grants.Path, "api.secrets"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if err := grants.IssueSecretsManager("api", "eu-west-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, grant, _ := grants.LookupSecretsManager(supplied); len(grant.Profiles) != 0 {
		t.Errorf("reissuing without secrets kept %v", grant)
	}
	grants.IssueSecretsManager("api", "eu-west-1", map[string][]string{"build": {"app/db"}})
	grants.Revoke("api")
	if _, err := os.Stat(filepath.Join(grants.Path, "api.secrets")); !os.IsNotExist(err) {
		t.Errorf("revoke left the secrets grant: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

type failingLogin struct{ exports *atomic.Int32 }

func (f failingLogin) CredentialProcess(profile string, granted map[string]bool) (json.RawMessage, error) {
	f.exports.Add(1)
	time.Sleep(100 * time.Millisecond)
	return nil, &Error{http.StatusServiceUnavailable, profile + ": Token has expired; refresh the profile's login on the host"}
}

func TestCallsWaitingOnAFailedLoginShareItsFailure(t *testing.T) {
	var exports atomic.Int32
	api := NewSecretsManager(failingLogin{&exports})
	var wait sync.WaitGroup
	for range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := api.key(context.Background(), "build"); err == nil {
				t.Error("a failed login gave a key")
			}
		}()
	}
	wait.Wait()
	if got := exports.Load(); got != 1 {
		t.Errorf("%d exports for one failing login", got)
	}
}

func TestACallStopsWaitingForAHostLoginWhenItsRequestEnds(t *testing.T) {
	slow := slowCredentials{make(chan struct{})}
	defer close(slow.release)
	api := NewSecretsManager(slow)
	go api.key(context.Background(), "build")
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := api.key(ctx, "build"); err == nil || time.Since(started) > time.Second {
		t.Errorf("waited %v: %v", time.Since(started), err)
	}
}

func TestAnOverlongARNIsRefusedWithoutAskingAWSAndIsNotLoggedInFull(t *testing.T) {
	b := newSecretsBroker(t)
	long := "arn:" + strings.Repeat("a", 60000) + ":secretsmanager:us-east-1:111122223333:secret:app/db-AbC123"
	code, decoded, _ := b.send("GetSecretValue", map[string]any{"SecretId": long})
	b.expectRefused(code, decoded, "is not listed for this sandbox")
	b.expectNoAWSCalls()
	if message, _ := decoded["Message"].(string); len(message) > 300 {
		t.Errorf("the refusal repeats %d bytes of the id", len(message))
	}
	wrong := "arn:aws-us-gov:secretsmanager:us-east-1:999999999999:secret:app/db-AbC123"
	b.send("GetSecretValue", map[string]any{"SecretId": wrong})
	for _, line := range b.lines {
		if len(line) > 300 || strings.Contains(line, wrong) {
			t.Errorf("a client id reached the log beyond a short prefix: %q", line)
		}
	}
}

func TestTheOldBrokerCheckDoesNotFollowARedirectToTheBroker(t *testing.T) {
	b := newSecretsBroker(t)
	serving := httptest.NewServer(b.handler)
	defer serving.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, serving.URL+"/", http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	if ServesSecretsManager(redirecting.URL + "/") {
		t.Error("a redirect to the broker passed as the broker")
	}
}
