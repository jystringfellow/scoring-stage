package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

const (
	RepoKitsAsk  = "ask"
	RepoKitsDeny = "deny"
)

var managedKeys = []string{"allowed_agents", "denied_agents", "aws", "github", "repo_kits", "copy_logins", "allow_global_policy"}
var managedAWSKeys = []string{"allowed_profiles", "denied_profiles"}
var managedGitHubKeys = []string{"allowed_modes", "app_client_id"}
var managedDefaultGitHubModes = []string{GitHubFineGrained, GitHubNone}

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
	allowedAgentsSet  bool
	deniedAgents      []string
	allowedProfiles   [][]string
	deniedProfiles    []string
	repoKits          string
	copyLogins        []string
	copyLoginsSet     bool
	allowGlobalPolicy bool
	gitHubModes       []string
	gitHubModesSet    bool
	appClientID       string
	appClientIDFile   string
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func managedFilePresent(file string) (bool, error) {
	_, err := os.Stat(file)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, errorf("cannot read managed settings %s: %v; refusing to run unmanaged", file, err)
	}
}

func lowerPatterns(patterns []string, where string) ([]string, error) {
	lowered := make([]string, len(patterns))
	for i, pattern := range patterns {
		lowered[i] = strings.ToLower(pattern)
		if _, err := path.Match(lowered[i], ""); err != nil {
			return nil, errorf("%s: bad pattern %q: %v", where, pattern, err)
		}
	}
	return lowered, nil
}

