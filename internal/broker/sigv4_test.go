package broker

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRequestsToAWSAreSignedAsInAWSsSignatureVersion4TestSuite(t *testing.T) {
	at := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	example := accessKey{ID: "AKIDEXAMPLE", Secret: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}
	withToken := example
	withToken.Token = "6e86291e8372ff2a2260956d9b8aae1d763fbf315fa00fa31553b73ebf194267"
	form := "Param1=value1"
	for name, vector := range map[string]struct {
		method, body string
		headers      map[string]string
		key          accessKey
		want         string
	}{
		"get-vanilla": {"GET", "", nil, example,
			"SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		"post-vanilla": {"POST", "", nil, example,
			"SignedHeaders=host;x-amz-date, Signature=5da7c1a2acd57cee7505fc6676e4e544621c30862966e37dddb68e92efbe5d6b"},
		"get-vanilla-with-session-token": {"GET", "", nil, withToken,
			"SignedHeaders=host;x-amz-date;x-amz-security-token, Signature=07ec1639c89043aa0e3e2de82b96708f198cceab042d4a97044c66dd9f74e7f8"},
		"post-x-www-form-urlencoded": {"POST", form, map[string]string{
			"Content-Type":         "application/x-www-form-urlencoded",
			"Content-Length":       "13",
			"X-Amz-Content-Sha256": "9095672bbd1f56dfc5b65f3e153adc8731a4a654192329106275f4c7b24d0b6e",
		}, example,
			"SignedHeaders=content-length;content-type;host;x-amz-content-sha256;x-amz-date, Signature=d3875051da38690788ef43de4db0d8f280229d82040bfac253562e56c3f20e0b"},
	} {
		request, err := http.NewRequest(vector.method, "https://example.amazonaws.com/", strings.NewReader(vector.body))
		if err != nil {
			t.Fatal(err)
		}
		for header, value := range vector.headers {
			request.Header.Set(header, value)
		}
		signRequest(request, []byte(vector.body), vector.key, "us-east-1", "service", at)
		want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " + vector.want
		if got := request.Header.Get("Authorization"); got != want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, want)
		}
		if got := request.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
			t.Errorf("%s: X-Amz-Date %q", name, got)
		}
		if got := request.Header.Get("X-Amz-Security-Token"); got != vector.key.Token {
			t.Errorf("%s: X-Amz-Security-Token %q", name, got)
		}
	}
}

func TestASecretsManagerRequestIsSignedAsBotocoreSignsIt(t *testing.T) {
	body := `{"SecretId": "app/db"}`
	request, err := http.NewRequest("POST", "http://127.0.0.1:9/", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")
	key := accessKey{ID: "AKIDEXAMPLE", Secret: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Token: "session-token-example"}
	signRequest(request, []byte(body), key, "us-east-1", "secretsmanager", time.Date(2026, 11, 26, 20, 47, 20, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261126/us-east-1/secretsmanager/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-date;x-amz-security-token;x-amz-target, " +
		"Signature=3837c570b2fda3a93681bb849dce05057a246115a124ca83e75fad2e48792739"
	if got := request.Header.Get("Authorization"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
