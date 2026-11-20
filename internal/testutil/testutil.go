package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
if [ "${FAKE_SBX_NOISE:-}" = 1 ]; then
  echo child-noise >&2
  case "$1 $2" in "env create"|"env rm") echo child-noise; cat > /dev/null ;; esac
fi
if [ "$1" = exec ]; then
  case " $* " in
    *"tar -cf -"*) if [ -f "$FAKE_SBX_STATE/takes.hang" ]; then sleep 30; fi; if [ -f "$FAKE_SBX_STATE/takes.fail" ]; then cat "$FAKE_SBX_STATE/takes.fail" >&2; exit 1; fi; [ -f "$FAKE_SBX_STATE/takes.tar" ] && cat "$FAKE_SBX_STATE/takes.tar" ;;
    *" -i "*" sh -s "*) if [ ! -f "$FAKE_SBX_STATE/exec.run" ]; then cat > /dev/null; exit 0; fi
      shift; dir=
      while :; do
        case "$1" in -i|-t|-it) shift ;; -u) shift 2 ;; -w) dir=$2; shift 2 ;; *) break ;; esac
      done
      sandbox=$1; shift
      printf '%s\n' "$sandbox" >> "$FAKE_SBX_STATE/reads.log"
      if [ -f "$FAKE_SBX_STATE/hang.$sandbox" ]; then cat > /dev/null; sleep 30; fi
      if [ -f "$FAKE_SBX_STATE/flood.$sandbox" ]; then cat > /dev/null; head -c 2000000 /dev/zero | tr '\0' x; exit 0; fi
      if [ -f "$FAKE_SBX_STATE/fail.$sandbox" ]; then cat > /dev/null; cat "$FAKE_SBX_STATE/fail.$sandbox" >&2; exit 1; fi
      script=$(cat)
      FAKE_READ_BOUNDARY=$(printf '%s\n' "$script" | sed -n 's/^b=//p')
      export FAKE_READ_BOUNDARY
      if [ -f "$FAKE_SBX_STATE/reply.$sandbox" ]; then sed "s/@REAL_BOUNDARY@/$FAKE_READ_BOUNDARY/g" "$FAKE_SBX_STATE/reply.$sandbox"; exit 0; fi
      if [ -f "$FAKE_SBX_STATE/reads.slow" ]; then
        mkdir -p "$FAKE_SBX_STATE/active"; : > "$FAKE_SBX_STATE/active/$$"
        ls "$FAKE_SBX_STATE/active" | wc -l >> "$FAKE_SBX_STATE/concurrency.log"
        sleep 1; rm -f "$FAKE_SBX_STATE/active/$$"
      fi
      if [ -d "$dir" ]; then cd "$dir" || exit 125; fi
      mkdir -p "$FAKE_SBX_STATE/agent-path" "$FAKE_SBX_STATE/agent-home"
      HOME="$FAKE_SBX_STATE/agent-home"
      kept=; IFS=:
      for entry in $PATH; do [ -x "$entry/km" ] || kept="$kept${kept:+:}$entry"; done
      unset IFS
      PATH="$FAKE_SBX_STATE/agent-path:$kept"
      export HOME PATH
      if [ -f "$FAKE_SBX_STATE/read.path" ]; then PATH=$(cat "$FAKE_SBX_STATE/read.path"); export PATH; fi
      printf '%s\n' "$script" | "$@"; exit $? ;;
    *" -i "*) for last; do :; done; if [ -f "$FAKE_SBX_STATE/feed.hang" ]; then sleep 30; fi; if [ -f "$FAKE_SBX_STATE/feed.ignore" ]; then exit 0; fi; if [ -f "$FAKE_SBX_STATE/feed.fail" ]; then cat > /dev/null; cat "$FAKE_SBX_STATE/feed.fail" >&2; exit 1; fi; mkdir -p "$FAKE_SBX_STATE/box"; cat > "$FAKE_SBX_STATE/box/$(basename "$last")"; case " $* " in *"tar -xf -"*) echo stage-gear-unpacked ;; esac ;;
    *" cat "*) for last; do :; done; file="$FAKE_SBX_STATE/box/$(basename "$last")"; [ -s "$file" ] || { echo "cat: No such file or directory" >&2; exit 1; }; cat "$file" ;;
    *) if [ -f "$FAKE_SBX_STATE/exec.run" ]; then
      shift
      while :; do
        case "$1" in -i|-t|-it) shift ;; -u|-w) shift 2 ;; *) break ;; esac
      done
      sandbox=$1; shift
      printf 'session-start %s %s\n' "$$" "$sandbox" >> "$FAKE_SBX_STATE/sessions.log"
      "$@" &
      child=$!
      trap 'kill -TERM "$child" 2>/dev/null; wait "$child" 2>/dev/null; echo "session-end stopped" >> "$FAKE_SBX_STATE/sessions.log"; exit 143' TERM
      wait "$child"; code=$?
      printf 'session-end %s\n' "$code" >> "$FAKE_SBX_STATE/sessions.log"
      exit "$code"
    fi ;;

  esac
  exit 0
