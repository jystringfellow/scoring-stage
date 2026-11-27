package kits

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	scoringstage "github.com/jystringfellow/scoring-stage"
	"gopkg.in/yaml.v3"
)

func TestCoreKitsShipWithTheBinary(t *testing.T) {
	for _, path := range []string{"kits/stage/stage.yaml", "kits/aws-broker/aws-broker.yaml"} {
		if _, err := scoringstage.Kits.ReadFile(path); err != nil {
			t.Errorf("%s is not embedded: %v", path, err)
		}
	}
}

func TestExtractWritesTheKitsToAContentHashedDirectoryOnce(t *testing.T) {
	home := t.TempDir()
	dir, err := Extract(home)
	if err != nil {
		t.Fatal(err)
	}
	if dir != Dir(home) || filepath.Dir(dir) != filepath.Join(home, "kits") || len(filepath.Base(dir)) != 16 {
		t.Fatalf("dir = %s", dir)
	}
	embedded, _ := scoringstage.Kits.ReadFile("kits/stage/stage.yaml")
	extracted, err := os.ReadFile(filepath.Join(dir, "stage", "stage.yaml"))
	if err != nil || string(extracted) != string(embedded) {
		t.Fatalf("extracted stage.yaml differs: %v", err)
	}
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if again, err := Extract(home); err != nil || again != dir {
		t.Fatalf("second extract = %s, %v", again, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("second extract replaced the directory")
	}
	entries, _ := os.ReadDir(filepath.Join(home, "kits"))
	if len(entries) != 1 {
		t.Fatalf("staging directories left behind: %v", entries)
	}
}

func TestTheBrokerKitReachesOnlyTheBrokerPort(t *testing.T) {
	content, err := scoringstage.Kits.ReadFile("kits/aws-broker/aws-broker.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "- localhost" || strings.Contains(trimmed, "domain: localhost,") {
			t.Errorf("aws-broker.yaml reaches every localhost port: %q", trimmed)
		}
	}
	if !strings.Contains(string(content), "- localhost:9911") || !strings.Contains(string(content), `domain: "localhost:9911"`) {
		t.Error("aws-broker.yaml does not allow and inject on localhost:9911")
	}
}

func TestTheStageKitsGuidanceHoldsHoweverTheAgentWasLaunched(t *testing.T) {
	kit, err := scoringstage.Kits.ReadFile("kits/stage/stage.yaml")
	if err != nil {
		t.Fatal(err)
	}
	guidance := string(kit)
	for _, claim := range []string{"prompts off", "permission prompts", "work freely"} {
		if strings.Contains(guidance, claim) {
			t.Errorf("the guidance assumes how the agent was launched: %q", claim)
		}
	}
	for _, rule := range []string{"`stage ship`", "deny-all", "HTTP 403", "Credentials are held on the host", "never print, copy or"} {
		if !strings.Contains(guidance, rule) {
			t.Errorf("the guidance lost a rule: %q", rule)
		}
	}
}

type brokerKit struct {
	Args         map[string]struct{ Default string }
	Capabilities []struct {
		Type   string
		Config map[string]any
	}
}

func loadBrokerKit(t *testing.T) brokerKit {
	t.Helper()
	content, err := scoringstage.Kits.ReadFile("kits/aws-broker/aws-broker.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var kit brokerKit
	if err := yaml.Unmarshal(content, &kit); err != nil {
		t.Fatal(err)
	}
	return kit
}

func writtenAWSConfig(t *testing.T, profiles, defaultProfile, secretsManager string) string {
	t.Helper()
	var install string
	for _, capability := range loadBrokerKit(t).Capabilities {
		if capability.Type == "com.docker.sandbox/lifecycle@1" {
			hooks := capability.Config["install"].([]any)
			install = hooks[0].(map[string]any)["command"].(string)
		}
	}
	home := t.TempDir()
	command := exec.Command("sh", "-c", install)
	command.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "STAGE_AWS_PROFILES=" + profiles, "STAGE_AWS_REGION=eu-west-1",
		"STAGE_AWS_DEFAULT_PROFILE=" + defaultProfile, "STAGE_AWS_SECRETS_MANAGER=" + secretsManager}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	written, err := os.ReadFile(filepath.Join(home, ".aws", "config"))
	if err != nil {
		t.Fatal(err)
	}
	return string(written)
}

