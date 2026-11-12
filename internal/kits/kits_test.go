package kits

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	scoringstage "github.com/jystringfellow/scoring-stage"
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
