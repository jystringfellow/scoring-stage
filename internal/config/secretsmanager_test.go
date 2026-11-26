package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func TestABookListsSecretsManagerSecretsByExactNameUnderTheHostProfileThatReadsThem(t *testing.T) {
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
		"secretsmanager.us-east-?.amazonaws.com",
		"{hosts: [secretsmanager.us-west-2.amazonaws.com], methods: [POST]}",
	}
	for _, entry := range reaching {
		for _, phase := range []string{"runtime", "install"} {
			dir := t.TempDir()
			path := book(t, dir, "org", "kits = [\"./kits/open\"]\n[aws.secrets_manager]\ndev = [\"app/db\"]\n")
			testutil.WriteFile(t, filepath.Join(path, "kits", "open", "kit.yaml"), networkKit(phase, entry))
			_, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}})
			expectError(t, err, "reaches AWS Secrets Manager")
			expectError(t, err, filepath.Join(path, "kits", "open"))
		}
	}
	for _, entry := range []string{"s3.us-east-1.amazonaws.com", "*.s3.amazonaws.com", "sts.amazonaws.com", "api.github.com", "*.example.com", "ssm.us-east-1.amazonaws.com"} {
		dir := t.TempDir()
		path := book(t, dir, "org", "kits = [\"./kits/open\"]\n[aws.secrets_manager]\ndev = [\"app/db\"]\n")
		testutil.WriteFile(t, filepath.Join(path, "kits", "open", "kit.yaml"), networkKit("runtime", entry))
		if _, err := Compose(load(t, filepath.Join(dir, "home")), Selection{Books: []string{path}}); err != nil {
			t.Errorf("%s: %v", entry, err)
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

func TestABooksApprovalNamesTheSecretsManagerSecretsItListsAndTheProfileThatReadsThem(t *testing.T) {
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
		"AWS Secrets Manager secrets read on your machine with profile build: app/config, app/db",
		"repos matching api: AWS Secrets Manager secrets read on your machine with profile vault: api/key",
	} {
		if !contains(details, want) {
			t.Errorf("details %q, want %q", details, want)
		}
	}
}