const helper = "credential_process = /home/agent/.local/bin/stage-aws-credentials"

func TestTheBrokerKitWritesTheSameAWSConfigAsBeforeWhenNoSecretsAreListed(t *testing.T) {
	want := "[profile dev]\nregion = eu-west-1\n" + helper + " dev\n\n" +
		"[profile staging]\nregion = eu-west-1\n" + helper + " staging\n\n" +
		"[default]\nregion = eu-west-1\n" + helper + " dev\n"
	if got := writtenAWSConfig(t, "dev staging", "dev", ""); got != want {
		t.Errorf("config:\n%s\nwant:\n%s", got, want)
	}
}

func TestTheBrokerKitSendsSecretsManagerCallsToTheBrokerUnderTheKeyTheSDKsRead(t *testing.T) {
	endpoint := "[services stage-broker]\nsecrets_manager =\n  endpoint_url = http://host.docker.internal:9912\n"
	want := "[profile build]\nregion = eu-west-1\n" + helper + " build\nservices = stage-broker\n\n" +
		"[default]\nregion = eu-west-1\n" + helper + " build\nservices = stage-broker\n\n" + endpoint
	if got := writtenAWSConfig(t, "build", "build", "on"); got != want {
		t.Errorf("config:\n%s\nwant:\n%s", got, want)
	}
	placeholder := "[default]\nregion = eu-west-1\naws_access_key_id = stage-broker\naws_secret_access_key = stage-broker\nservices = stage-broker\n\n" + endpoint
	if got := writtenAWSConfig(t, "", "", "on"); got != placeholder {
		t.Errorf("with no granted profile:\n%s\nwant:\n%s", got, placeholder)
	}
	if got := writtenAWSConfig(t, "build staging", "", "on"); !strings.HasSuffix(got, placeholder) || strings.Count(got, "services = stage-broker") != 3 {
		t.Errorf("with granted profiles and no default:\n%s", got)
	}
}

func TestTheBrokerKitInjectsTheSandboxsTokenTheSameWayOnBothBrokerPorts(t *testing.T) {
	var allowed, domains []string
	styles := map[string]bool{}
	credentials := 0
	for _, capability := range loadBrokerKit(t).Capabilities {
		switch capability.Type {
		case "com.docker.sandbox/network-policy@2":
			for _, host := range capability.Config["runtime"].(map[string]any)["allow"].([]any) {
				allowed = append(allowed, host.(string))
			}
		case "com.docker.sandbox/credential@1":
			credentials++
			if capability.Config["service"] != "stage-broker" || capability.Config["phase"] != "runtime" || len(capability.Config) != 3 {
				t.Errorf("credential config %v", capability.Config)
			}
			apiKey := capability.Config["apiKey"].(map[string]any)
			if len(apiKey) != 1 {
				t.Errorf("apiKey %v", apiKey)
			}
			for _, rule := range apiKey["inject"].([]any) {
				entry := rule.(map[string]any)
				if len(entry) != 3 {
					t.Errorf("inject entry %v has keys beyond domain, header and format", entry)
				}
				domains = append(domains, fmt.Sprint(entry["domain"]))
				styles[fmt.Sprintf("%s %s", entry["header"], entry["format"])] = true
			}
		}
	}
	if strings.Join(allowed, " ") != "localhost:9911 localhost:9912" {
		t.Errorf("allowed %v", allowed)
	}
	if credentials != 1 {
		t.Errorf("%d credential capabilities, want 1", credentials)
	}
	sort.Strings(domains)
	if strings.Join(domains, " ") != "localhost:9911 localhost:9912" {
		t.Errorf("injected on %v", domains)
	}
	if len(styles) != 1 || !styles["Authorization Bearer %s"] {
		t.Errorf("injected as %v; sbx gives every domain the first entry's header and format, so all must be Authorization Bearer %%s", styles)
	}
}
