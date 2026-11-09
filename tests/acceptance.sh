#!/usr/bin/env bash
set -uo pipefail

if [ $# -lt 1 ] || [ $# -gt 3 ]; then
  echo "usage: tests/acceptance.sh <slug> [granted-aws-profile] [ungranted-aws-profile]" >&2
  echo "  checks a sandbox created with stage up; run on the host" >&2
  exit 2
fi

slug="$1"
granted="${2:-}"
ungranted="${3:-stage-acceptance-ungranted}"
home="${STAGE_HOME:-$HOME/.scoring-stage}"
task="$home/tasks/$slug/task.json"
[ -f "$task" ] || { echo "no task $slug in $home" >&2; exit 2; }
sandbox="$(jq -r .sandbox "$task")"
repo_dir="$(jq -r .repo_dir "$task")"
token_file="$home/broker/grants/$slug.token"
failures=0

box() { sbx exec -u agent -w "$repo_dir" "$sandbox" bash -c "$1"; }

check() {
  local name="$1" command="$2" output
  if output="$(box "$command" 2>&1)"; then
    echo "ok   $name"
  else
    echo "FAIL $name"
    printf '%s\n' "$output" | tail -5 | sed 's/^/     /'
    failures=$((failures + 1))
  fi
}

code() { echo "curl -s -o /dev/null -w '%{http_code}' --max-time 15 $*"; }

sha() { printf %s "$1" | shasum -a 256 | cut -d' ' -f1; }
b64() { printf %s "$1" | base64 | tr -d '\n'; }

read -r -d '' scan_script <<'SCAN'
hash_lines() {
  if command -v perl >/dev/null; then
    perl -MDigest::SHA=sha256_hex -nle 'print sha256_hex($_)'
  elif command -v python3 >/dev/null; then
    python3 -c 'import hashlib,sys
for line in sys.stdin: print(hashlib.sha256(line.rstrip("\n").encode()).hexdigest())'
  else
    while read -r word; do printf %s "$word" | sha256sum | cut -d' ' -f1; done
  fi
}
new_canary() { printf 'stagecanary%s%s%s%s%s\n' "$(od -An -tx1 -N16 /dev/urandom 2>/dev/null | tr -dc '0-9a-f')" "$RANDOM" "$RANDOM" "$RANDOM" "$$"; }
announce() { printf 'canary %s %s\n' "$1" "$(printf '%s\n' "$2" | hash_lines)"; }
env_canary="$(new_canary)" home_canary="$(new_canary)" repo_canary="$(new_canary)"
tmp_canary="$(new_canary)" bin_canary="$(new_canary)" end_canary="$(new_canary)"
home_file="$AGENT_HOME/.stage-scan-canary.$$"
repo_file="$REPO_DIR/.git/stage-scan-canary.$$"
tmp_file="$TMP_ROOT/stage-scan-canary.$$"
bin_file="$AGENT_HOME/.stage-scan-bin.$$"
export STAGE_SCAN_CANARY="$env_canary"
printf '%s\n' "$home_canary" >"$home_file" 2>/dev/null
printf '%s\n' "$repo_canary" >"$repo_file" 2>/dev/null
printf '%s\n' "$tmp_canary" >"$tmp_file" 2>/dev/null
printf 'x\0%s\n' "$bin_canary" >"$bin_file" 2>/dev/null
announce env "$env_canary"
announce home "$home_canary"
announce repo "$repo_canary"
announce tmp "$tmp_canary"
announce bin "$bin_canary"
announce end "$end_canary"
command -v strings >/dev/null || echo nostrings
{
  env
  cat /proc/[0-9]*/environ /proc/[0-9]*/cmdline 2>/dev/null | tr '\0' '\n'
  echo
  cat "$AGENT_HOME/.aws/config" 2>/dev/null
  echo
  grep -rIhoE '[A-Za-z0-9+/=_-]{20,}' --exclude-dir=ssl --exclude-dir=ca-certificates \
    "$AGENT_HOME" "$REPO_DIR" /etc "$TMP_ROOT" 2>/dev/null
  git -C "$REPO_DIR" log -p --all 2>/dev/null
  if command -v strings >/dev/null; then
    comm -23 <(find "$AGENT_HOME" "$REPO_DIR" -type f -size -64M 2>/dev/null | sort) \
      <(find "$AGENT_HOME" "$REPO_DIR" -type f -size -64M -exec grep -lI '' {} + 2>/dev/null | sort) |
      while read -r binary; do strings -n 20 "$binary"; done
  fi
  printf '%s\n' "$end_canary"
} | grep -oE '[A-Za-z0-9+/=_-]{20,}' | awk '{
  print
  n = split($0, words, /[^A-Za-z0-9_]+/)
  for (i = 1; i <= n; i++) if (length(words[i]) >= 20) print words[i]
  rest = $0
  sub(/^[\/-]+/, "", rest)
  if (rest != $0 && length(rest) >= 20) print rest
  while (match(rest, /=[^=]/)) {
    rest = substr(rest, RSTART + 1)
    sub(/^[\/-]+/, "", rest)
    if (length(rest) >= 20) print rest
  }
}' | sort -u | hash_lines | awk '{ print } END { print "scanned " NR }'
rm -f "$home_file" "$repo_file" "$tmp_file" "$bin_file" 2>/dev/null
SCAN

