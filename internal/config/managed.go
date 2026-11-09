package config

import (
	"os"
	"path"
	"runtime"
	"strings"
)

const (
	RepoKitsAsk  = "ask"
	RepoKitsDeny = "deny"
)

var managedKeys = []string{"allowed_agents", "denied_agents", "aws", "repo_kits", "copy_logins", "allow_global_policy"}
var managedAWSKeys = []string{"allowed_profiles", "denied_profiles"}

// ManagedPaths lists where an administrator's managed settings may live, most specific first.
var ManagedPaths = func() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Library/Application Support/scoring-stage/managed.toml"}
	case "linux":
		paths := []string{"/etc/scoring-stage/managed.toml"}
		if os.Getenv("WSL_DISTRO_NAME") != "" || fileExists("/proc/sys/fs/binfmt_misc/WSLInterop") {
			paths = append(paths, "/mnt/c/ProgramData/scoring-stage/managed.toml")
		}
		return paths
	}
	return nil
}

type Managed struct {
	Paths             []string
	allowedAgents     []string
	deniedAgents      []string
	allowedProfiles   [][]string
	deniedProfiles    []string
	repoKits          string
	copyLogins        []string
	copyLoginsSet     bool
	allowGlobalPolicy bool
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LoadManaged merges every managed file that exists; each can only narrow the others.
func LoadManaged() (*Managed, error) {
	managed := &Managed{repoKits: RepoKitsAsk, allowGlobalPolicy: true}
	for _, file := range ManagedPaths() {
		if !fileExists(file) {
			continue
		}
		data, err := readTOML(file)
		if err != nil {
			return nil, err
		}
		if err := checkKeys(data, managedKeys, file); err != nil {
			return nil, err
		}
		managed.Paths = append(managed.Paths, file)
		allowed, err := optionalStrings(data, "allowed_agents", file)
		if err != nil {
			return nil, err
		}
		if data["allowed_agents"] != nil {
			managed.allowedAgents = narrow(managed.allowedAgents, allowed, len(managed.Paths) > 1 && managed.allowedAgents != nil)
		}
		denied, err := optionalStrings(data, "denied_agents", file)
		if err != nil {
			return nil, err
		}
		managed.deniedAgents = append(managed.deniedAgents, denied...)
		if data["aws"] != nil {
			table, ok := data["aws"].(map[string]any)
			if !ok {
				return nil, errorf("%s: aws must be a table", file)
			}
			if err := checkKeys(table, managedAWSKeys, file+" [aws]"); err != nil {
				return nil, err
			}
			allowedProfiles, err := optionalStrings(table, "allowed_profiles", file+" [aws]")
			if err != nil {
				return nil, err
			}
			if table["allowed_profiles"] != nil {
				managed.allowedProfiles = append(managed.allowedProfiles, allowedProfiles)
			}
			deniedProfiles, err := optionalStrings(table, "denied_profiles", file+" [aws]")
			if err != nil {
				return nil, err
			}
			managed.deniedProfiles = append(managed.deniedProfiles, deniedProfiles...)
		}
		if value, present := data["repo_kits"]; present {
			text, _ := value.(string)
			if text != RepoKitsAsk && text != RepoKitsDeny {
				return nil, errorf("%s: repo_kits must be %q or %q", file, RepoKitsAsk, RepoKitsDeny)
			}
			if text == RepoKitsDeny {
				managed.repoKits = RepoKitsDeny
			}
		}
		if data["copy_logins"] != nil {
			logins, err := optionalStrings(data, "copy_logins", file)
			if err != nil {
				return nil, err
			}
			managed.copyLogins = narrow(managed.copyLogins, logins, managed.copyLoginsSet)
			managed.copyLoginsSet = true
		}
		if value, present := data["allow_global_policy"]; present {
			allowed, ok := value.(bool)
			if !ok {
				return nil, errorf("%s: allow_global_policy must be true or false", file)
			}
			managed.allowGlobalPolicy = managed.allowGlobalPolicy && allowed
		}
	}
	return managed, nil
}

func narrow(current, next []string, both bool) []string {
	if !both {
		return append([]string{}, next...)
	}
	var kept []string
	for _, name := range current {
		if contains(next, name) {
			kept = append(kept, name)
		}
	}
	return kept
}

var unmanaged = &Managed{repoKits: RepoKitsAsk, allowGlobalPolicy: true}

func (m *Managed) orUnmanaged() *Managed {
	if m == nil {
		return unmanaged
	}
	return m
}

func (m *Managed) where() string { return strings.Join(m.Paths, ", ") }

// CheckAgent refuses an agent the managed settings do not allow. An agent
// given only as a kit reference has no name to check, so it is refused
// whenever an allow list is set.
func (m *Managed) CheckAgent(name string, known bool, source string) error {
	m = m.orUnmanaged()
	if !known {
		if m.allowedAgents != nil {
			return errorf("agent kit %s is not a known agent, and %s allows only %s", source, m.where(), strings.Join(m.allowedAgents, ", "))
		}
		return nil
	}
	if contains(m.deniedAgents, name) {
		return errorf("agent %s is denied by %s", name, m.where())
	}
	if m.allowedAgents != nil && !contains(m.allowedAgents, name) {
		return errorf("agent %s is not allowed by %s (allowed: %s)", name, m.where(), strings.Join(m.allowedAgents, ", "))
	}
	return nil
}

func (m *Managed) CheckAWSProfile(profile string) error {
	m = m.orUnmanaged()
	for _, pattern := range m.deniedProfiles {
		if matched, _ := path.Match(pattern, profile); matched {
			return errorf("AWS profile %s is denied by %s (%s)", profile, m.where(), pattern)
		}
	}
	for _, patterns := range m.allowedProfiles {
		if !matchesPattern(patterns, profile) {
			return errorf("AWS profile %s is not allowed by %s (allowed: %s)", profile, m.where(), strings.Join(patterns, ", "))
		}
	}
	return nil
}

func matchesPattern(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func (m *Managed) RepoKitsDenied() bool { return m.orUnmanaged().repoKits == RepoKitsDeny }

func (m *Managed) CheckCopyLogin(agent string) error {
	m = m.orUnmanaged()
	if m.copyLoginsSet && !contains(m.copyLogins, agent) {
		return errorf("copy_logins: copying %s's login is not allowed by %s", agent, m.where())
	}
	return nil
}

func (m *Managed) AllowsGlobalPolicy() bool { return m.orUnmanaged().allowGlobalPolicy }

// Summary describes the managed settings in effect, for stage setup.
func (m *Managed) Summary() []string {
	if m == nil || len(m.Paths) == 0 {
		return nil
	}
	lines := []string{"managed settings: " + m.where()}
	if m.allowedAgents != nil {
		lines = append(lines, "  allowed agents: "+strings.Join(m.allowedAgents, ", "))
	}
	if len(m.deniedAgents) > 0 {
		lines = append(lines, "  denied agents: "+strings.Join(m.deniedAgents, ", "))
	}
	for _, patterns := range m.allowedProfiles {
		lines = append(lines, "  allowed AWS profiles: "+strings.Join(patterns, ", "))
	}
	if len(m.deniedProfiles) > 0 {
		lines = append(lines, "  denied AWS profiles: "+strings.Join(m.deniedProfiles, ", "))
	}
	lines = append(lines, "  repository kits: "+m.repoKits)
	if m.copyLoginsSet {
		lines = append(lines, "  copyable logins: "+strings.Join(m.copyLogins, ", "))
	}
	if !m.allowGlobalPolicy {
		lines = append(lines, "  --allow-global-policy: refused")
	}
	return lines
}
