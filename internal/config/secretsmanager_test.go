package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestABookListsSecretsManagerSecretsByExactNameUnderAHostProfile(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", `
		[aws]
		profiles = ["build"]
		[aws.secrets_manager]
		build = ["app/db", "app/config", "app/db"]
		vault = ["shared/signing-key"]
	`)
	settings, selection := withBooks(t, filepath.Join(dir, "home"), path)
	composition := composeWith(t, settings, selection)
	want := map[string][]string{"build": {"app/config", "app/db"}, "vault": {"shared/signing-key"}}
	if !reflect.DeepEqual(composition.AWSSecretsManager, want) {
		t.Errorf("secrets manager = %v, want %v", composition.AWSSecretsManager, want)
	}
	if !reflect.DeepEqual(composition.AWSProfiles, []string{"build"}) || composition.AWSDefaultProfile != "build" {
		t.Errorf("a profile named only for Secrets Manager was granted to the sandbox: profiles %v, default %q", composition.AWSProfiles, composition.AWSDefaultProfile)
	}
}

func TestAProfileNamedOnlyForSecretsManagerGivesTheSandboxNoCredentials(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", "[aws.secrets_manager]\nvault = [\"shared/signing-key\"]\n")
	settings, selection := withBooks(t, filepath.Join(dir, "home"), path)
	composition := composeWith(t, settings, selection)
	if len(composition.AWSProfiles) != 0 || composition.AWSDefaultProfile != "" {
		t.Errorf("profiles %v, default %q", composition.AWSProfiles, composition.AWSDefaultProfile)
	}
	if !reflect.DeepEqual(composition.AWSSecretsManager, map[string][]string{"vault": {"shared/signing-key"}}) {
		t.Errorf("secrets manager = %v", composition.AWSSecretsManager)
	}
}

func TestSecretsManagerListsFromBooksTheirRepoRulesAndTheUserConfigAreCombined(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	path := book(t, dir, "org", `
		[aws.secrets_manager]
		build = ["app/config"]
		[[repos]]
		match = "api"
		aws = { secrets_manager = { build = ["api/key", "app/config"] } }
		[[repos]]
		match = "web"
		aws = { secrets_manager = { build = ["web/key"] } }
	`)
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[aws.secrets_manager]\nmine = [\"personal/token\"]\n")
	composition := composeWith(t, load(t, home), Selection{Books: []string{path}, RepoNames: []string{"api"}})
	want := map[string][]string{"build": {"api/key", "app/config"}, "mine": {"personal/token"}}
	if !reflect.DeepEqual(composition.AWSSecretsManager, want) {
		t.Errorf("secrets manager = %v, want %v", composition.AWSSecretsManager, want)
	}
}

func TestOneSecretNameListedUnderTwoProfilesIsAnError(t *testing.T) {
	dir := t.TempDir()
	first := book(t, dir, "one", "[aws.secrets_manager]\ndev = [\"app/db\"]\n")
	second := book(t, dir, "two", "[aws.secrets_manager]\nprod = [\"app/db\"]\n")
	_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{first, second}})
	expectError(t, err, "book two lists Secrets Manager secret app/db under profile prod, but book one listed it under dev")
}

func TestSecretsManagerEntriesAreExactSecretNamesUnderProfileNames(t *testing.T) {
	cases := map[string]string{
		`secrets_manager = ["app/db"]`:           "secrets_manager must be a table of profile names to lists of secret names",
		`secrets_manager = { dev = "app/db" }`:   "secrets_manager.dev must be a list of secret names",
		`secrets_manager = { dev = [1] }`:        "secrets_manager.dev must be a list of secret names",
		`secrets_manager = { dev = [] }`:         "secrets_manager.dev lists no secrets",
		`secrets_manager = { dev = ["app/*"] }`:  `secret name "app/*" is not an exact Secrets Manager name`,
		`secrets_manager = { dev = [""] }`:       `secret name "" is not an exact Secrets Manager name`,
		`secrets_manager = { dev = ["app db"] }`: `secret name "app db" is not an exact Secrets Manager name`,
		`secrets_manager = { dev = ["arn:aws:secretsmanager:us-east-1:111122223333:secret:app-AbCdEf"] }`: "is not an exact Secrets Manager name",
		`secrets_manager = { dev = ["` + strings.Repeat("a", 513) + `"] }`:                                "is not an exact Secrets Manager name",
		`secrets_manager = { "dev team" = ["app/db"] }`:                                                   `profile "dev team" is not an AWS profile name`,
		`secrets_manager = { "-dev" = ["app/db"] }`:                                                       `profile "-dev" is not an AWS profile name`,
		`secrets_manager = { dev = ["-app"] }`:                                                            `secret name "-app" is not an exact Secrets Manager name`,
		`secrets_manager = {}`:                                                                            "secrets_manager lists no profiles",
	}
	for body, fragment := range cases {
		dir := t.TempDir()
		path := book(t, dir, "org", "[aws]\n"+body+"\n")
		_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
		if err == nil {
			_, err = LoadBook(BuiltinAgents(), path)
		}
		expectError(t, err, fragment)
	}
	longest := strings.Repeat("a", 512)
	dir := t.TempDir()
	path := book(t, dir, "org", `[aws.secrets_manager]
dev = ["`+longest+`", "team/app_1+x=y.z@acme-2"]
`)
	composition := composeWith(t, load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
	if got := composition.AWSSecretsManager["dev"]; len(got) != 2 {
		t.Errorf("valid names were refused: %v", got)
	}
}

func TestManagedAWSProfilePatternsApplyToSecretsManagerProfiles(t *testing.T) {
	managedFiles(t, "[aws]\nallowed_profiles = [\"dev*\"]\n")
	dir := t.TempDir()
	path := book(t, dir, "org", "[aws.secrets_manager]\nprod = [\"app/db\"]\n")
	_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
	expectError(t, err, "AWS profile prod is not allowed by")
}

func networkKit(phase, entry string) string {
	if !strings.HasPrefix(entry, "{") {
		entry = fmt.Sprintf("%q", entry)
	}
	return fmt.Sprintf("capabilities:\n  - type: com.docker.sandbox/network-policy@2\n    config:\n      %s:\n        allow:\n          - %s\n", phase, entry)
}

func TestNoKitMayAllowASecretsManagerHostWhenSecretsAreListed(t *testing.T) {
	reaching := []string{
		"secretsmanager.us-east-1.amazonaws.com",
		"secretsmanager.us-east-1.amazonaws.com:443",
		"SecretsManager.US-East-1.amazonaws.com.",
		"secretsmanager-fips.us-gov-west-1.amazonaws.com",
		"secretsmanager.eu-west-1.api.aws",
		"secretsmanager.cn-north-1.amazonaws.com.cn",
		"vpce-0abc123-def456.secretsmanager.us-east-1.vpce.amazonaws.com",
		"us-east-1.amazonaws.com",
		"amazonaws.com",
		"*.amazonaws.com",
		"*.us-east-1.amazonaws.com",
		"*.api.aws",
		"*.aws",
		"*.com",
		"*",
		"**",
		"secretsmanager.*.amazonaws.com",
		"*.secretsmanager.us-east-1.vpce.amazonaws.com",
		"{hosts: [secretsmanager.us-west-2.amazonaws.com], methods: [POST]}",
		"*manager.us-east-1.amazonaws.com",
		"*r.us-east-1.amazonaws.com",
		"*-east-1.amazonaws.com",
		"*-fips.us-gov-west-1.amazonaws.com",
		"secretsmanager.us-*-1.amazonaws.com",
		"secretsmanager.us-east-1.amazonaws.c*m",
		"*1.vpce.amazonaws.com",
		"secretsmanager.eusc-de-east-1.amazonaws.eu",
		"eusc-de-east-1.amazonaws.eu",
		"*.eusc-de-east-1.amazonaws.eu",
		"us-isob-east-1.sc2s.sgov.gov",
		"*.api.amazonwebservices.eu",
		"*.api.aws.ic.gov",
		"*.api.aws.scloud",
		"*.api.cloud-aws.adc-e.uk",
		"*.api.aws.hci.ic.gov",
		"secretsmanager.us-isof-south-1.api.aws.hci.ic.gov",
		"*.us-east2-1.amazonaws.com",
		"*.apacx-east-1.amazonaws.com",
		"*.us-east-1a.amazonaws.com",
		"us-east2-1.amazonaws.com",
		"apacx-east-1.amazonaws.com",
	}
	unreadable := []string{
		"secretsmanager.us-east-?.amazonaws.com",
		"https://secretsmanager.us-east-1.amazonaws.com",
		"secretsmanager.us-east-1.amazonaws.com/",
		"secretsmanager.us-east-1.amazonaws.com:https",
		"",
		"{hosts: []}",
		".amazonaws.com",
		".us-east-1.amazonaws.com",
		"*.amazonaws.com..",
		"secretsmanager..us-east-1.amazonaws.com",
		"10.0.0.1",
		"52.94.0.1:443",
		"0x7f.0.0.1",
		"0x7f000001",
		"2130706433",
	}
	compose := func(phase, entry string) error {
		dir := t.TempDir()
		path := book(t, dir, "org", "kits = [\"./kits/open\"]\n[aws.secrets_manager]\ndev = [\"app/db\"]\n")
		testutil.WriteFile(t, filepath.Join(path, "kits", "open", "kit.yaml"), networkKit(phase, entry))
		_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
		if err != nil && !strings.Contains(err.Error(), filepath.Join(path, "kits", "open")) {
			t.Errorf("%s: the error does not name the kit: %v", entry, err)
		}
		return err
	}
	for _, phase := range []string{"runtime", "install"} {
		for _, entry := range reaching {
			if err := compose(phase, entry); err == nil || !strings.Contains(err.Error(), "which reaches AWS Secrets Manager") {
				t.Errorf("%s %q: %v", phase, entry, err)
			}
		}
		for _, entry := range unreadable {
			if err := compose(phase, entry); err == nil || !strings.Contains(err.Error(), "which stage cannot read as a host") {
				t.Errorf("%s %q: %v", phase, entry, err)
			}
		}
		for _, entry := range []string{"s3.us-east-1.amazonaws.com", "*.s3.amazonaws.com", "sts.amazonaws.com", "api.github.com", "*.example.com", "ssm.us-east-1.amazonaws.com", "registry.npmjs.org:443", "s3-us-west-2.amazonaws.com", "s3-external-1.amazonaws.com", "*.s3-us-west-2.amazonaws.com", "*.compute-1.amazonaws.com", "ec2.us-east-1.api.aws", "*.execute-api.us-east-1.amazonaws.com", "*.s3-accesspoint.us-east-1.amazonaws.com", "*.elb.us-east-1.amazonaws.com"} {
			if err := compose(phase, entry); err != nil {
				t.Errorf("%s %q: %v", phase, entry, err)
			}
		}
	}
}

func TestAKitsNetworkPolicyMustBeAShapeStageCanReadWhenSecretsAreListed(t *testing.T) {
	for fragment, config := range map[string]string{
		"network-policy config key Runtime":                                           "Runtime:\n        allow: [api.github.com]",
		"network-policy config key egress":                                            "egress:\n        allow: [api.github.com]",
		"network-policy runtime must be a mapping":                                    "runtime: [api.github.com]",
		"network-policy runtime key Allow":                                            "runtime:\n        Allow: [secretsmanager.us-east-1.amazonaws.com]",
		"network-policy runtime allow must be a list":                                 "runtime:\n        allow: secretsmanager.us-east-1.amazonaws.com",
		"network-policy install allow must be a list":                                 "install:\n        allow: {hosts: [x]}",
		"network-policy runtime allow entry key Hosts is not hosts, methods or paths": "runtime:\n        allow:\n          - {hosts: [github.com], Hosts: [secretsmanager.us-east-1.amazonaws.com]}",
		"network-policy runtime allow entry key host is not hosts, methods or paths":  "runtime:\n        allow:\n          - {hosts: [github.com], host: secretsmanager.us-east-1.amazonaws.com}",
		"network-policy runtime allow entry hosts must be a list of strings":          "runtime:\n        allow:\n          - {hosts: secretsmanager.us-east-1.amazonaws.com}",
		"network-policy runtime allow entries must be hosts or mappings":              "runtime:\n        allow:\n          - [secretsmanager.us-east-1.amazonaws.com]",
	} {
		dir := t.TempDir()
		path := book(t, dir, "org", "kits = [\"./kits/open\"]\n[aws.secrets_manager]\ndev = [\"app/db\"]\n")
		testutil.WriteFile(t, filepath.Join(path, "kits", "open", "kit.yaml"), "capabilities:\n  - type: com.docker.sandbox/network-policy@2\n    config:\n      "+config+"\n")
		_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
		expectError(t, err, fragment)
	}
}

func TestAUserKitStageCannotReadIsRefusedWhenSecretsAreListed(t *testing.T) {
	for name, kit := range map[string]string{
		"extends another kit": "extends: docker.io/acme/base:1\ncapabilities: []\n",
		"is unreadable":       "capabilities: [\n",
		"is not a directory":  "",
		"unrecognised capability type \"com.docker.sandbox/egress@9\"": "capabilities:\n  - type: com.docker.sandbox/egress@9\n    config: {allow: [secretsmanager.us-east-1.amazonaws.com]}\n",
		"unrecognised capability type \"\"":                            "capabilities:\n  - config: {runtime: {allow: [secretsmanager.us-east-1.amazonaws.com]}}\n",
	} {
		dir := t.TempDir()
		home := filepath.Join(dir, "home")
		if kit != "" {
			testutil.WriteFile(t, filepath.Join(home, "kits", "mine", "kit.yaml"), kit)
		}
		testutil.WriteFile(t, filepath.Join(home, "config.toml"), "kits = [\"./kits/mine\"]\n[aws.secrets_manager]\ndev = [\"app/db\"]\n")
		_, err := Compose(load(t, home), Selection{})
		expectError(t, err, name)
		expectError(t, err, "stage reads every local kit to check that")
	}
}

func TestEverySourceOfASecretMustListItUnderTheSameProfile(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	path := book(t, dir, "org", "[aws.secrets_manager]\ndev = [\"app/db\"]\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "[aws.secrets_manager]\nprod = [\"app/db\"]\n")
	_, err := Compose(load(t, home), Selection{Books: []string{path}})
	expectError(t, err, "config lists Secrets Manager secret app/db under profile prod, but book org listed it under dev")

	own := book(t, dir, "own", `
		[aws.secrets_manager]
		dev = ["app/db"]
		[[repos]]
		match = "api"
		aws = { secrets_manager = { prod = ["app/db"] } }
	`)
	_, err = Compose(load(t, filepath.Join(dir, "other-home")), Selection{Books: []string{own}, RepoNames: []string{"api"}})
	expectError(t, err, "book own repo api lists Secrets Manager secret app/db under profile prod, but book own listed it under dev")
}

func TestManagedDeniedProfilesApplyToSecretsManagerProfiles(t *testing.T) {
	managedFiles(t, "[aws]\ndenied_profiles = [\"*admin*\"]\n")
	dir := t.TempDir()
	path := book(t, dir, "org", "[aws.secrets_manager]\nvault-admin = [\"app/db\"]\n")
	_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
	expectError(t, err, "AWS profile vault-admin is denied by")
}

func TestAProfileGrantedToTheSandboxAndListedForSecretsManagerIsWarnedAbout(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", "[aws]\nprofiles = [\"build\"]\n[aws.secrets_manager]\nbuild = [\"app/db\"]\nvault = [\"shared/key\"]\n")
	composition := composeWith(t, load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}, AWSProfile: "vault"})
	for _, profile := range []string{"build", "vault"} {
		want := "AWS profile " + profile + " is granted to the sandbox and also listed for Secrets Manager secrets; " +
			"its keys in the sandbox can read unlisted secrets through any open host that reads secrets for them"
		if !contains(composition.Warnings, want) {
			t.Errorf("warnings %q, want %q", composition.Warnings, want)
		}
	}
}

func TestAKitMayStillAllowSecretsManagerWhenNoSecretsAreListed(t *testing.T) {
	dir := t.TempDir()
	path := book(t, dir, "org", "kits = [\"./kits/open\"]\n[aws]\nprofiles = [\"dev\"]\n")
	testutil.WriteFile(t, filepath.Join(path, "kits", "open", "kit.yaml"), networkKit("runtime", "secretsmanager.us-east-1.amazonaws.com"))
	composeWith(t, load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
}

func TestTheUsersAndTheRepositorysLocalKitsCannotAllowSecretsManagerWhenABookListsSecrets(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	path := book(t, dir, "org", "[aws.secrets_manager]\ndev = [\"app/db\"]\n")
	testutil.WriteFile(t, filepath.Join(home, "kits", "mine", "kit.yaml"), networkKit("runtime", "*.amazonaws.com"))
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "kits = [\"./kits/mine\"]\n")
	_, err := Compose(load(t, home), Selection{Books: []string{path}})
	expectError(t, err, filepath.Join(home, "kits", "mine")+" allows *.amazonaws.com, which reaches AWS Secrets Manager")

	repoDir := filepath.Join(dir, "repo")
	testutil.WriteFile(t, filepath.Join(repoDir, RepoConfigPath), "kits = [\"./kit\"]\n")
	testutil.WriteFile(t, filepath.Join(repoDir, "kit", "kit.yaml"), networkKit("runtime", "secretsmanager.us-east-1.amazonaws.com"))
	repo, err := LoadRepoConfig(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compose(load(t, filepath.Join(dir, "other-home")), Selection{Books: []string{path}, Repo: repo})
	expectError(t, err, "reaches AWS Secrets Manager")
}

func TestABooksApprovalNamesTheSecretsManagerSecretsItListsAndTheirProfiles(t *testing.T) {
	path := book(t, t.TempDir(), "org", `
		[aws.secrets_manager]
		build = ["app/db", "app/config"]
		[[repos]]
		match = "api"
		aws = { secrets_manager = { vault = ["api/key"] } }
	`)
	loaded, err := LoadBook(BuiltinAgents(), path)
	if err != nil {
		t.Fatal(err)
	}
	details := summarizeBook(BuiltinAgents(), *loaded)
	for _, want := range []string{
		"AWS Secrets Manager secrets listed for profile build: app/config, app/db",
		"repos matching api: AWS Secrets Manager secrets listed for profile vault: api/key",
	} {
		if !contains(details, want) {
			t.Errorf("details %q, want %q", details, want)
		}
	}
}
