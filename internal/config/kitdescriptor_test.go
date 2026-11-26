package config

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"

	"github.com/jystringfellow/scoring-stage/internal/testutil"
)

func utf16LittleEndian(text string) string {
	var out []byte
	out = append(out, 0xff, 0xfe)
	for _, unit := range utf16.Encode([]rune(text)) {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return string(out)
}

func bookWithKitFile(t *testing.T, name, content string) string {
	t.Helper()
	path := book(t, t.TempDir(), "org", "kits = [\"./kits/tool\"]\n")
	testutil.WriteFile(t, filepath.Join(path, "kits", "tool", "kit.yaml"), "capabilities: []\n")
	if name != "" {
		testutil.WriteFile(t, filepath.Join(path, "kits", "tool", name), content)
	}
	return path
}

func TestAKitFileThatNamesTheKitFrontendIsADescriptorStageCannotRead(t *testing.T) {
	comment := "# kit:\n#   schemaVersion: \"3\"\n#   kind: mixin\n#   capabilities:\n#     - type: com.docker.sandbox/credential@1\nFROM scratch\n"
	for name, content := range map[string]string{
		"Dockerfile":         "# syntax=docker/sandbox-kit:3\n" + comment,
		"tool.dockerfile":    "#syntax=docker/sandbox-kit:3\n" + comment,
		"Containerfile":      "# SYNTAX=docker/sandbox-kit:3\n" + comment,
		"build":              "# syntax = docker/sandbox-kit:3\n" + comment,
		"bom.dockerfile":     "\xef\xbb\xbf# syntax=docker/sandbox-kit:3\n" + comment,
		"cr.dockerfile":      strings.ReplaceAll("# syntax=docker/sandbox-kit:3\n"+comment, "\n", "\r"),
		"crlf.dockerfile":    strings.ReplaceAll("# syntax=docker/sandbox-kit:3\n"+comment, "\n", "\r\n"),
		"escape.dockerfile":  "# escape=`\n# check=skip=all\n# syntax=docker.io/docker/sandbox-kit:3.1@sha256:abc\n" + comment,
		"slashes.dockerfile": "// syntax=docker/sandbox-kit:3\n" + comment,
		"utf16.dockerfile":   utf16LittleEndian("# syntax=docker/sandbox-kit:3\n" + comment),
		"files/kit.yaml":     "# syntax=docker/sandbox-kit:3\nschemaVersion: \"3\"\n",
	} {
		_, err := LoadBook(BuiltinAgents(), bookWithKitFile(t, name, content))
		if err == nil || !strings.Contains(err.Error(), filepath.FromSlash(name)+" names the sandbox-kit frontend") ||
			!strings.Contains(err.Error(), "a kit descriptor stage cannot read; write the kit's descriptor as YAML") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestALinkToAFileNamingTheKitFrontendIsReadThroughTheLink(t *testing.T) {
	for _, target := range []string{"outside", "files/inside"} {
		path := bookWithKitFile(t, "", "")
		kit := filepath.Join(path, "kits", "tool")
		destination := filepath.Join(path, "shared", "outside")
		if target == "files/inside" {
			destination = filepath.Join(kit, "files", "inside")
		}
		testutil.WriteFile(t, destination, "# syntax=docker/sandbox-kit:3\n# kit:\n#   kind: mixin\n")
		if err := os.Symlink(destination, filepath.Join(kit, "Dockerfile")); err != nil {
			t.Fatal(err)
		}
		_, err := LoadBook(BuiltinAgents(), path)
		if err == nil || !strings.Contains(err.Error(), "Dockerfile names the sandbox-kit frontend") {
			t.Errorf("link to %s: %v", target, err)
		}
	}
}

func TestKitFilesThatAreNotDescriptorsAreLeftAlone(t *testing.T) {
	for name, content := range map[string]string{
		"README.md":       "# A tool kit\n\nAn example descriptor:\n\n```\n# syntax=docker/sandbox-kit:3\n# kit:\n#   kind: mixin\n```\n",
		"tool.dockerfile": "# syntax=docker/dockerfile:1\nFROM scratch\nCOPY . /\n",
		"notes.txt":       "# kit: notes for maintainers\n# syntax=docker/sandbox-kit:3 is the frontend\n",
		"late.dockerfile": "FROM scratch\n# syntax=docker/sandbox-kit:3\n",
		"files/run.sh":    "#!/bin/sh\necho ok\n",
	} {
		if _, err := LoadBook(BuiltinAgents(), bookWithKitFile(t, name, content)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAKitIsCheckedWithoutReadingWholeFilesOrBlockingOnSpecialOnes(t *testing.T) {
	path := bookWithKitFile(t, "", "")
	kit := filepath.Join(path, "kits", "tool")
	large, err := os.Create(filepath.Join(kit, "files", "data.bin"))
	if err != nil {
		os.MkdirAll(filepath.Join(kit, "files"), 0o755)
		large, err = os.Create(filepath.Join(kit, "files", "data.bin"))
		if err != nil {
			t.Fatal(err)
		}
	}
	large.WriteString("plain data\n")
	if err := large.Truncate(300 << 20); err != nil {
		t.Fatal(err)
	}
	large.Close()
	if err := syscall.Mkfifo(filepath.Join(kit, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBook(BuiltinAgents(), path); err != nil {
		t.Fatal(err)
	}
}

func TestTheSecretsManagerCheckRefusesAUserKitWithADescriptorStageCannotRead(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	testutil.WriteFile(t, filepath.Join(home, "kits", "mine", "kit.yaml"), "capabilities: []\n")
	testutil.WriteFile(t, filepath.Join(home, "kits", "mine", "Dockerfile"), "# syntax=docker/sandbox-kit:3\n# kit:\n#   kind: mixin\n")
	testutil.WriteFile(t, filepath.Join(home, "config.toml"), "kits = [\"./kits/mine\"]\n[aws.secrets_manager]\ndev = [\"app/db\"]\n")
	_, err := Compose(load(t, home), Selection{})
	expectError(t, err, "Dockerfile names the sandbox-kit frontend")
}
