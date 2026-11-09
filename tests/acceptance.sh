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

check "the repository is the sandbox's clone at the host path" "git rev-parse HEAD >/dev/null && test \"\$PWD\" = '$repo_dir'"
check "agent CLIs run" "claude --version && codex --version && copilot --version"
check "commits carry the user's git identity" "test -n \"\$(git config --global user.name)\" && test -n \"\$(git config --global user.email)\""
check "the agent is told how the sandbox behaves" "grep -rqs 'scoring-stage sandbox' ~/.claude ~/.codex"
check "git fetch from github.com works" "git ls-remote https://github.com/octocat/Hello-World.git HEAD >/dev/null"
check "git push is refused at the proxy" "test \"\$($(code -X POST https://github.com/octocat/Hello-World.git/git-receive-pack))\" = 403"
check "a real git push fails" "! git push -q origin HEAD:refs/heads/stage-acceptance-must-not-exist 2>/dev/null"
check "unlisted hosts are refused" "test \"\$($(code https://example.org/))\" = 403"
check "GH_TOKEN is a placeholder, not the host token" "test \"\${GH_TOKEN:-}\" != '$(gh auth token 2>/dev/null)'"

marker="stage-acceptance-$$"
box "touch .git/$marker; git config core.fsmonitor 'touch /tmp/$marker'" >/dev/null 2>&1
if [ ! -e "$repo_dir/.git/$marker" ] && ! git -C "$repo_dir" config --local --get core.fsmonitor >/dev/null; then
  echo "ok   the sandbox cannot write the host scratch clone's .git"
else
  echo "FAIL the sandbox cannot write the host scratch clone's .git"
  rm -f "$repo_dir/.git/$marker"
  git -C "$repo_dir" config --local --unset core.fsmonitor 2>/dev/null
  failures=$((failures + 1))
fi
box "rm -f .git/$marker; git config --unset core.fsmonitor" >/dev/null 2>&1

if [ -n "$granted" ]; then
  check "aws config routes $granted through the broker" \
    "grep -q 'credential_process = /home/agent/.local/bin/stage-aws-credentials $granted' ~/.aws/config"
  check "the broker answers for the granted profile (credentials, or a host-login error)" \
    "out=\$(/home/agent/.local/bin/stage-aws-credentials $granted 2>&1); echo \"\$out\" | grep -qE 'AccessKeyId|refresh the profile.s login on the host'"
  check "the broker refuses an ungranted profile" \
    "out=\$(/home/agent/.local/bin/stage-aws-credentials $ungranted 2>&1); echo \"\$out\" | grep -q 'not granted to this sandbox'"
  if [ -s "$token_file" ]; then
    check "the broker token is nowhere in the sandbox" \
      "! (env; cat ~/.aws/config) | grep -qF '$(cat "$token_file")' && ! grep -rqsF '$(cat "$token_file")' /home/agent /etc /tmp"
  fi
fi

echo "failures: $failures"
exit "$failures"