// LoadManaged merges every managed file that exists; each can only narrow the others.
func LoadManaged() (*Managed, error) {
	managed := &Managed{repoKits: RepoKitsAsk, allowGlobalPolicy: true}
	for _, file := range ManagedPaths() {
		present, err := managedFilePresent(file)
		if err != nil {
			return nil, err
		}
		if !present {
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
			managed.allowedAgents = narrow(managed.allowedAgents, allowed, managed.allowedAgentsSet)
			managed.allowedAgentsSet = true
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
			if allowedProfiles, err = lowerPatterns(allowedProfiles, file+" [aws] allowed_profiles"); err != nil {
				return nil, err
			}
			if table["allowed_profiles"] != nil {
				managed.allowedProfiles = append(managed.allowedProfiles, allowedProfiles)
			}
			deniedProfiles, err := optionalStrings(table, "denied_profiles", file+" [aws]")
			if err != nil {
				return nil, err
			}
			if deniedProfiles, err = lowerPatterns(deniedProfiles, file+" [aws] denied_profiles"); err != nil {
				return nil, err
			}
			managed.deniedProfiles = append(managed.deniedProfiles, deniedProfiles...)
		}
		if err := managed.loadGitHub(data["github"], file); err != nil {
			return nil, err
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

func (m *Managed) loadGitHub(value any, file string) error {
	if value == nil {
		return nil
	}
	where := file + " [github]"
	table, ok := value.(map[string]any)
	if !ok {
		return errorf("%s: github must be a table", file)
	}
	if err := checkKeys(table, managedGitHubKeys, where); err != nil {
		return err
	}
	clientID, err := decodeGitHubAppClientID(table, file)
	if err != nil {
		return err
	}
	if clientID != "" {
		if m.appClientID != "" && m.appClientID != clientID {
			return errorf("%s and %s pin different GitHub App client IDs (%s, %s)", m.appClientIDFile, file, m.appClientID, clientID)
		}
		m.appClientID, m.appClientIDFile = clientID, file
	}
	if table["allowed_modes"] == nil {
		return nil
	}
	modes, err := optionalStrings(table, "allowed_modes", where)
	if err != nil {
		return err
	}
	unique := []string{}
	for _, mode := range modes {
		if !contains(GitHubModes, mode) {
			return errorf("%s: allowed_modes has unknown mode %q; modes are %s", where, mode, quotedList(GitHubModes))
		}
		if !contains(unique, mode) {
			unique = append(unique, mode)
		}
	}
	m.gitHubModes = narrow(m.gitHubModes, unique, m.gitHubModesSet)
	m.gitHubModesSet = true
	return nil
}

func narrow(current, next []string, both bool) []string {
	if !both {
		return append([]string{}, next...)
	}
	kept := []string{}
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
		if m.HasAgentPolicy() {
			return errorf("agent kit %s is not a known agent, and %s limits agents", source, m.where())
		}
		return nil
	}
	if contains(m.deniedAgents, name) {
		return errorf("agent %s is denied by %s", name, m.where())
	}
	if m.allowedAgentsSet && !contains(m.allowedAgents, name) {
		return errorf("agent %s is not allowed by %s (allowed: %s)", name, m.where(), strings.Join(m.allowedAgents, ", "))
	}
	return nil
}

// HasAgentPolicy reports whether the managed settings limit agents at all.
func (m *Managed) HasAgentPolicy() bool {
	m = m.orUnmanaged()
	return m.allowedAgentsSet || len(m.deniedAgents) > 0
}

func (m *Managed) CheckAWSProfile(profile string) error {
	m = m.orUnmanaged()
	profile = strings.ToLower(profile)
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

func (m *Managed) AllowedGitHubModes() []string {
	m = m.orUnmanaged()
	switch {
	case m.gitHubModesSet:
		return append([]string{}, m.gitHubModes...)
	case len(m.Paths) > 0:
		return slices.Clone(managedDefaultGitHubModes)
	}
	return slices.Clone(GitHubModes)
}

func (m *Managed) DefaultGitHubMode() string {
	if allowed := m.AllowedGitHubModes(); m.orUnmanaged().gitHubModesSet && len(allowed) == 1 {
		return allowed[0]
	}
	return ""
}

func (m *Managed) CheckGitHubMode(mode string) error {
	m = m.orUnmanaged()
	allowed := m.AllowedGitHubModes()
	switch {
	case contains(allowed, mode):
		return nil
	case len(allowed) == 0:
		return m.noGitHubModeAllowed()
	case !m.gitHubModesSet:
		return errorf("GitHub mode %q is not allowed by %s: managed settings allow a mode outside %s only when [github] allowed_modes lists it (allowed: %s)",
			mode, m.where(), quotedList(managedDefaultGitHubModes), quotedList(allowed))
	}
	return errorf("GitHub mode %q is not allowed by %s (allowed: %s)", mode, m.where(), quotedList(allowed))
}

func (m *Managed) noGitHubModeAllowed() error {
	return errorf("%s sets [github] allowed_modes = [], so no GitHub mode is allowed and stage up refuses every sandbox; "+
		"ask the administrator to allow a mode", m.where())
}

func (m *Managed) gitHubModesSummary() string {
	allowed := m.AllowedGitHubModes()
	summary := strings.Join(allowed, ", ")
	if len(allowed) == 0 {
		summary = "no mode (stage up refuses every sandbox)"
	}
	if !m.gitHubModesSet {
		summary += " (any mode outside " + strings.Join(managedDefaultGitHubModes, " and ") + " needs listing in [github] allowed_modes)"
	}
	return summary
}

// Summary describes the managed settings in effect, for stage setup.
func (m *Managed) Summary() []string {
	if m == nil || len(m.Paths) == 0 {
		return nil
	}
	lines := []string{"managed settings: " + m.where()}
	if m.allowedAgentsSet {
		allowed := strings.Join(m.allowedAgents, ", ")
		if allowed == "" {
			allowed = "none"
		}
		lines = append(lines, "  allowed agents: "+allowed)
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
	lines = append(lines, "  allowed GitHub modes: "+m.gitHubModesSummary())
	if m.appClientID != "" {
		lines = append(lines, "  GitHub App client ID (pinned): "+m.appClientID)
	}
	if !m.allowGlobalPolicy {
		lines = append(lines, "  --allow-global-policy: refused")
	}
	return lines
}

// Warnings flags managed files a non-administrator could have written.
func (m *Managed) Warnings() []string {
	if m == nil {
		return nil
	}
	var warnings []string
	for _, file := range m.Paths {
		if strings.HasPrefix(file, "/mnt/") {
			warnings = append(warnings, file+": on a Windows drive, so stage cannot check who can write it")
			continue
		}
		for _, target := range []string{file, filepath.Dir(file)} {
			info, err := os.Stat(target)
			if err != nil {
				continue
			}
			if info.Mode().Perm()&0o022 != 0 {
				warnings = append(warnings, fmt.Sprintf("%s is writable by group or others (mode %o)", target, info.Mode().Perm()))
			}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != 0 {
				warnings = append(warnings, fmt.Sprintf("%s is not owned by root", target))
			}
		}
	}
	if len(m.Paths) == 0 {
		return warnings
	}
	appAllowed := contains(m.AllowedGitHubModes(), GitHubApp)
	switch {
	case appAllowed && m.appClientID == "":
		warnings = append(warnings, fmt.Sprintf("[github] allowed_modes allows %q but no managed file pins app_client_id, so users may log in "+
			"with any GitHub App and only stage's read-only installation check stands between them and an app that can write", GitHubApp))
	case !appAllowed && m.appClientID != "":
		warnings = append(warnings, fmt.Sprintf("[github] app_client_id %s (%s) has no effect: allowed_modes does not allow %q",
			m.appClientID, m.appClientIDFile, GitHubApp))
	}
	return warnings
}

func (m *Managed) GitHubAppClientID() string { return m.orUnmanaged().appClientID }
