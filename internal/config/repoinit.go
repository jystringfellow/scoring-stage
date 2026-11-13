package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func DraftRepoConfig(agents, books, kits []string) string {
	var out strings.Builder
	out.WriteString("# Written by stage init.\n")
	paths := make([]string, len(kits))
	for i, name := range kits {
		paths[i] = "./.scoring-stage/kits/" + name
	}
	for _, entry := range []struct {
		key    string
		values []string
	}{{"agents", agents}, {"books", books}, {"kits", paths}} {
		if len(entry.values) == 0 {
			continue
		}
		quoted := make([]string, len(entry.values))
		for i, value := range entry.values {
			quoted[i] = strconv.Quote(value)
		}
		fmt.Fprintf(&out, "%s = [%s]\n", entry.key, strings.Join(quoted, ", "))
	}
	return out.String()
}

func CheckScaffoldKitName(name string) error {
	if !bookName.MatchString(name) || name == "." || name == ".." {
		return errorf("kit name %q must match %s and be one path component", Printable(name), bookName)
	}
	return nil
}

func ScaffoldKit(name string) (relPath, content string) {
	return filepath.ToSlash(filepath.Join(".scoring-stage", "kits", name, name+".yaml")), fmt.Sprintf("# syntax=docker/sandbox-kit:3\nschemaVersion: \"3\"\nkind: mixin\ndisplayName: %s\ndescription: Grants nothing yet.\ncapabilities: []\n", strconv.Quote(name))
}

func (r *RepoConfig) MissingRequests(agents, books, kits []string) []string {
	var missing []string
	for _, name := range agents {
		found := false
		for _, entry := range r.agents {
			found = found || entry == name
		}
		if !found {
			missing = append(missing, "--agent "+name)
		}
	}
	for _, name := range books {
		if !contains(r.books, name) {
			missing = append(missing, "--book "+name)
		}
	}
	for _, name := range kits {
		found := false
		for _, entry := range r.kits {
			found = found || kitSource(entry) == "./.scoring-stage/kits/"+name
		}
		if !found {
			missing = append(missing, "--kit "+name)
		}
	}
	return missing
}
