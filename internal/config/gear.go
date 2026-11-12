package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Gear struct {
	From     string
	To       string
	HostPath string
}

const (
	gearOwnerReason = "gear copies files from your machine into the sandbox, where the agent can read them, so only ~/.scoring-stage/config.toml may list it"
	gearPathRule    = "a path that starts with / or ~/ and has no .., doubled or trailing slash"
	sandboxUserHome = "/home/agent/"
)

var credentialLocationsUnderHome = []string{
	".ssh", ".aws", ".azure", ".gnupg", ".kube", ".netrc", ".npmrc", ".pypirc", ".git-credentials",
	".config/git/credentials", ".config/gh", ".config/gcloud", ".config/sbx", ".docker/config.json",
	".claude/.credentials.json", ".codex/auth.json",
}

func refuseGear(data map[string]any, who string) error {
	if _, present := data["gear"]; present {
		return errorf("%s cannot list gear; %s", who, gearOwnerReason)
	}
	return nil
}

func decodeGear(data map[string]any, where, home string, agents Agents) ([]Gear, error) {
	entries, err := optionalList(data, "gear", where)
	if err != nil {
		return nil, err
	}
	var gear []Gear
	destinations := map[string]string{}
	for _, entry := range entries {
		item, err := decodeGearEntry(entry)
		if err != nil {
			return nil, errorf("%s: gear: %v", where, err)
		}
		if other, taken := destinations[item.To]; taken {
			return nil, errorf("%s: gear: %s and %s are both copied to %s", where, other, item.From, item.To)
		}
		destinations[item.To] = item.From
		item.HostPath = Resolve(ExpandUser(item.From))
		if reason := gearRefusal(item.HostPath, home, agents); reason != "" {
			return nil, errorf("%s: gear: %s %s", where, item.From, reason)
		}
		gear = append(gear, item)
	}
	return gear, nil
}

func decodeGearEntry(entry any) (Gear, error) {
	switch typed := entry.(type) {
	case string:
		if !strings.HasPrefix(typed, "~/") {
			return Gear{}, errorf("%s must start with ~/, the same path under your home and the sandbox user's; write { from = \"...\", to = \"...\" } to copy from or to anywhere else", Printable(typed))
		}
		if !isTakePath(typed) {
			return Gear{}, errorf("%s must be %s", Printable(typed), gearPathRule)
		}
		return Gear{From: typed, To: typed}, nil
	case map[string]any:
		from, fromOK := typed["from"].(string)
		to, toOK := typed["to"].(string)
		if !fromOK || !toOK || len(unknownKeys(typed, []string{"from", "to"})) > 0 {
			break
		}
		if !isTakePath(from) {
			return Gear{}, errorf("from %s must be %s on your machine", Printable(from), gearPathRule)
		}
		if !isTakePath(to) {
			return Gear{}, errorf("to %s must be %s in the sandbox", Printable(to), gearPathRule)
		}
		return Gear{From: from, To: to}, nil
	}
	return Gear{}, errorf("a gear entry is a ~/ path or { from, to }, not %s", Printable(fmt.Sprint(entry)))
}

func gearRefusal(resolved, home string, agents Agents) string {
	if overlaps(resolved, home) {
		return "is, is inside or holds stage's own directory " + home + ", which keeps tokens, saved logins and broker grants; stage never copies those into a sandbox"
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	locations := append([]string{}, credentialLocationsUnderHome...)
	for _, name := range agents.Names() {
		for _, file := range agents.LoginFiles(name) {
			if relative, inHome := strings.CutPrefix(file.Path, sandboxUserHome); inHome {
				locations = append(locations, relative)
			}
		}
	}
	for _, location := range locations {
		if overlaps(resolved, Resolve(filepath.Join(user, filepath.FromSlash(location)))) {
			return "is, is inside or holds ~/" + location + ", where a credential or login is kept; credentials stay on the host, behind secrets and bindings. List the files or directories beside it that you mean"
		}
	}
	return ""
}

func overlaps(one, other string) bool {
	return within(one, other) || within(other, one)
}

func within(path, dir string) bool {
	path, dir = strings.ToLower(path), strings.ToLower(dir)
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

func GearSummary(gear []Gear) string {
	entries := make([]string, len(gear))
	for i, item := range gear {
		entries[i] = item.To
		if item.From != item.To {
			entries[i] = item.From + " -> " + item.To
		}
	}
	return Printable(strings.Join(entries, ", "))
}
