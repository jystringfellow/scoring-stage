# Verification

What has been checked against a live sbx or GitHub, when, on what, and how shared state was put back.
`go test ./...` runs every command against fakes (an `sbx` script on PATH, a local repository standing in for
github.com, a simulated GitHub API and device flow, and a stub `aws` CLI behind the broker), so it shows what stage
asks sbx, git and GitHub to do, not what they then do. Those runs are not recorded here.

Entries are added at the end and not rewritten. When a later run contradicts an entry, add a new entry that says so.

## Not yet run live

- Parts of the acceptance secret scan that a canary cannot prove. `/etc` cannot hold a canary, so its coverage is
  unproven. Matching is by whole token-like word, so a base64 token sitting at an offset inside a longer blob is
  not found. The scan sees only what the sandbox's own user can read, not root's or the proxy's processes.
- `tests/acceptance.sh` has no CI; each run is recorded here.
- The GitHub REST write refusals in the stage kit beyond the three requests checked on 2026-11-10.
- `fine-grained` token checks and `stage token set` against GitHub itself.
- `app` mode's refresh in a running sandbox: whether sbx re-runs a secret's command on its `refresh` interval, so a
  sandbox that stays up gets a new access token before the old one expires. On 2026-11-10 only a sandbox start was
  seen to refresh, after expiry. sbx stops a sandbox 30 seconds after its last session ends, so a test needs a
  session held open across the expiry. Also whether a refresh rotates the refresh token, and whether
  `/user/installations` reports account permissions (the app checked on 2026-11-10 had none).
- `stage ship --pr`.
- The release workflow's refusals: a tag that is not `vX.Y.Z[-suffix]`, and a tagged commit that is not on `main`.
- Managed settings, beyond unit tests.
- Takes: `stage down` keeping takes before a real removal, a repository's takes approval, a take near the size,
  entry or stream limit, a sandbox that goes quiet for the 10-minute limit, and overlapping paths on the sandbox's
  tar. `stage take` was checked on 2026-11-10.
- Book and repository kit refusals: a changed kit asking again, a book claiming a reserved service, and a
  published kit in a book or repository config.
- Whether `sbx env create --force` prints the plan it approves.
- Where sbx keeps `credentials.yaml`, beyond `~/.config/sbx/credentials.yaml` on macOS. `stage setup`'s bindings
  audit also looks under `$XDG_CONFIG_HOME` and `%APPDATA%`, inferred from strings in the sbx binary.
- sbx's kit loader: which files it reads from a kit directory, and whether it matches keys case-insensitively.
  stage checks `.yaml`, `.yml` and `.json` files at any depth except under `files/`, and refuses keys that differ
  from `capabilities`, `extends`, `type`, `config`, `service`, `inject` or `domain` only in case.

## 2026-11-08 to 2026-11-09: sbx behaviours stage relies on

- **sbx:** 0.51.0.
- **stage:** early `main`, from 63e672e to the push-deny change 07ba0d9 and the broker-port change 3edda1c. The
  exact commit for each check was not recorded.
- **Credentials and restoration:** not recorded. These checks were collected in `docs/design.md` before this file
  existed.

Observed:

- `network-policy@2` bounded allow and deny entries are enforced and refused with HTTP 403. A kit's deny wins over a
  global allow. IP globs such as `1.1.*.*:443` work.
- Bounded entries cannot match query strings. Refusing `POST /**/git-receive-pack` is what stops a push; the
  `info/refs?service=git-receive-pack` discovery request is left to the origin, which answers 403 for anonymous
  callers.
- A bounded entry makes its hosts require HTTP inspection, and sbx fails closed on connections it cannot inspect
  ("HTTP policy requires L7 enforcement, which is unavailable for transparent connections"). On `**` the push deny
  refused every non-HTTP connection, such as SQL Server on 1433; limited to `**:443` and `**:80`, those connections
  work. Port suffixes on bounded deny hosts are honored.
- `host-port@1` is not supported; `host-mount@1` is, and is shared by every sandbox composing the same kit.
- sbx names `host.docker.internal` "localhost" when matching policy and credentials. With the kit allowing and
  injecting only on `localhost:9911`, the broker answers with its token added by the proxy, and other host ports are
  refused with 403. Injecting on `host.docker.internal` reaches the broker without the header.
