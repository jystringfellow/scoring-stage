package kits

import (
	"os"
	"path/filepath"
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
