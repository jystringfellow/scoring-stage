package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

const allowedKitRoot = "a kit's root may hold one descriptor (.yaml, .yml or .json), README, LICENSE, NOTICE, CHANGELOG, AUTHORS, " +
	"CONTRIBUTING (bare or .md or .txt), .gitignore and a files/ directory; everything else belongs under files/"

func toolKit(t *testing.T) (string, string) {
	t.Helper()
	path := book(t, t.TempDir(), "org", "kits = [\"./kits/tool\"]\n")
	kit := filepath.Join(path, "kits", "tool")
	testutil.WriteFile(t, filepath.Join(kit, "tool.yaml"), "capabilities: []\n")
	return path, kit
}

func TestAKitRootHoldsOnlyItsDescriptorDocumentationAndFiles(t *testing.T) {
	padded := strings.Repeat("# a=b\n", 12000) + "# syntax=docker/sandbox-kit:3\n# kit:\n#   kind: mixin\n"
	for name, content := range map[string]string{
		"Dockerfile":      "# syntax=docker/sandbox-kit:3\n# kit:\n#   kind: mixin\nFROM scratch\n",
		"tool.dockerfile": "# syntax=docker/dockerfile:1\nFROM scratch\n",
		"padded":          padded,
		"shebang":         "#!/usr/bin/env docker\n# syntax=docker/sandbox-kit:3\n",
		"json":            "{\"syntax\":\"docker/sandbox-kit:3\"}\n",
		"install.sh":      "#!/bin/sh\necho hi\n",
		"notes.markdown":  "notes\n",
	} {
		path, kit := toolKit(t)
		testutil.WriteFile(t, filepath.Join(kit, name), content)
		_, err := LoadBook(BuiltinAgents(), path)
		if err == nil || !strings.Contains(err.Error(), name+" is not allowed at the kit's root") || !strings.Contains(err.Error(), allowedKitRoot) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAKitRootRefusesDirectoriesLinksAndAnotherDescriptor(t *testing.T) {
	path, kit := toolKit(t)
	testutil.WriteFile(t, filepath.Join(kit, "lib", "helper.yaml"), "capabilities: []\n")
	_, err := LoadBook(BuiltinAgents(), path)
	expectError(t, err, "lib is a directory, which is not allowed at the kit's root")

	path, kit = toolKit(t)
	testutil.WriteFile(t, filepath.Join(kit, "extra.yml"), "capabilities: []\n")
	_, err = LoadBook(BuiltinAgents(), path)
	expectError(t, err, "has 2 descriptors (extra.yml, tool.yaml); a kit has exactly one")

	path, kit = toolKit(t)
	testutil.WriteFile(t, filepath.Join(path, "shared", "Dockerfile"), "# syntax=docker/sandbox-kit:3\n")
	if err := os.Symlink(filepath.Join(path, "shared", "Dockerfile"), filepath.Join(kit, "README")); err != nil {
		t.Fatal(err)
	}
	_, err = LoadBook(BuiltinAgents(), path)
	expectError(t, err, "README is a link, which is not allowed at the kit's root")

	path, kit = toolKit(t)
	os.MkdirAll(filepath.Join(path, "elsewhere"), 0o755)
	if err := os.Symlink(filepath.Join(path, "elsewhere"), filepath.Join(kit, "files")); err != nil {
		t.Fatal(err)
	}
	_, err = LoadBook(BuiltinAgents(), path)
	expectError(t, err, "files is a link, which is not allowed at the kit's root")

	path, kit = toolKit(t)
	os.Remove(filepath.Join(kit, "tool.yaml"))
	testutil.WriteFile(t, filepath.Join(kit, "README.md"), "a kit with no descriptor\n")
	_, err = LoadBook(BuiltinAgents(), path)
	expectError(t, err, "has no descriptor; a kit has exactly one")
}

func TestAKitRootEntryThatCannotBeReadIsRefused(t *testing.T) {
	path, kit := toolKit(t)
	testutil.WriteFile(t, filepath.Join(kit, "LICENSE"), "terms\n")
	if err := os.Chmod(filepath.Join(kit, "LICENSE"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(kit, "LICENSE"), 0o644)
	_, err := LoadBook(BuiltinAgents(), path)
	expectError(t, err, "LICENSE cannot be read")
}

func TestAKitRootRefusalEscapesTheFileName(t *testing.T) {
	path, kit := toolKit(t)
	name := "evil\x1b[31m‮name"
	testutil.WriteFile(t, filepath.Join(kit, name), "x\n")
	_, err := LoadBook(BuiltinAgents(), path)
	if err == nil || strings.ContainsAny(err.Error(), "\x1b‮") {
		t.Errorf("error %q", err)
	}
}

func TestADescriptorWithAKitCommentBlockIsRefused(t *testing.T) {
	for _, line := range []string{"# kit:", "#kit:", "   # KIT :", "\t#  Kit:   {kind: mixin}"} {
		path, kit := toolKit(t)
		testutil.WriteFile(t, filepath.Join(kit, "tool.yaml"), "# syntax=docker/sandbox-kit:3\n"+line+"\n#   kind: mixin\ncapabilities: []\n")
		_, err := LoadBook(BuiltinAgents(), path)
		if err == nil || !strings.Contains(err.Error(), "tool.yaml has a `# kit:` comment line") {
			t.Errorf("%q: %v", line, err)
		}
	}
}

func TestAKitsDocumentationAndFilesAreLeftAlone(t *testing.T) {
	path, kit := toolKit(t)
	for name, content := range map[string]string{
		"README.md":               "# A tool kit\n\n```\n# syntax=docker/sandbox-kit:3\n# kit:\n#   kind: mixin\n```\n",
		"license.txt":             "terms\n",
		"NOTICE":                  "notice\n",
		"CHANGELOG.md":            "# kit: 1.0\n",
		".gitignore":              "*.tmp\n",
		"files/kit.yaml":          "# syntax=docker/sandbox-kit:3\n",
		"files/run.sh":            "#!/bin/sh\necho ok\n",
		"files/vendor/Dockerfile": "# syntax=docker/sandbox-kit:3\n# kit:\n",
	} {
		testutil.WriteFile(t, filepath.Join(kit, name), content)
	}
	large, err := os.Create(filepath.Join(kit, "files", "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	large.Truncate(300 << 20)
	large.Close()
	if err := syscall.Mkfifo(filepath.Join(kit, "files", "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(kit, "files", "locked"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(kit, "files", "locked"), 0o755)
	if _, err := LoadBook(BuiltinAgents(), path); err != nil {
		t.Fatal(err)
	}
}

func TestTheSecretsManagerCheckRefusesAUserKitWithAFileOutsideFiles(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "kits", "mine", "kit.yaml"), "capabilities: []\n")
	testutil.WriteFile(t, filepath.Join(home, "kits", "mine", "Dockerfile"), "# syntax=docker/sandbox-kit:3\n# kit:\n#   kind: mixin\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "kits = [\"./kits/mine\"]\n[aws.secrets_manager]\ndev = [\"app/db\"]\n")
	_, err := Compose(load(t, home), Selection{})
	expectError(t, err, "Dockerfile is not allowed at the kit's root")
}
