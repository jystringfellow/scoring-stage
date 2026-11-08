package envfile

import (
	"path/filepath"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/config"
	"github.com/jystringfellow/scoring-stage/internal/testutil"
	"github.com/jystringfellow/scoring-stage/internal/yamlout"
)

func composition(root string) *config.Composition {
	return &config.Composition{
		Base: config.Kit{Source: "docker.io/sbx/shell:1.1.0"},
		Kits: []config.Kit{
			{Source: "docker.io/sbx/claude-mixin:2.1.319@sha256:abc"},
			{Source: filepath.Join(root, "repo", "kits", "stage")},
			{Source: filepath.Join(root, "repo", "kits", "aws-broker"), Args: map[string]any{"profiles": "dev", "region": "us-east-1"}},
		},
		AWSProfiles: []string{"dev"},
		AWSRegion:   "us-east-1",
		Secrets:     map[string]any{"stage-broker": map[string]any{"command": "/bin/cat /t"}},
		Bindings:    map[string]any{"stage-broker": map[string]any{"apiKey": map[string]any{"domains": []any{"localhost"}}}},
	}
}

func dump(t *testing.T, env yamlout.Map) string {
	t.Helper()
	text, err := yamlout.Dump(env)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestHandleIsTheLastRepositorySegmentWithoutTagOrDigest(t *testing.T) {
	for source, want := range map[string]string{
		"docker.io/sbx/shell:1.1.0":                     "shell",
		"docker.io/sbx/claude-mixin:2.1.319@sha256:abc": "claude-mixin",
		"ghcr.io/org/base@sha256:abc":                   "base",
		"/tmp/kits/dotnet":                              "dotnet",
	} {
		if got := Handle(config.Kit{Source: source}); got != want {
			t.Errorf("Handle(%q) = %q, want %q", source, got, want)
		}
	}
}

func TestEnvClonesTheScratchSourceAndListsEveryKit(t *testing.T) {
	root := t.TempDir()
	env := Build(composition(root), "stage-fix", filepath.Join(root, "home", "tasks", "fix"), Options{})
	want := `schemaVersion: "1"
name: "stage-fix"
agent: "shell"
workspace:
  path: "./src"
  clone: true
kits:
  - "docker.io/sbx/shell:1.1.0"
  - "docker.io/sbx/claude-mixin:2.1.319@sha256:abc"
  - "../../../repo/kits/stage"
  - source: "../../../repo/kits/aws-broker"
    args:
      profiles: "dev"
      region: "us-east-1"
secrets:
  stage-broker:
    command: "/bin/cat /t"
bindings:
  stage-broker:
    apiKey:
      domains:
        - "localhost"
`
	if got := dump(t, env); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestLocalKitsBelowTheEnvDirGetADotSlashPrefix(t *testing.T) {
	root := t.TempDir()
	c := composition(root)
	c.Kits = []config.Kit{{Source: filepath.Join(root, "kits", "local")}}
	env := Build(c, "stage-x", root, Options{})
	kits := env[4].Value.([]any)
	if kits[1] != "./kits/local" {
		t.Fatalf("kit = %v", kits[1])
	}
}

func TestSandboxOptionsOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	c := composition(root)
	c.Secrets, c.Bindings = nil, nil
	cpus := 4
	env := Build(c, "stage-x", root, Options{CPUs: &cpus, Memory: "16g"})
	last := env[len(env)-1]
	if last.Key != "sandboxOptions" || dump(t, yamlout.Map{last}) != "sandboxOptions:\n  cpus: 4\n  memory: \"16g\"\n" {
		t.Fatalf("env ends with %v", last)
	}
	plain := Build(composition(root), "stage-x", root, Options{})
	for _, item := range plain {
		if item.Key == "sandboxOptions" {
			t.Fatal("sandboxOptions without cpus or memory")
		}
	}
}

func TestWritePutsSbxenvYamlInTheTaskDir(t *testing.T) {
	dir := t.TempDir()
	path, err := Write(yamlout.Map{{Key: "schemaVersion", Value: "1"}, {Key: "name", Value: "stage-x"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "sbxenv.yaml") {
		t.Errorf("path = %s", path)
	}
	if got := testutil.ReadFile(t, path); got != "schemaVersion: \"1\"\nname: \"stage-x\"\n" {
		t.Errorf("content = %q", got)
	}
}