agent_home=/home/agent
sandbox_tmp=/tmp

sandbox_hashes() {
  box "REPO_DIR=$(printf %q "$repo_dir"); AGENT_HOME=$(printf %q "$agent_home"); TMP_ROOT=$(printf %q "$sandbox_tmp"); $scan_script"
}

scan_sandbox() {
  local output scanned label canary_hash missing=""
  output="$(sandbox_hashes 2>/dev/null)"
  hashes="$(printf '%s\n' "$output" | grep -E '^[0-9a-f]{64}$')"
  scanned="$(printf '%s\n' "$output" | sed -n 's/^scanned \([0-9][0-9]*\)$/\1/p' | tail -1)"
  for label in env home repo tmp bin end; do
    canary_hash="$(printf '%s\n' "$output" | sed -n "s/^canary $label \\([0-9a-f]\\{64\\}\\)\$/\\1/p" | head -1)"
    if [ -z "$canary_hash" ] || ! printf '%s\n' "$hashes" | grep -qxF "$canary_hash"; then
      missing="$missing $label"
    fi
  done
  scan_ok=0
  if [ -z "$scanned" ]; then
    echo "FAIL the sandbox scan for token-like strings did not finish"
  elif [ "$scanned" -eq 0 ]; then
    echo "FAIL the sandbox scan for token-like strings found nothing to hash"
  elif [ -n "$missing" ]; then
    echo "FAIL the sandbox scan for token-like strings missed its canary in:$missing"
    printf '%s\n' "$output" | grep -qx nostrings && echo "     strings is not installed in the sandbox, so binaries were not scanned"
  else
    scan_ok=1
    echo "ok   the sandbox scan hashed $scanned token-like strings and found its canary in every planted source"
  fi
  echo "note /etc is scanned but cannot hold a canary, so its coverage is unproven"
  [ "$scan_ok" = 1 ] || failures=$((failures + 1))
}

secret_absent() {
  local name="$1" secret="$2"
  if [ -z "$secret" ]; then
    echo "skip $name (no such secret on the host)"
  elif [ "$scan_ok" != 1 ]; then
    echo "FAIL $name (the sandbox scan is not trustworthy)"
    failures=$((failures + 1))
  elif ! printf '%s\n' "$hashes" | grep -qxF -e "$(sha "$secret")" -e "$(sha "$(b64 "$secret")")" \
    -e "$(sha "$(b64 "x-access-token:$secret")")" -e "$(sha "$(b64 ":$secret")")"; then
    echo "ok   $name"
  else
    echo "FAIL $name"
    failures=$((failures + 1))
  fi
}

