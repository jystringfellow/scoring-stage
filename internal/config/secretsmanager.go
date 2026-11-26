package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var (
	awsProfileName       = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	secretsManagerName   = regexp.MustCompile(`^[A-Za-z0-9/_+=.@][A-Za-z0-9/_+=.@-]{0,511}$`)
	awsRegionLabel       = regexp.MustCompile(`^[a-z]+(-[a-z0-9]+)*-[a-z0-9]+-[0-9]+[a-z]?$`)
	addressLabel         = regexp.MustCompile(`^([0-9]+|0x[0-9a-f]*)$`)
	hostPortSuffix       = regexp.MustCompile(`:[0-9]+$`)
	hostPattern          = regexp.MustCompile(`^[a-z0-9.*-]+$`)
	secretsManagerLabels = []string{"secretsmanager", "secretsmanager-fips"}
	awsPartitionSuffixes = []string{
		"amazonaws.com", "api.aws",
		"amazonaws.com.cn", "api.amazonwebservices.com.cn",
		"amazonaws.eu", "api.amazonwebservices.eu",
		"c2s.ic.gov", "api.aws.ic.gov",
		"sc2s.sgov.gov", "api.aws.scloud",
		"cloud.adc-e.uk", "api.cloud-aws.adc-e.uk",
		"csp.hci.ic.gov", "api.aws.hci.ic.gov",
	}
	secretsManagerPatterns = secretsManagerHostPatterns()
)

const (
	anyRegion = "<region>"
	anyLabel  = "<label>"
)

func secretsManagerHostPatterns() [][]string {
	var patterns [][]string
	for _, suffix := range awsPartitionSuffixes {
		tail := strings.Split(suffix, ".")
		for _, service := range secretsManagerLabels {
			patterns = append(patterns, append([]string{service, anyRegion}, tail...))
		}
		patterns = append(patterns, append([]string{anyLabel, "secretsmanager", anyRegion, "vpce"}, tail...))
	}
	return patterns
}

func decodeSecretsManager(value any, where string) (map[string][]string, error) {
	table, ok := value.(map[string]any)
	if !ok {
		return nil, errorf("%s: secrets_manager must be a table of profile names to lists of secret names, such as build = [\"app/config\"]", where)
	}
	if len(table) == 0 {
		return nil, errorf("%s: secrets_manager lists no profiles; name a profile and its secrets, or remove the table", where)
	}
	listed := map[string][]string{}
	for _, profile := range sortedKeys(table) {
		if !awsProfileName.MatchString(profile) {
			return nil, errorf("%s: secrets_manager: profile %q is not an AWS profile name", where, profile)
		}
		names, err := asStrings(table[profile])
		if err != nil {
			return nil, errorf("%s: secrets_manager.%s must be a list of secret names", where, profile)
		}
		if len(names) == 0 {
			return nil, errorf("%s: secrets_manager.%s lists no secrets; name the secrets or remove the profile", where, profile)
		}
		for _, name := range names {
			if !secretsManagerName.MatchString(name) {
				return nil, errorf("%s: secrets_manager.%s: secret name %q is not an exact Secrets Manager name "+
					"(1 to 512 of A-Z a-z 0-9 / _ + = . @ -, not starting with -); stage takes exact names, not prefixes, wildcards or ARNs", where, profile, name)
			}
		}
		slices.Sort(names)
		listed[profile] = slices.Compact(names)
	}
	return listed, nil
}

type listedSecret struct{ profile, owner string }

type secretsManagerListing map[string]listedSecret

func (l secretsManagerListing) add(byProfile map[string][]string, owner string) error {
	profiles := make([]string, 0, len(byProfile))
	for profile := range byProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	for _, profile := range profiles {
		for _, name := range byProfile[profile] {
			if existing, found := l[name]; found {
				if existing.profile != profile {
					return errorf("%s lists Secrets Manager secret %s under profile %s, but %s listed it under %s; "+
						"each secret is listed under one profile", owner, name, profile, existing.owner, existing.profile)
				}
				continue
			}
			l[name] = listedSecret{profile, owner}
		}
	}
	return nil
}

func (l secretsManagerListing) profiles() []string {
	var profiles []string
	for _, secret := range l {
		if !slices.Contains(profiles, secret.profile) {
			profiles = append(profiles, secret.profile)
		}
	}
	sort.Strings(profiles)
	return profiles
}

func (l secretsManagerListing) owners() []string {
	var owners []string
	for _, secret := range l {
		if !slices.Contains(owners, secret.owner) {
			owners = append(owners, secret.owner)
		}
	}
	sort.Strings(owners)
	return owners
}

func (l secretsManagerListing) byProfile() map[string][]string {
	if len(l) == 0 {
		return nil
	}
	result := map[string][]string{}
	for name, secret := range l {
		result[secret.profile] = append(result[secret.profile], name)
	}
	for _, names := range result {
		sort.Strings(names)
	}
	return result
}

