package broker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

type accessKey struct{ ID, Secret, Token string }

func signRequest(request *http.Request, body []byte, key accessKey, region, service string, at time.Time) {
	stamp := at.UTC().Format("20060102T150405Z")
	day := stamp[:8]
	request.Header.Set("X-Amz-Date", stamp)
	if key.Token != "" {
		request.Header.Set("X-Amz-Security-Token", key.Token)
	}
	host := request.Host
	if host == "" {
		host = request.URL.Host
	}
	signed := map[string]string{"host": host}
	for name, values := range request.Header {
		signed[strings.ToLower(name)] = strings.Join(values, ",")
	}
	names := make([]string, 0, len(signed))
	for name := range signed {
		names = append(names, name)
	}
	sort.Strings(names)
	var headers strings.Builder
	for _, name := range names {
		headers.WriteString(name + ":" + strings.TrimSpace(signed[name]) + "\n")
	}
	path := request.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	signedNames := strings.Join(names, ";")
	canonical := strings.Join([]string{request.Method, path, request.URL.RawQuery, headers.String(), signedNames, hexSHA256(body)}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hexSHA256([]byte(canonical))
	signingKey := []byte("AWS4" + key.Secret)
	for _, part := range []string{day, region, service, "aws4_request"} {
		signingKey = hmacSHA256(signingKey, part)
	}
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+key.ID+"/"+scope+", SignedHeaders="+signedNames+", Signature="+hex.EncodeToString(hmacSHA256(signingKey, toSign)))
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}
