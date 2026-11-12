package config

import (
	"fmt"
	"io/fs"
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
	".ssh", ".aws", ".azure", ".gnupg", ".kube", ".netrc", ".npmrc", ".pypirc", ".git-credentials", ".vault-token",
	".pgpass", ".my.cnf", ".password-store", ".terraform.d", ".docker/config.json", ".cargo/credentials",
	".cargo/credentials.toml", ".gem/credentials", ".m2/settings.xml", ".claude/.credentials.json", ".codex/auth.json",
	".config/git/credentials", ".config/gh", ".config/github-copilot", ".config/gcloud", ".config/sbx",
	".config/rclone", ".config/op", ".config/containers/auth.json", ".local/share/keyrings", "Library/Keychains",
	".bash_history", ".zsh_history", ".local/share/fish/fish_history",
}

var credentialLocationVariables = []string{"AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE", "GNUPGHOME", "HISTFILE"}

const linksCheckedPerCredentialLocation = 100_000

type credentialLocation struct {
	label string
	path  string
}

func refuseGear(data map[string]any, who string) error {
	if _, present := data["gear"]; present {
		return errorf("%s cannot list gear; %s", who, gearOwnerReason)
	}
	return nil
}

func decodeGear(data map[string]any, where string, agents Agents) ([]Gear, error) {
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
		if err := refuseLoginDestination(item.To, agents); err != nil {
			return nil, errorf("%s: gear: %v", where, err)
		}
		gear = append(gear, item)
	}
	return gear, nil
}

func (s *Settings) CheckedGear() ([]Gear, error) {
	if len(s.Gear) == 0 {
		return nil, nil
	}
	where := filepath.Join(s.Home, "config.toml")
	if err := s.Managed.CheckGear(); err != nil {
		return nil, errorf("%s: %v", where, err)
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return nil, errorf("%s: gear: stage cannot find your home directory (%v), so it cannot tell your gear from the places credentials are kept", where, err)
	}
	locations, err := credentialLocations(user, s.Agents)
	if err != nil {
		return nil, errorf("%s: gear: %v", where, err)
	}
	checked := make([]Gear, len(s.Gear))
	for i, item := range s.Gear {
		item.HostPath = Resolve(ExpandUser(item.From))
		if overlaps(item.HostPath, s.Home) {
			return nil, errorf("%s: gear: %s is, is inside or holds stage's own directory %s, which keeps tokens, saved logins and broker grants; stage never copies those into a sandbox",
				where, item.From, s.Home)
		}
		for _, location := range locations {
			if overlaps(item.HostPath, location.path) {
				return nil, errorf("%s: gear: %s is, is inside or holds %s, where a credential or login is kept; credentials stay on the host, behind secrets and bindings. List the files or directories beside it that you mean",
					where, item.From, Printable(location.label))
			}
		}
		checked[i] = item
	}
	return checked, nil
}

func credentialLocations(user string, agents Agents) ([]credentialLocation, error) {
	var named []credentialLocation
	add := func(label, path string) {
		named = append(named, credentialLocation{label: label, path: Resolve(path)})
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	for _, relative := range credentialLocationsUnderHome {
		add("~/"+relative, filepath.Join(user, filepath.FromSlash(relative)))
		if underConfig, moved := strings.CutPrefix(relative, ".config/"); moved && configHome != "" {
			add("$XDG_CONFIG_HOME/"+underConfig, filepath.Join(configHome, filepath.FromSlash(underConfig)))
		}
	}
	for _, variable := range credentialLocationVariables {
		if value := os.Getenv(variable); value != "" {
			add("$"+variable, value)
		}
	}
	if value := os.Getenv("DOCKER_CONFIG"); value != "" {
		add("$DOCKER_CONFIG/config.json", filepath.Join(value, "config.json"))
	}
	for _, file := range filepath.SplitList(os.Getenv("KUBECONFIG")) {
		if file != "" {
			add("$KUBECONFIG", file)
		}
	}
	for _, name := range agents.Names() {
		for _, file := range agents.LoginFiles(name) {
			if relative, inHome := strings.CutPrefix(file.Path, sandboxUserHome); inHome {
				add("~/"+relative, filepath.Join(user, filepath.FromSlash(relative)))
			}
		}
	}
	locations := named
	for _, location := range named {
		linked, err := linkedFrom(location)
		if err != nil {
			return nil, err
		}
		locations = append(locations, linked...)
	}
	return locations, nil
}

func linkedFrom(location credentialLocation) ([]credentialLocation, error) {
	if info, err := os.Stat(location.path); err != nil || !info.IsDir() {
		return nil, nil
	}
	var linked []credentialLocation
	seen := 0
	err := filepath.WalkDir(location.path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errorf("cannot read %s, so stage cannot tell what %s links to: %v", path, location.label, err)
		}
		if seen++; seen > linksCheckedPerCredentialLocation {
			return errorf("%s holds more than %d files and directories, so stage cannot tell what it links to", location.label, linksCheckedPerCredentialLocation)
		}
		if entry.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := filepath.EvalSymlinks(path)
		if err != nil || within(target, location.path) {
			return nil
		}
		linked = append(linked, credentialLocation{label: target + ", which " + path + " links to", path: target})
		return nil
	})
	return linked, err
}

func sandboxPath(to string) string {
	if rest, inHome := strings.CutPrefix(to, "~/"); inHome {
		return sandboxUserHome + rest
	}
	return to
}

func GearLandsIn(item Gear, dir string) bool {
	return overlaps(sandboxPath(item.To), dir)
}

func refuseLoginDestination(to string, agents Agents) error {
	for _, name := range agents.Names() {
		for _, file := range agents.LoginFiles(name) {
			if overlaps(sandboxPath(to), file.Path) {
				return errorf("to %s is, is inside or holds %s, one of %s's login files, which stage copies and saves itself (copy_logins)", Printable(to), file.Path, name)
			}
		}
	}
	return nil
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