func (l secretsManagerListing) refuseKitsOpeningSecretsManager(kits []Kit, coreKits string) error {
	if len(l) == 0 {
		return nil
	}
	reason := fmt.Sprintf("%s list Secrets Manager secrets, so no kit may open Secrets Manager itself",
		strings.Join(l.owners(), ", "))
	for _, kit := range kits {
		if !kit.IsLocal() || filepath.Dir(kit.Source) == coreKits {
			continue
		}
		hosts, err := allowedHosts(kit.Source)
		if err != nil {
			return errorf("%v; %s, and stage reads every local kit to check that", err, reason)
		}
		for _, host := range hosts {
			switch secretsManagerReach(host) {
			case hostUnreadable:
				return errorf("kit %s allows \"%s\", which stage cannot read as a host. %s", kit.Source, Printable(host), reason)
			case hostReachesSecretsManager:
				return errorf("kit %s allows %s, which reaches AWS Secrets Manager. %s", kit.Source, Printable(host), reason)
			}
		}
	}
	return nil
}

func allowedHosts(dir string) ([]string, error) {
	documents, err := loadKit(dir)
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, document := range documents {
		where := fmt.Sprintf("kit %s (%s)", dir, document.file)
		if document.spec.Extends != nil {
			return nil, errorf("%s extends another kit, which stage cannot read", where)
		}
		for _, capability := range document.spec.Capabilities {
			switch capabilityName(capability.Type) {
			case "":
				return nil, errorf("%s: unrecognised capability type \"%s\"", where, Printable(capability.Type))
			case "network-policy":
			default:
				continue
			}
			for _, phase := range slices.Sorted(maps.Keys(capability.Config)) {
				if phase != "install" && phase != "runtime" {
					return nil, errorf("%s: network-policy config key %s is not install or runtime", where, Printable(phase))
				}
				rules, ok := capability.Config[phase].(map[string]any)
				if !ok {
					return nil, errorf("%s: network-policy %s must be a mapping of allow and deny lists", where, phase)
				}
				for _, key := range slices.Sorted(maps.Keys(rules)) {
					if key != "allow" && key != "deny" {
						return nil, errorf("%s: network-policy %s key %s is not allow or deny", where, phase, Printable(key))
					}
				}
				if rules["allow"] == nil {
					continue
				}
				entries, ok := rules["allow"].([]any)
				if !ok {
					return nil, errorf("%s: network-policy %s allow must be a list", where, phase)
				}
				for _, entry := range entries {
					named, err := entryHosts(entry)
					if err != nil {
						return nil, errorf("%s: network-policy %s allow %v", where, phase, err)
					}
					hosts = append(hosts, named...)
				}
			}
		}
	}
	return hosts, nil
}

var allowEntryKeys = []string{"hosts", "methods", "paths"}

func entryHosts(entry any) ([]string, error) {
	switch typed := entry.(type) {
	case string:
		return []string{typed}, nil
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			if !slices.Contains(allowEntryKeys, key) {
				return nil, errorf("entry key %s is not hosts, methods or paths", Printable(key))
			}
		}
		names, ok := typed["hosts"].([]any)
		if !ok && typed["hosts"] != nil {
			return nil, errorf("entry hosts must be a list of strings")
		}
		if len(names) == 0 {
			return []string{"?"}, nil
		}
		hosts := make([]string, len(names))
		for i, name := range names {
			host, ok := name.(string)
			if !ok {
				return nil, errorf("entry hosts must be a list of strings")
			}
			hosts[i] = host
		}
		return hosts, nil
	}
	return nil, errorf("entries must be hosts or mappings")
}

type hostReach int

const (
	hostClear hostReach = iota
	hostReachesSecretsManager
	hostUnreadable
)

func secretsManagerReach(entry string) hostReach {
	host := strings.TrimSuffix(hostPortSuffix.ReplaceAllString(strings.ToLower(strings.TrimSpace(entry)), ""), ".")
	if !hostPattern.MatchString(host) || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return hostUnreadable
	}
	if !strings.Contains(host, "*") && !slices.ContainsFunc(strings.Split(host, "."), func(label string) bool { return !addressLabel.MatchString(label) }) {
		return hostUnreadable
	}
	partial := false
	if star := strings.LastIndex(host, "*"); star >= 0 {
		host, partial = host[star+1:], true
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if strings.Contains(label, "secretsmanager") {
			return hostReachesSecretsManager
		}
	}
	for _, pattern := range secretsManagerPatterns {
		if endsLike(labels, pattern, partial) {
			return hostReachesSecretsManager
		}
	}
	return hostClear
}

func secretsManagerDetails(byProfile map[string][]string, where string) []string {
	profiles := make([]string, 0, len(byProfile))
	for profile := range byProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	details := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		details = append(details, fmt.Sprintf("%sAWS Secrets Manager secrets listed for profile %s: %s", where, profile, strings.Join(byProfile[profile], ", ")))
	}
	return details
}

func endsLike(labels, pattern []string, firstIsPartial bool) bool {
	if len(labels) > len(pattern) {
		return false
	}
	offset := len(pattern) - len(labels)
	for i, label := range labels {
		want := pattern[offset+i]
		if i == 0 && firstIsPartial {
			if want != anyRegion && want != anyLabel && !strings.HasSuffix(want, label) {
				return false
			}
			continue
		}
		switch want {
		case anyRegion:
			if !awsRegionLabel.MatchString(label) {
				return false
			}
		case anyLabel:
			if label == "" {
				return false
			}
		default:
			if label != want {
				return false
			}
		}
	}
	return true
}
