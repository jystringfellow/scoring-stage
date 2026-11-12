package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func RunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func WriteFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func ReadFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

type Remote struct {
	Root     string
	Bare     string
	Checkout string
}

// GitHubRemote makes https://github.com/acme/api.git resolve to a local bare
// repository and returns a checkout of it whose origin is git@github.com:acme/api.git.
func GitHubRemote(t *testing.T) Remote {
	t.Helper()
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		t.Setenv(key, value)
	}
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	RunGit(t, root, "init", "-q", "--bare", "-b", "main", bare)
	RunGit(t, root, "init", "-q", "-b", "main", seed)
	WriteFile(t, filepath.Join(seed, "README.md"), "api\n")
	RunGit(t, seed, "add", ".")
	RunGit(t, seed, "commit", "-q", "-m", "first")
	RunGit(t, seed, "push", "-q", bare, "main")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+bare+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/api.git")
	checkout := filepath.Join(root, "Acme.Api")
	RunGit(t, root, "clone", "-q", "https://github.com/acme/api.git", checkout)
	RunGit(t, checkout, "remote", "set-url", "origin", "git@github.com:acme/api.git")
	return Remote{Root: root, Bare: bare, Checkout: checkout}
}

const fakeSbx = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_SBX_LOG"
if [ "$1" = exec ]; then
  case " $* " in
    *"tar -cf -"*) if [ -f "$FAKE_SBX_STATE/takes.hang" ]; then sleep 30; fi; if [ -f "$FAKE_SBX_STATE/takes.fail" ]; then cat "$FAKE_SBX_STATE/takes.fail" >&2; exit 1; fi; [ -f "$FAKE_SBX_STATE/takes.tar" ] && cat "$FAKE_SBX_STATE/takes.tar" ;;
    *" -i "*) for last; do :; done; if [ -f "$FAKE_SBX_STATE/feed.fail" ]; then cat > /dev/null; cat "$FAKE_SBX_STATE/feed.fail" >&2; exit 1; fi; mkdir -p "$FAKE_SBX_STATE/box"; cat > "$FAKE_SBX_STATE/box/$(basename "$last")" ;;
    *" cat "*) for last; do :; done; file="$FAKE_SBX_STATE/box/$(basename "$last")"; [ -s "$file" ] || { echo "cat: No such file or directory" >&2; exit 1; }; cat "$file" ;;
  esac
  exit 0
fi
case "$1 $2" in
  "version "*) echo "sbx version ${FAKE_SBX_VERSION:-v0.51.0} deadbeef" ;;
  "policy ls") cat "$FAKE_SBX_POLICY" ;;
  "secret ls") cat "$FAKE_SBX_SECRETS" ;;
  "ls -q") cat "$FAKE_SBX_SANDBOXES" ;;
  "ls ") printf 'SANDBOX   STATUS\n'; sed 's/$/   running/' "$FAKE_SBX_SANDBOXES" ;;
esac
exit 0
`

type FakeSbx struct {
	t     *testing.T
	State string
}

// NewFakeSbx puts an sbx on PATH that logs its arguments and answers
// version, policy ls and ls from files under State.
func NewFakeSbx(t *testing.T) *FakeSbx {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	script := WriteFile(t, filepath.Join(bin, "sbx"), fakeSbx)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "sbx-state")
	WriteFile(t, filepath.Join(state, "calls.log"), "")
	WriteFile(t, filepath.Join(state, "policy.json"), `{"rules": []}`)
	WriteFile(t, filepath.Join(state, "sandboxes"), "")
	WriteFile(t, filepath.Join(state, "secrets.json"), `{"secrets": []}`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SBX_LOG", filepath.Join(state, "calls.log"))
	t.Setenv("FAKE_SBX_POLICY", filepath.Join(state, "policy.json"))
	t.Setenv("FAKE_SBX_SANDBOXES", filepath.Join(state, "sandboxes"))
	t.Setenv("FAKE_SBX_SECRETS", filepath.Join(state, "secrets.json"))
	t.Setenv("FAKE_SBX_STATE", state)
	return &FakeSbx{t: t, State: state}
}

func (f *FakeSbx) Calls() []string {
	text := strings.TrimSuffix(ReadFile(f.t, filepath.Join(f.State, "calls.log")), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func (f *FakeSbx) LastCall() string {
	calls := f.Calls()
	if len(calls) == 0 {
		return ""
	}
	return calls[len(calls)-1]
}

func (f *FakeSbx) SetPolicy(json string) {
	WriteFile(f.t, filepath.Join(f.State, "policy.json"), json)
}

func (f *FakeSbx) SetSecrets(json string) {
	WriteFile(f.t, filepath.Join(f.State, "secrets.json"), json)
}

func (f *FakeSbx) SetSandboxes(lines string) {
	WriteFile(f.t, filepath.Join(f.State, "sandboxes"), lines)
}

// SetTakes is the tar stream the fake sandbox answers a take with.
func (f *FakeSbx) SetTakes(archive string) {
	WriteFile(f.t, filepath.Join(f.State, "takes.tar"), archive)
}

// FailTakes makes the fake sandbox fail a take with this message.
func (f *FakeSbx) FailTakes(message string) {
	WriteFile(f.t, filepath.Join(f.State, "takes.fail"), message+"\n")
}

// HangTakes makes the fake sandbox go quiet when asked for a take.
func (f *FakeSbx) HangTakes() {
	WriteFile(f.t, filepath.Join(f.State, "takes.hang"), "")
}

func (f *FakeSbx) FailFeeds(message string) {
	WriteFile(f.t, filepath.Join(f.State, "feed.fail"), message+"\n")
}

// BoxFile is the fake sandbox's copy of the in-box file with this base name:
// exec -i writes it and exec cat reads it.
func (f *FakeSbx) BoxFile(name string) string {
	return filepath.Join(f.State, "box", name)
}
