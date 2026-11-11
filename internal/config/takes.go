package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"
)

func decodeTakes(data map[string]any, where string) ([]string, error) {
	takes, err := optionalStrings(data, "takes", where)
	if err != nil {
		return nil, err
	}
	for _, take := range takes {
		if !isTakePath(take) {
			return nil, errorf("%s: takes: %s must be a path in the sandbox that starts with / or ~/ and has no .., doubled or trailing slash", where, Printable(take))
		}
	}
	return takes, nil
}

func isTakePath(take string) bool {
	rooted := take
	if strings.HasPrefix(take, "~/") {
		rooted = take[1:]
	}
	return strings.HasPrefix(rooted, "/") && rooted != "/" && path.Clean(rooted) == rooted && !HasControl(take)
}

func takeDetails(takes []string) []string {
	details := make([]string, len(takes))
	for i, take := range takes {
		details[i] = Printable("copies " + take + " from the sandbox to your machine before the sandbox is removed")
	}
	return details
}

func takesFingerprint(repository string, takes []string) string {
	identity, _ := json.Marshal(struct {
		Repository string
		Takes      []string
	}{strings.ToLower(repository), takes})
	sum := sha256.Sum256(append([]byte("takes\x00"), identity...))
	return hex.EncodeToString(sum[:])
}