check "the repository is the sandbox's clone at the host path" "git rev-parse HEAD >/dev/null && test \"\$PWD\" = '$repo_dir'"
check "agent CLIs run" "claude --version && codex --version && copilot --version"
check "commits carry the user's git identity" "test -n \"\$(git config --global user.name)\" && test -n \"\$(git config --global user.email)\""
check "the agent is told how the sandbox behaves" "grep -rqs 'scoring-stage sandbox' ~/.claude ~/.codex"
check "git fetch from github.com works" "git ls-remote https://github.com/octocat/Hello-World.git HEAD >/dev/null"
check "git push is refused at the proxy" "test \"\$($(code -X POST https://github.com/octocat/Hello-World.git/git-receive-pack))\" = 403"
check "a real git push fails" "! git push -q origin HEAD:refs/heads/stage-acceptance-must-not-exist 2>/dev/null"
check "unlisted hosts are refused" "test \"\$($(code https://example.org/))\" = 403"

marker="stage-acceptance-$$"
if box "touch .git/$marker && test -e .git/$marker && git config core.fsmonitor 'touch /tmp/$marker'" >/dev/null 2>&1; then
  if [ ! -e "$repo_dir/.git/$marker" ] && ! git -C "$repo_dir" config --local --get core.fsmonitor >/dev/null; then
    echo "ok   the sandbox's writes to its clone's .git do not reach the host scratch clone"
  else
    echo "FAIL the sandbox's writes to its clone's .git do not reach the host scratch clone"
    rm -f "$repo_dir/.git/$marker"
    git -C "$repo_dir" config --local --unset core.fsmonitor 2>/dev/null
    failures=$((failures + 1))
  fi
else
  echo "FAIL the sandbox's writes to its clone's .git do not reach the host scratch clone (could not write in the sandbox)"
  failures=$((failures + 1))
fi
box "rm -f .git/$marker; git config --unset core.fsmonitor" >/dev/null 2>&1
check "the task directory and its task.json are not visible to the sandbox" "test ! -e ../task.json && test ! -e ../sbxenv.yaml"
case "$(box "if test -d /run/sandbox/source; then echo present; else echo absent; fi" 2>/dev/null)" in
  present) check "the host source mount is read-only" "! touch /run/sandbox/source/.git/$marker 2>/dev/null" ;;
  absent) echo "skip the host source mount is read-only (no source mount in this sandbox)" ;;
  *) echo "FAIL could not check the host source mount"; failures=$((failures + 1)) ;;
esac

scan_sandbox
secret_absent "the host gh login token is nowhere in the sandbox" "$(gh auth token 2>/dev/null)"
for token_path in "$home"/secrets/github/*.token; do
  [ -f "$token_path" ] && secret_absent "the GitHub token $(basename "$token_path") is nowhere in the sandbox" "$(tr -d '[:space:]' <"$token_path")"
done
if [ -f "$home/secrets/github/app.json" ]; then
  secret_absent "the GitHub App refresh token is nowhere in the sandbox" "$(jq -r '.refresh_token // empty' "$home/secrets/github/app.json")"
  secret_absent "the current GitHub App access token is nowhere in the sandbox" "$(jq -r '.access_token // empty' "$home/secrets/github/app.json")"
fi
[ -s "$token_file" ] && secret_absent "the broker token is nowhere in the sandbox" "$(cat "$token_file")"

if [ -n "$granted" ]; then
  check "aws config routes $granted through the broker" \
    "grep -q 'credential_process = /home/agent/.local/bin/stage-aws-credentials $granted' ~/.aws/config"
  check "the broker answers for the granted profile (credentials, or a host-login error)" \
    "out=\$(/home/agent/.local/bin/stage-aws-credentials $granted 2>&1); echo \"\$out\" | grep -qE 'AccessKeyId|refresh the profile.s login on the host'"
  check "the broker refuses an ungranted profile" \
    "out=\$(/home/agent/.local/bin/stage-aws-credentials $ungranted 2>&1); echo \"\$out\" | grep -q 'not granted to this sandbox'"
fi

echo "failures: $failures"
exit "$failures"