fi
case "$1 $2" in
  "version "*) echo "sbx version ${FAKE_SBX_VERSION:-v0.51.0} deadbeef" ;;
  "policy ls") cat "$FAKE_SBX_POLICY" ;;
  "secret ls") cat "$FAKE_SBX_SECRETS" ;;
  "ls -q") cat "$FAKE_SBX_SANDBOXES" "$FAKE_SBX_STATE/stopped" ;;
  "ls ") printf 'SANDBOX   STATUS\n'; sed 's/$/   running/' "$FAKE_SBX_SANDBOXES"; sed 's/$/   stopped/' "$FAKE_SBX_STATE/stopped" ;;
  "ls --json")
    if [ -f "$FAKE_SBX_STATE/listing.json" ]; then cat "$FAKE_SBX_STATE/listing.json"; exit 0; fi
    sep=
    printf '['
    for status in running stopped; do
      file="$FAKE_SBX_SANDBOXES"; [ "$status" = stopped ] && file="$FAKE_SBX_STATE/stopped"
      while read -r name; do
        [ -n "$name" ] || continue
        printf '%s{"name":"%s","status":"%s","created_at":"2026-11-19T10:00:00Z","last_used_at":"2026-11-19T11:30:00.25Z"}' "$sep" "$name" "$status"
        sep=,
      done < "$file"
    done
    printf ']\n' ;;