- `sbx secret set <service> --sandbox <name>` accepts services that are not built in, which is how `stage-broker`
  and book services get values.
- `sbx env create --force --no-input` creates a sandbox, its secrets and its bindings without prompting.
  `sbx env rm` removes the sandbox and its scoped secrets.
- Docker's published agent mixins use `agent-context@1`, which cannot compose with `@2`. The `stage` kit declares
  both as optional with the same content so either selection composes.
- Typing a built-in agent name (`claude`) selects Docker's v2 kits, which cannot compose with v3 mixins.
- `kit.allowedSources` defaults to Docker Hub and dhi.io. Kits from another registry need it widened, and signed
  kits must be published images: local and git sources cannot be signed.

## 2026-11-08: first acceptance run

- **sbx:** 0.51.0.
- **stage:** the first version of `tests/acceptance.sh`, against a sandbox made with `stage up`. The commit was not
  recorded.
- **Credentials:** not recorded; probably the user's real agent logins and gh login.
- **Restoration:** `stage down`. A global `github` sbx secret and a `localhost` stage-broker binding from this
  period were left behind; remove them with `sbx secret rm github` and a hand edit of `credentials.yaml`.

Passed: the repository path, that the claude, codex and copilot CLIs start, the git identity and agent guidance,
fetching from github.com, and that `git push` and an unlisted host are refused.

Does not count: the secret checks from that run. They could pass without looking at anything, and were rewritten
on 2026-11-09 (see Not yet run live).

## 2026-11-09: credential injection, plans and allowed sources

- **sbx:** 0.51.0, driven directly with hand-written env files and local kits, not through stage.
- **Credentials:** dummy only. Each probed secret was a command printing a fixed dummy string; no real gh, copilot
  or agent secret was used.
- **Shared state:** `~/.config/sbx/credentials.yaml` was copied before the first probe (sha256
  `4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`) and put back afterwards; the restored file had
  the same sha256. Every probe sandbox was removed.

Observed:

