package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var (
	awsProfileName         = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	secretsManagerName     = regexp.MustCompile(`^[A-Za-z0-9/_+=.@-]{1,512}$`)
	awsRegionLabel         = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	secretsManagerLabels   = []string{"secretsmanager", "secretsmanager-fips"}
	awsPartitionSuffixes   = []string{"amazonaws.com", "amazonaws.com.cn", "api.aws", "api.amazonwebservices.com.cn", "amazonaws.eu", "c2s.ic.gov", "sc2s.sgov.gov", "cloud.adc-e.uk", "csp.hci.ic.gov"}
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
					"(1 to 512 of A-Z a-z 0-9 / _ + = . @ -); stage takes exact names, not prefixes, wildcards or ARNs", where, profile, name)
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
						"each secret is read with one profile", owner, name, profile, existing.owner, existing.profile)
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
	reason := fmt.Sprintf("%s list Secrets Manager secrets, which the sandbox reads only through the stage broker, so no kit may open Secrets Manager itself",
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
			if reachesSecretsManager(host) {
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
		if document.spec.Extends != nil {
			return nil, errorf("kit %s (%s) extends another kit, which stage cannot read", dir, document.file)
		}
		for _, capability := range document.spec.Capabilities {
			if capabilityName(capability.Type) != "network-policy" {
				continue
			}
			for _, phase := range capability.Config {
				rules, _ := phase.(map[string]any)
				entries, _ := rules["allow"].([]any)
				for _, entry := range entries {
					hosts = append(hosts, entryHosts(entry)...)
				}
			}
		}
	}
	return hosts, nil
}

func entryHosts(entry any) []string {
	switch typed := entry.(type) {
	case string:
		return []string{typed}
	case map[string]any:
		names, _ := typed["hosts"].([]any)
		if len(names) == 0 {
			return []string{"?"}
		}
		hosts := make([]string, len(names))
		for i, name := range names {
			hosts[i] = fmt.Sprint(name)
		}
		return hosts
	}
	return []string{"?"}
}

func reachesSecretsManager(entry string) bool {
	host := normalizeHost(entry)
	if host == "" || hasDomainGlob(host) {
		return true
	}
	if star := strings.LastIndex(host, "*"); star >= 0 {
		host = strings.TrimPrefix(host[star+1:], ".")
		if host == "" {
			return true
		}
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if slices.Contains(secretsManagerLabels, label) {
			return true
		}
	}
	for _, pattern := range secretsManagerPatterns {
		if endsLike(labels, pattern) {
			return true
		}
	}
	return false
}

func secretsManagerDetails(byProfile map[string][]string, where string) []string {
	profiles := make([]string, 0, len(byProfile))
	for profile := range byProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	details := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		details = append(details, fmt.Sprintf("%sAWS Secrets Manager secrets read on your machine with profile %s: %s", where, profile, strings.Join(byProfile[profile], ", ")))
	}
	return details
}

func endsLike(labels, pattern []string) bool {
	if len(labels) > len(pattern) {
		return false
	}
	offset := len(pattern) - len(labels)
	for i, label := range labels {
		switch want := pattern[offset+i]; want {
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