esac
exit 0
`

const fakeCurl = `#!/bin/sh
for url; do :; done
rest=${url#http://127.0.0.1:}
port=${rest%%/*}
dir="$FAKE_SBX_STATE/cao/$port"
[ -d "$dir" ] || { echo "curl: (7) Failed to connect" >&2; exit 7; }
[ -f "$dir/hang" ] && { sleep 30; exit 28; }
file="$dir/$(printf '%s' "/${rest#*/}" | tr '/' '_')"
[ -f "$file" ] || exit 22
if grep -q @REAL_BOUNDARY@ "$file"; then sed "s/@REAL_BOUNDARY@/$FAKE_READ_BOUNDARY/g" "$file"; else cat "$file"; fi
`

const fakeTmux = `#!/bin/sh
for target; do :; done
file="$FAKE_SBX_STATE/panes/$target"
[ -f "$file" ] || exit 1
cat "$file"
`

const fakeTimeout = `#!/bin/sh
[ "$1" = -k ] && shift 2
limit=$1
shift
expired="$FAKE_SBX_STATE/timeout-expired.$$"
"$@" &
child=$!
( sleep "$limit"; : > "$expired"; kill -TERM "$child" 2>/dev/null ) >/dev/null 2>&1 &
watcher=$!
wait "$child"
code=$?
kill "$watcher" 2>/dev/null
if [ -f "$expired" ]; then rm -f "$expired"; exit 124; fi
exit "$code"
`

type FakeSbx struct {
	t     *testing.T
	State string
	Bin   string
}

// NewFakeSbx puts an sbx on PATH that logs its arguments and answers
// version, policy ls and ls from files under State.
func NewFakeSbx(t *testing.T) *FakeSbx {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	for name, content := range map[string]string{"sbx": fakeSbx, "curl": fakeCurl, "tmux": fakeTmux, "timeout": fakeTimeout} {
		script := WriteFile(t, filepath.Join(bin, name), content)
		if err := os.Chmod(script, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(root, "sbx-state")
	WriteFile(t, filepath.Join(state, "calls.log"), "")
	WriteFile(t, filepath.Join(state, "policy.json"), `{"rules": []}`)
	WriteFile(t, filepath.Join(state, "sandboxes"), "")
	WriteFile(t, filepath.Join(state, "stopped"), "")
	WriteFile(t, filepath.Join(state, "secrets.json"), `{"secrets": []}`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SBX_LOG", filepath.Join(state, "calls.log"))
	t.Setenv("FAKE_SBX_POLICY", filepath.Join(state, "policy.json"))
	t.Setenv("FAKE_SBX_SANDBOXES", filepath.Join(state, "sandboxes"))
	t.Setenv("FAKE_SBX_SECRETS", filepath.Join(state, "secrets.json"))
	t.Setenv("FAKE_SBX_STATE", state)
	return &FakeSbx{t: t, State: state, Bin: bin}
}

func (f *FakeSbx) SetCAO(port, path, body string) {
	WriteFile(f.t, filepath.Join(f.State, "cao", port, strings.ReplaceAll(path, "/", "_")), body)
}

func (f *FakeSbx) HangCAO(port string) {
	WriteFile(f.t, filepath.Join(f.State, "cao", port, "hang"), "")
}

func (f *FakeSbx) SetPane(target, text string) {
	WriteFile(f.t, filepath.Join(f.State, "panes", target), text)
}

func (f *FakeSbx) HangReads(sandbox string) {
	WriteFile(f.t, filepath.Join(f.State, "hang."+sandbox), "")
}

func (f *FakeSbx) FloodReads(sandbox string) {
	WriteFile(f.t, filepath.Join(f.State, "flood."+sandbox), "")
}

func (f *FakeSbx) AgentHome() string {
	return filepath.Join(f.State, "agent-home")
}

func (f *FakeSbx) AgentPath() string {
	return filepath.Join(f.State, "agent-path")
}

func (f *FakeSbx) FailReads(sandbox, stderr string) {
	WriteFile(f.t, filepath.Join(f.State, "fail."+sandbox), stderr)
}

func (f *FakeSbx) ReplyToReads(sandbox, reply string) {
	WriteFile(f.t, filepath.Join(f.State, "reply."+sandbox), reply)
}

func (f *FakeSbx) SlowReads() {
	WriteFile(f.t, filepath.Join(f.State, "reads.slow"), "")
}

func (f *FakeSbx) ReadConcurrency() []int {
	var seen []int
	for _, line := range strings.Fields(ReadFile(f.t, filepath.Join(f.State, "concurrency.log"))) {
		n, err := strconv.Atoi(line)
		if err != nil {
			f.t.Fatalf("concurrency.log has %q", line)
		}
		seen = append(seen, n)
	}
	return seen
}

func (f *FakeSbx) SetReadPath(path string) {
	WriteFile(f.t, filepath.Join(f.State, "read.path"), path)
}

func (f *FakeSbx) Noisy() { f.t.Setenv("FAKE_SBX_NOISE", "1") }

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

func (f *FakeSbx) SetStopped(lines string) {
	WriteFile(f.t, filepath.Join(f.State, "stopped"), lines)
}

func (f *FakeSbx) SetListing(json string) {
	WriteFile(f.t, filepath.Join(f.State, "listing.json"), json)
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

func (f *FakeSbx) IgnoreFeeds() {
	WriteFile(f.t, filepath.Join(f.State, "feed.ignore"), "")
}

func (f *FakeSbx) HangFeeds() {
	WriteFile(f.t, filepath.Join(f.State, "feed.hang"), "")
}

// BoxFile is the fake sandbox's copy of the in-box file with this base name:
// exec -i writes it and exec cat reads it.
func (f *FakeSbx) BoxFile(name string) string {
	return filepath.Join(f.State, "box", name)
}

func (f *FakeSbx) RunExecs() {
	WriteFile(f.t, filepath.Join(f.State, "exec.run"), "")
	WriteFile(f.t, filepath.Join(f.State, "sessions.log"), "")
}

func (f *FakeSbx) Sessions() []string {
	return strings.FieldsFunc(ReadFile(f.t, filepath.Join(f.State, "sessions.log")), func(r rune) bool { return r == '\n' })
}