- Binding domains do not bound injection. A binding only authorises a service name; a kit's `credential@1` inject
  domain decides where the secret goes. A local kit declaring `service: copilot` with an inject domain of another
  host got the probe's dummy copilot secret injected there with no binding in the env file, because the global
  `credentials.yaml` already bound copilot. A service with no binding anywhere is refused ("no binding authorizes
  X").
- `sbx env plan` runs no secret commands and merges no bindings: a secret command that touches a file did not run.
- `kit.allowedSources` refuses a kit from a registry it does not list even under `--force`, before any binding
  merges.
- `sbx env rm --prune-bindings` removes the whole shared service entry from `credentials.yaml`, not just the
  sandbox's use of it. `stage down` never passes it.

## 2026-11-09: release workflow dry run

- **Tag:** `v0.0.0-rc.1` on `main` at 5944f17, pushed by the maintainer.
- **Credentials:** the workflow's own `GITHUB_TOKEN` and OIDC token; nothing else.
- **Restoration:** the prerelease and tag were deleted afterwards (`gh release delete v0.0.0-rc.1 --cleanup-tag`).

Passed:

- The build and release jobs both succeeded and published a prerelease with four archives and `SHA256SUMS`.
- The darwin/arm64 archive matched `SHA256SUMS`, and its `stage version` printed `v0.0.0-rc.1`.
- The README's `gh attestation verify` with `--signer-workflow` and `--source-ref refs/tags/v0.0.0-rc.1` passed, and
  the certificate named the tag and 5944f17.
- The same command with `--source-ref refs/tags/v9.9.9` failed.

Not exercised: the refusal of a malformed tag and of a tag not on `main`.

## 2026-11-09: acceptance on a private repository and on kapellmeister

- **sbx:** 0.51.0.
- **stage:** `main` at a452ee8, built to a temporary binary; `tests/acceptance.sh` from 9728e55, which fixes the
  scan bug below.
- **Credentials:** real. `[github] mode = "gh"` (the maintainer's gh login), the maintainer's agent logins, a saved
  agy login through `copy_logins`, a token from a private book's secret command, and one AWS profile
  through the broker. The leftover global `github` secret from 2026-11-08 was removed with `sbx secret rm github -f`
  before the run, as `stage up` requires.
- **Global secrets:** `sbx secret rm github -f`, with no scope flag, printed "Removed secret github (global)", so it
  removes the global secret by default. Afterwards the `stage up --plan` bindings audit, which reads
  `sbx secret ls --global --json`, marked `anthropic` and `openai` as "global secret set" and `github` as not, which
  matches `sbx secret ls --global`.
- **Shared state:** `~/.config/sbx/credentials.yaml` was copied before the run (sha256
  `4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`) and was byte-for-byte the same after the
  first sandbox's `stage down`, with the kapellmeister sandbox still up; every binding the runs needed already
  existed. The stale `localhost` entry under `stage-broker` remains, as the `stage setup` audit
  reports. The first run on this version cleared old-format
  approvals, and both repository configs were approved with `--approve-repo-config` after review.

**A private repository**, with a repository config naming three private books and one repository kit; agents
claude, codex and copilot; one AWS profile granted. One book supplies a credential through a host secret command;
the others add egress and the AWS profile.

- `stage up --plan` with approvals missing listed them and exited 1 without writing the env file or running
  `sbx env plan`, and printed the bindings audit.
- The first acceptance run, with the script as merged in #19, failed the secret scan: "missed its canary in: env
  home repo tmp bin end". Running the scan by hand showed every canary but the binary one was hashed. With
  `pipefail` on, piping about 84,000 hashes into `grep -q` fails with SIGPIPE when grep matches early, so found
  canaries read as missing, and the host-secret check, the same pipe under `!`, would have read a found secret as
  absent. The image has no `strings`, so the binary canary could not be found either. The earlier local-harness
  runs of the scan did not exercise this path. 9728e55 reads the hashes from a here-string and scans binaries with
  `grep -a`.
- With the fix, all 18 checks passed: the repository path, the agent CLIs, git identity and guidance, fetch from
  github.com, push refused at the proxy and for real, an unlisted host refused, `.git` writes kept out of the host
  scratch clone, the task directory hidden, the source mount read-only, the scan (83,978 strings, every canary
  found), the gh login token and broker token absent, and the broker serving the granted profile and refusing an
  ungranted one.
- Positive control: the sandbox's own broker token was written to `/tmp` inside the sandbox and the script rerun.
  "FAIL the broker token is nowhere in the sandbox" was reported, and the gh token check still passed. The file was
  then removed.
- The repository's end-to-end tests: 134 passed, 8 skipped by the tests' own attributes, 0 failed.
- A sample of its integration tests, built with the book's credential and run through the books' egress: 269
  passed, 11 failed. The same 11 fail on the host.

**kapellmeister** (`agent/e7125958/scoring-stage` at 77ff6873; agents agy, claude, codex and copilot from the
repository config; no AWS profile):

- All 14 checks that apply without AWS passed; the scan hashed 488,784 strings and found every canary.
- The agents' CLIs started, but no agent signed in or made a request in either sandbox.
- The repository kit's setup worked: `km`, `cao` and `cao-server` were installed, `cao-server` was running in tmux,
  and the saved agy login was copied in. A `km run` was not attempted.

## 2026-11-10: GitHub App mode and agent sign-ins on kapellmeister

- **sbx:** 0.51.0.
- **stage:** `main` at a452ee8, built to a temporary binary, the same build as the 2026-11-09 acceptance entry;
  `tests/acceptance.sh` from main at d39aee4. No non-test code changed between a452ee8 and d39aee4.
- **Credentials:** real. `[github] mode = "app"` with an enterprise-owned GitHub App the maintainer registered with
  read-only repository permissions and no organisation or account permissions, Device Flow on and expiring user
  tokens; the maintainer's agent logins; the saved agy login through `copy_logins`. No AWS profile.
- **Shared state:** the earlier kapellmeister sandbox, created in gh mode, was removed with `stage down` and
  recreated. `~/.config/sbx/credentials.yaml` had the same sha256 after the run as recorded on 2026-11-09
  (`4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`): the `github` binding on `api.github.com`
  and `github.com` already existed and did not change. `sbx secret ls --global` still listed no `github` secret.
  `stage github login` left the app login on the host, an access token and a refresh token in
  `secrets/github/app.json` under stage's home, which stays until it is removed or expires. The sandbox was left
  running after this entry to check token refresh.

GitHub App:

- `stage github login` showed a device code; after the maintainer authorised it, stage saved the login, reported
  the access token's expiry (8 hours) and the refresh token's (about 6 months), and listed the app's
  installations. The read-only check passed on every one.
- The sandbox's env file had a `github` secret with the command `stage github token` and `refresh: 30m`.
- Inside the sandbox, `GET /user` returned 200 as the maintainer, and a GraphQL read returned 200. `GH_TOKEN` held a
  `gho_sbxp…` placeholder whose hash matched neither the gh login token nor the app access token; the proxy
  supplies the real token.
- `POST /user/repos`, `PATCH /user` and `DELETE` on a git ref under `/repos/**` were refused by the proxy with 403
  and the body "Blocked by local rule for api.github.com:443", not by GitHub.

kapellmeister (`agent/e7125958/scoring-stage` at 77ff6873):

- `tests/acceptance.sh`: every check that applies without AWS passed, including the new checks that the app's
  refresh token and current access token are nowhere in the sandbox (488,877 strings scanned, every canary found).
- `km preflight` with all four agents, in a throwaway worktree and state directory. Preflight sends each agent a
  prompt to write the word "ready" to a file and reply "ready", so each one made an authenticated call to its
  vendor and used a tool. claude (22 s), codex (10 s), copilot (28 s) and agy (36 s) all did, 36 s in all. Only
  `cao-server`'s own tmux session remained afterwards. This is the first record of claude, codex, copilot and agy,
  including agy's copied login, authenticating and answering inside a sandbox on the current code.

## 2026-11-10: app-mode token refresh, and sbx moving the sandbox remote

- **sbx:** 0.51.0.
- **stage:** the same temporary build and kapellmeister sandbox as the previous entry. Other stage sandboxes, created
  from the same config, ran during the day.
- **Credentials:** as the previous entry.
- **Shared state:** `~/.config/sbx/credentials.yaml` had the same sha256 afterwards,
  `4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`.

Token refresh:

- The first access token expired eight hours after login. stage's saved app login was rewritten three minutes after
  expiry, not before it: the access token's fingerprint changed, its new expiry was eight hours on, and the refresh
  token's expiry moved to six months from the rewrite. No fingerprint of the refresh token was taken at login, so
  this does not show the refresh token itself changed.
- Nothing ran `stage github token` by hand. The kapellmeister sandbox was stopped across the expiry:
  - `sbx ls` showed it stopped between five and six hours before expiry.
  - The next command run in it, about 45 minutes after the rewrite, printed that sbx had started it.
  - sbx's audit log records a `filesystem_mount` event for every command run in a sandbox, including the one that
    starts it; another sandbox started by `sbx exec` that day logged one at that second. The kapellmeister sandbox
    has none between the minutes after login and that later command; assuming sbx starts a sandbox only through a
    command, which logs a mount event, it was not started in between.
- The rewrite came 34 seconds before another stage sandbox's first audit event. That fits its creation running
  `stage github token`, which refreshes a login that has expired or has under 40 minutes left.
- So a sandbox start refreshed an expired login. A running sandbox re-running the command on `refresh: 30m` was not
  observed: the kapellmeister sandbox was not running across the expiry. Another stage sandbox last ran a command
  25 minutes before expiry, and the audit log cannot show whether it was still running at expiry.
- After the kapellmeister sandbox was started again, `GET /user` from inside it returned 200. This shows the
  restarted sandbox had a working token, not that a running one was refreshed.

Sandbox remote:

- While the sandbox was stopped, the scratch clone's `.git/config` had no `sandbox-<sandbox>` remote. On the next
  start sbx added it again as `git://127.0.0.1:<port>/src` with its two fetch refspecs, on a different port from the
  one it had at `stage up`. Before #29, `stage ship` refused every sandbox that had restarted for this reason.

## 2026-11-10: sbx auto-stop, a kapellmeister ensemble and the first push to GitHub

- **sbx:** 0.51.0.
- **stage:** a temporary build of 974a2e8 (#29), in `app` mode.
- **Credentials:** real. The agents' saved sign-ins and the GitHub App's token, as the previous entries.
- **Shared state:** `~/.config/sbx/credentials.yaml` had the same sha256 afterwards,
  `4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`.

sbx auto-stop:

- sbx stops a sandbox 30 seconds after its last `sbx exec` session ends. Its daemon log records "session
  disconnected, deferring auto-stop" with a delay of 30 seconds, then "auto-stop grace period expired, stopping
  runtime". `stage shell` and `stage exec` are such sessions. No sbx setting for the delay was found.
- Work left running inside the sandbox after the session ends is stopped with it. A kapellmeister run started in
  a detached `tmux` session inside a sandbox died 40 seconds into planning, at the stop.
- Running the same command in the foreground of an `sbx exec` session that stayed open kept the sandbox up until it
  finished.
- This is why the kapellmeister sandbox in the previous entry was stopped across the token's expiry. The daemon log,
  checked afterwards, shows it auto-stopped at 13:28Z, 30 seconds after its last session following login, and logs
  no session for it until the command at 22:11Z that started it again, when sbx moved the sandbox remote.

kapellmeister ensemble on this repository:

- `stage up` on this repository, with a temporary local book whose kit built `km` from a pinned kapellmeister commit
  and the agy, claude, codex and copilot agents.
- `km run` with kapellmeister's `thorough` ensemble and this repository's tests as its verify step. Its five chairs
  approved after one round, in 10 minutes 9 seconds. The change became #31, with review follow-ups made by hand.

First push to GitHub:

- `stage ship --branch agent/<session>/origin-refs --yes` pushed the ensemble's commit from the scratch clone to
  GitHub with host git, after the sandbox had stopped and started several times. The config check passed with sbx's
  remote on a new port, as #29 intended.
- `--pr` was not used; the pull request was opened separately.

## 2026-11-10: takes copied out of a running sandbox

- **sbx:** 0.51.0, macOS.
- **stage:** the takes change on top of 974a2e8, built from its working tree before it was committed.
- **Credentials:** none used. The run had its own `STAGE_HOME` with a hand-written task record naming an existing
  sandbox and its takes, so no sandbox, secret or binding was created.
- **Restoration:** the probe directory made in the sandbox was removed. sbx's `credentials.yaml` was not hashed
  before and after; `stage take` runs only `sbx exec`.

Checked:

- The sandbox's tar (GNU tar 1.35) accepts `--null -T -` and writes an empty archive, exit 0, for an empty list.
- `stage take` on three listed paths, one of them missing, kept 3 files and reported the one symbolic link it left
  behind. A 70,000-byte file of random bytes had the same SHA-256 on the host as in the sandbox, so `sbx exec`
  passes a binary stream through unchanged. Files were mode 600 and directories 700.
- A task whose only take does not exist in the sandbox printed "has nothing to take", exited 0 and left no new
  directory.
- A larger take of 82 files (2.1 MB) from six paths under the sandbox user's home and `/tmp` completed.
- `sbx exec` started a stopped sandbox by itself before running the command.

## 2026-11-10: a larger take, after the copy was bounded in time and stream size

- **sbx:** 0.51.0, macOS.
- **stage:** the takes change on top of 974a2e8 with its review fixes, built from its working tree before they were
  committed.
- **Credentials:** none used. The same hand-written task record and separate `STAGE_HOME` as the entry above.
- **Restoration:** `sbx exec` started the stopped sandbox, and it was stopped again afterwards. sbx's
  `credentials.yaml` was not hashed before and after; `stage take` runs only `sbx exec`.

Checked:

- `stage take` on six listed paths kept 15,917 files (157 MB) and reported 63 links and special files left behind,
  exit 0. One listed directory held a full source checkout, which is where most of the files came from.

Not checked: checksums against the sandbox for this run, and any of the refusals.
