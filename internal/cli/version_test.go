package cli

import (
	"runtime/debug"
	"testing"
)

func buildInfo(version string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/jystringfellow/scoring-stage", Version: version}}, true
	}
}

func noBuildInfo() (*debug.BuildInfo, bool) { return nil, false }

func TestVersionPrefersTheLinkedVersion(t *testing.T) {
	if got := versionOf("v0.0.1", buildInfo("v0.0.0-20261101-abcdef")); got != "v0.0.1" {
		t.Fatalf("got %q", got)
	}
}

func TestVersionFallsBackToTheModuleVersionForGoInstall(t *testing.T) {
	if got := versionOf("dev", buildInfo("v0.0.2")); got != "v0.0.2" {
		t.Fatalf("got %q", got)
	}
}

func TestVersionIsDevForALocalBuild(t *testing.T) {
	for _, read := range []func() (*debug.BuildInfo, bool){buildInfo("(devel)"), buildInfo(""), noBuildInfo} {
		if got := versionOf("dev", read); got != "dev" {
			t.Fatalf("got %q", got)
		}
	}
}

func TestVersionCommandAndFlagPrintTheVersion(t *testing.T) {
	h := setup(t)
	original := Version
	Version = "v9.8.7"
	t.Cleanup(func() { Version = original })
	for _, arg := range []string{"version", "--version"} {
		if code := h.stage(arg); code != 0 {
			t.Fatalf("%s exited %d: %s", arg, code, h.stderr.String())
		}
		if got := h.stdout.String(); got != "stage v9.8.7\n" {
			t.Fatalf("%s printed %q", arg, got)
		}
	}
}
