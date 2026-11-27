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
- Where sbx puts a book or repository kit's `host-mount@1` directory on the host when stage passes no `--mount` or
  `mounts`, and that it is the same directory for every sandbox using the kit. The kit specification says the
  runtime owns it; this has not been looked at on a live sbx.
- The release workflow's refusals: a tag that is not `vX.Y.Z[-suffix]`, and a tagged commit that is not on `main`.
- The release workflow's `homebrew` job: rendering the formula from a real release's `SHA256SUMS`, skipping without
  `PUBLISHER_APP_CLIENT_ID` or `PUBLISHER_APP_PRIVATE_KEY`, and pushing to the tap with a token minted from the
  publisher App, past the tap's ruleset, as the App's bot. `brew install jystringfellow/tap/scoring-stage` from a
  formula this workflow published.
- Managed settings, beyond unit tests.
- Takes: `stage down` keeping takes before a real removal, a repository's takes approval, a take near the size,
  entry or stream limit, a sandbox that goes quiet for the 10-minute limit, and overlapping paths on the sandbox's
  tar. `stage take` was checked on 2026-11-10.
- Gear: `stage up` with gear has not run against a live sandbox. On 2026-11-12 the sandbox side was run alone on
  sbx 0.51 (GNU tar 1.35): archives built by the gear package were piped into `sbx exec -i -u agent` with the
  unpack script. An existing file was replaced, a directory merged with what was there, existing directories kept
  mode 755, new directories and files came out 700 and 600 owned by `agent`, a name with non-ASCII letters and
  spaces arrived intact, the confirmation line came back on stdout, and a destination the sandbox user could not
  create exited non-zero without it. Unchecked are the full `stage up` path, the 5-minute limit, and whether a
  kit's `startup` command has already run when `sbx env create` returns.
- A take's `stage.json` and the ships `stage ship` records on a task: run only against fakes.
- Book and repository kit refusals: a changed kit asking again, a book claiming a reserved service, and a
  published kit in a book or repository config.
- Whether `sbx env create --force` prints the plan it approves.
- Where sbx keeps `credentials.yaml`, beyond `~/.config/sbx/credentials.yaml` on macOS. `stage setup`'s bindings
  audit also looks under `$XDG_CONFIG_HOME` and `%APPDATA%`, inferred from strings in the sbx binary.
- Fetching a book from github.com by a plain tag (the fallback when a book has no tag of its own), by an annotated
  tag, and after a tag moved on github.com. A book's own lightweight `<directory>/<version>` tag was checked on
  2026-11-23, and a full commit on 2026-11-22.
- The library books in `books/`, used by reference in a sandbox. The Go book has not been run in a sandbox as a
  book: its kit is the one this repository's own config names, but no entry here checks a Go command in a
  sandbox. The Python book ran in a sandbox, given by path, on 2026-11-13. The .NET book has not been run in a
  sandbox from this repository, by path or by reference, nor included by another book in one: no entry here
  checks its SDK install, its NuGet or certificate revocation egress, or a `dotnet` command in a sandbox.
- Kit arguments set where a book is named. The tests show stage checking each value against the kit's `pattern` and
  writing it into the env file's kit `args`; no sandbox has been created from a book named with arguments, so it is
  unchecked that sbx accepts them and that the kit's install sees them, for example the .NET kit installing another
  SDK channel or an extra runtime.
- `stage ship` on a stopped sandbox. The tests show it starting the sandbox with `sbx exec` before it fetches; a
  live run has not checked that the remote is back when that exec returns, nor a fetch larger than sbx's 30-second
  grace after it. Before this change, a run of v0.0.1-rc.6 saw ship fail on a stopped sandbox and succeed after a
  `stage exec` in the same shell command.
- `stage secrets refresh` itself against a live sandbox. The `sbx secret set --sandbox … --command … --force` call
  it makes was checked on 2026-11-24 by hand, with a dummy command; the command as stage runs it, `--refresh`
  passed to `sbx secret set`, and services other than `copilot` were not.
- The broker's Secrets Manager endpoint in a live sandbox, and against AWS itself. On 2026-11-26 the proxy did not
  inject the broker token as `X-Stage-Broker-Token` on a `POST` to `localhost:9912` (see that entry), so the endpoint
  does not work in a live sandbox yet and nothing past the token check has been reached. The inject probe the same
  day found why: sbx sends a credential to every `inject` entry's domain with the first entry's header and format,
  so port 9912 gets `Authorization: Bearer`. Still unchecked:
  - whether the broker works live once both ports take the token the same way;
  - whether an SDK in a sandbox reads the `[services stage-broker]` section that the `aws-broker` kit writes (the
    key is `secrets_manager`), and signs with the placeholder keys when no profile is the default;
  - the broker's own signed calls to Secrets Manager with a real temporary session, and AWS's answers to them.

  Run only against fakes: the operations, filters and refusals.
- Whether sbx matches allow entries that are IP addresses, CIDR ranges or hosts with underscores. stage refuses
  them when Secrets Manager secrets are listed, because the kit specification's host grammar does not name them.
- sbx's kit loader: which files it reads from a kit directory, and whether it matches keys case-insensitively.
  stage reads the one `.yaml`, `.yml` or `.json` descriptor at a kit's root, refuses any other root entry but
  documentation and `files/`, and refuses keys that differ from `capabilities`, `extends`, `type`, `config`,
  `service`, `inject` or `domain` only in case. Whether sbx reads a descriptor from `files/`, or a `# kit:` comment
  inside a YAML descriptor, is unchecked. Also unchecked: that sbx never takes a descriptor from a file named like
  documentation (README, LICENSE and the rest) or `.DS_Store`, which stage accepts at a kit's root unread.

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

## 2026-11-12: put and detached jobs against a live sbx

- **sbx:** 0.51.0, macOS.
- **stage:** built from 1a30a13.
- **Credentials:** none used. A sandbox of this repository with the `shell` and `stage` kits only, in the usual
  stage home.
- **Restoration:** the sandbox was removed with `stage down`; its task directory and job records went with it and
  no holder process remained. The SHA-256 of sbx's `credentials.yaml` was the same before and after.

Checked:

- `stage put` of a 70,000-byte and a 6,000,000-byte file of random bytes: each had the same SHA-256 in the sandbox
  as on the host and was owned by `agent`. Both arrived mode 600, though the larger was mode 644 on the host.
- `stage put` refused a directory, a missing file and the destination `~/../x`, each with exit 1.
- `stage exec --detach` of `sleep 150; echo done; exit 7` printed only the job id on stdout. After 80 seconds
  `stage job wait --timeout 80s` exited 124, `sbx ls` showed the sandbox running and the job was `running`. A later
  `stage job wait` exited 7 and `stage job output` printed `done`.
- The holder's arguments in `ps` were `job hold <slug> <id>`, without the command.
- `stage down` refused while a job was running and removed nothing.
- `stage job stop` reported `stopped` and `stage job wait` then exited 125. **The command was still running in the
  sandbox afterwards** (`kill -0` of its recorded pid succeeded): stop ends the host's session, not the command.
- A holder killed with SIGKILL: the job was reported `lost`, `stage job wait` exited 125 and the output written
  before the kill stayed readable.
- Ordinary `stage exec` behaved as before alongside jobs.

Not checked: a host restart with a job running, the sandbox stopping by itself after a job ends, the put timeout,
and put's refusal of the workspace and of agent login files.

## 2026-11-12: the MCP tools against a live sbx

- **sbx:** 0.51.0, macOS.
- **stage:** built from 3dbf50e.
- **Client:** a script speaking the stdio protocol (revision 2025-11-25), started in a scratch directory. No MCP
  client application was used.
- **Credentials:** none beyond what `stage up` sets for any sandbox. A sandbox of this repository, which has no
  repository config, in the usual stage home.
- **Restoration:** the sandbox was removed through the `down` tool. No holder process remained and nothing was
  pushed: no `agent/<slug>/` branch exists on the remote.

Checked:

- `tools/list` returned `list`, `up`, `put`, `exec`, `job`, `take`, `ship` and `down`. `up` with an `approve`
  argument and `down` with a `discard_takes` argument were refused as unknown arguments.
- `up` created the sandbox. sbx's own plan output went to the server's stderr; stdout carried only protocol
  messages.
- `put` of a file in the server's working directory arrived and was read back with `exec`; `put` of `/etc/hosts`
  was refused as outside the working directory.
- `exec` returned the command's status (3) and output. Detached `exec` returned a job id; after the server exited
  and a new one started, `job` wait returned `exited` with status 4 and `job` output returned its text.
- `ship` of a commit made in the sandbox returned the commit, diffstat, branch and command with `pushed: false`.
  After a commit adding a file under `.github/workflows/`, `ship` was refused.
- `down` removed the sandbox.

Not checked: a repository whose config needs approval, `take` with takes present, the exec and wait caps,
cancellation, and an MCP client application.

## 2026-11-13: the Python example book against a live sbx

- **sbx:** 0.51.0, macOS.
- **stage:** built from 0e30846 plus the cache fix described below.
- **Credentials:** none used. A sandbox of a public Python repository, with the `shell` and `stage` kits and the
  book given by path, in the usual stage home.
- **Restoration:** the sandbox was removed with `stage down`.

Checked:

- sbx accepted the kit, including the install-only entries that limit `github.com` to GET requests under the
  Python build releases path.
- In the sandbox `uv` 0.13.8 and `python3.12` (3.12.16) were on the agent user's PATH; the image's own `python3`
  was still 3.14.
- As the agent user, `uv sync --python 3.12 --locked --all-extras --dev` installed the repository's dependencies
  from PyPI, and 590 of its unit tests passed. Without the book the same command was refused with
  `403 Forbidden` from `files.pythonhosted.org`.
- **Before the cache fix, `uv` failed for the agent user** with `Failed to initialize cache at
  /home/agent/.cache/uv ... Permission denied`: the install steps run as root with the agent's home, and left
  that directory owned by root. The steps now run without a cache, and the directory is created by the agent
  on first use.

Not checked: a Python version other than the default, a repository that needs a C compiler to build a
dependency, and `stage init --guide` followed by an agent.

## 2026-11-18: fetching a shipped branch, a sandbox's disks and the root disk size

- **sbx:** 0.51.0, macOS.
- **stage:** built from 49924c2 for the first sandbox, and from that commit plus the disk report and `--disk`
  changes for the second.
- **Credentials:** none real in the sandbox for GitHub: `[github] mode = "none"`, on this public repository. The
  agent mixins brought their usual secrets and bindings. A temporary `STAGE_HOME` was used; the repository's own
  config (its Go example kit) was approved there with `--approve-repo-config` on the maintainer's instruction.
- **Restoration:** two sandboxes were created one after the other and each removed with `stage down`. sbx's
  `credentials.yaml` had the same sha256 before and after. No holder process remained. `stage ship` pushed one
  probe branch, `agent/probe-fetch/1eeef6d3`, which was left for the maintainer to delete.

Commands, in order: `stage up <repo> probe-fetch --approve-repo-config`; `stage exec probe-fetch -- sh -c '...'`
running `git branch -avv`, `df -Pk`, `lsblk`, `git fetch origin`, `git ls-remote origin` and a commit;
`stage ship probe-fetch --yes`; `stage exec` again running `git fetch origin` and `git ls-remote origin
'refs/heads/agent/probe-fetch/*'`; `stage list`; `stage exec --detach` and `stage job wait`; `stage down
probe-fetch`; `stage up <repo> probe-disk --disk 30g`; `stage exec probe-disk -- df -h`; `stage up <repo>
probe-plan --plan --disk <value>` for six values; `stage down probe-disk`.

Checked:

- **A sandbox with no GitHub credential fetched the branch it had shipped.** `git fetch origin` and `git ls-remote`
  inside the sandbox exited 0 before and after the ship, and the fetch after it brought
  `origin/agent/probe-fetch/1eeef6d3`. The 403 seen on private repositories did not appear, so the proxy's rules
  do not refuse the fetch. Not checked: the same fetch with a real credential on a private repository, where the
  403 was reported.
- The first sandbox's clone, made by a stage without the scratch clone's branch fix, came up with a detached
  `HEAD` at GitHub's `main` and `origin/main` at the host checkout's older commit.
- **A sandbox has three disks.** `/` is a 20 GB overlay, `/var/lib/docker` a 10 GB disk, and the cloned
  workspace a 50 GB disk mounted at the repository's directory, the same path as the scratch clone on the host.
  `df -Pk / <repository>` printed one row for each, with those two mount paths.
- `stage job wait` on an exited job in a running sandbox printed
  `stage: disk in stage-probe-fetch: / 3% used (18.0 GiB free), <repository> 1% used (46.4 GiB free)` on stderr
  and exited with the job's status. `stage list` printed no disk figures for the sandbox once sbx listed it as
  stopped; it was not checked while sbx listed it as running.
- **`sandboxOptions.rootSize: "30g"` in the env file sized `/` at 30 GB.** sbx's plan showed
  `sandboxOptions.rootSize: 30g`, and `df -h /` in the new sandbox showed 30G; the other two disks were unchanged.
  The key is not in sbx's documentation and was found in the sbx binary, which also names `dockerSize` and
  `clonedWorkspaceSize`; those two were not tried. sbx has a `sandbox.disk.dockerVolume` setting (10g).
- `sbx env plan` does not check the value: it printed `30GiB`, `30G`, `30000m`, `abc`, `1g` and `30` back without
  complaint. Only `30g` was tried in a creation, so stage accepts only whole gigabytes written that way.

Not checked: a size below sbx's default, a disk that actually fills, and the 90% warning.

## 2026-11-18: the probe branch from the entry above is gone

The entry above says the probe branch `agent/probe-fetch/1eeef6d3` was left for the maintainer to delete. It has
since been deleted: `git ls-remote origin 'refs/heads/agent/probe-fetch/*'` from the host printed nothing. Nothing
from that run remains on the remote.

## 2026-11-19: the stage read against a real CAO server, providers under real `timeout`, and auto-stop

- **sbx:** 0.51.0, macOS.
- **stage:** built from ce71722 (the hall's slice 3 with its review fixes; 4200a40 has the same tree).
- **Credentials:** none real. A temporary `STAGE_HOME` with `[github] mode = "none"` and
  `[secrets.copilot] command = "echo stage-probe-dummy"`; no agent signed in. The sandbox was made from a clone of
  kapellmeister at a466e0d, whose repository config was approved with `--approve-repo-config` in that home only.
- **Restoration:** the one sandbox created, `stage-hall-probe`, was removed with `stage down`, which also removed
  its scoped secrets. sbx's `credentials.yaml` had the same sha256 before and after
  (`4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`). No stage process remained. No other
  sandbox was read or touched: the other 17 that sbx listed had no task in this home and were listed only.

Checked:

- **The read against CAO 2.5.4.** Two sessions were started with `cao launch --headless`, one `claude_code` and one
  `codex` terminal. `stage list` showed `2 (2 idle)`, and `--json` had both terminals with CAO's `idle`, their
  profiles and providers, and 26 and 40 tail lines read through `tmux capture-pane`. CAO's `/sessions` also lists
  its own `cao-server` tmux session, whose terminals list is empty.
- **The cost of a read.** One read of the stage took 552 ms (`took_ms`), and `stage list` took 1.0 to 1.1 seconds in
  all with 18 sandboxes listed.
- **Providers under GNU coreutils `timeout` 9.7.** Two providers each running `sh -c 'sleep 31; echo late'`: the
  first was stopped at the shared 4 seconds, the second was reported as not run, CAO's sessions and the disk were
  still read, and the read took 4.6 seconds. Afterwards no provider `sleep` was left in the stage, so `timeout`
  stopped the shell's child too. A provider that ignores SIGTERM (`trap '' TERM` and a loop) was killed by
  `-k 1`: the read took 5.6 seconds, and nothing was left.
- **Auto-stop and `last_used_at`.** With no session open, a read moved sbx's `last_used_at` to the end of the read,
  and the sandbox stopped 30 seconds after it. On stopping, `last_used_at` moved again, to the time it stopped, so
  it is not the time of the last agent activity. `stage list` while it was stopped showed `stopped`, read nothing
  and left it stopped for the 12 seconds watched.
- `stage exec` on the stopped sandbox started it, as recorded on 2026-11-10.

Not checked: a stage that stops between `sbx ls` and the read, a CAO terminal that is working or waiting, a
provider that floods, and several stages read at once.

## 2026-11-20: `stage hall` in a browser against a stage running km and CAO

- **sbx:** 0.51.0, macOS.
- **stage:** built from `agent/839afa8c/hall-terminal-tail` at the head of #64 (the terminal tail on the hall page), on
  top of main at 51ee235.
- **Credentials:** none real. The same kind of temporary `STAGE_HOME` as the 2026-11-19 entry, with dummy secrets and
  no agent signed in. The sandbox was made from kapellmeister at 64fcd4f, whose kit built `km` from that commit.
- **Restoration:** the one sandbox created, `stage-hall-demo`, was removed with `stage down`, with its scoped
  secrets. sbx's `credentials.yaml` had the same sha256 before and after
  (`4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`). No stage process remained. The other 17
  sandboxes were listed and not read.

Inside the stage, a scratch repository was committed with kapellmeister #103's demo score, two parts and a
four-chair ensemble, and `km run -worktree . -ensemble duo -task … -detach` was started. With no agent signed in, km
stayed in its preflight, so `km status -json` reported the run as schema 1 and `alive` with `round 0/2`, but no
sections, score steps or chair terminals. Its preflight had opened three CAO sessions.

`stage hall` was opened in headless Chrome and driven over the DevTools protocol:

- The stage row read `running`, `3 (3 idle)` and `km run · duo · round 0/2`. The read had taken 698 ms.
- Expanding the row showed the activity and the three CAO sessions, each with its terminal. Opening one showed its
  real tmux lines (the sandbox banner and the agent's prompt) as text.
- 70 seconds later the hall had read the stage again and the page had redrawn, and the terminal was still open with
  its lines.
- The browser reported one error, a 404 for `/favicon.ico`.

Not checked: a member's terminal through its session link, since km never seated a chair; that path is covered by
km's captured schema 1 document in the tests. A run with chairs seated needs agents that are signed in.

## 2026-11-20: holding a demo km run up, and global secrets in the hall runs

- **sbx:** 0.51.0, macOS.
- **stage:** built from the follow-up branch to #63, #62 and #64 (this entry's PR), on main at 79eeb5f.
- **Credentials:** the temporary `STAGE_HOME` set no real secret, but sbx's global secrets apply to every sandbox,
  and this machine has global `anthropic` and `openai` OAuth secrets (`sbx secret ls --global`). So the claude
  chairs' preflight in this run, and in the 2026-11-20 entry above, reached the model through them and spent a
  few real tokens. That entry's "none real" holds for stage's own secrets only. No agent ran beyond km's preflight.
- **Restoration:** the one sandbox created, `stage-hall-demo`, was removed with `stage down`, with its scoped
  secrets. sbx's `credentials.yaml` had the same sha256 before and after
  (`4998c033b4b70e066939e87ed0f223fea6dee62917823758bc7a58e2ce021008`). No stage process remained.

Checked:

- **A detached km run does not hold a stage up.** With `km run … -detach` at the end of a script run through
  `stage exec`, the exec returned, and with only `stage hall` reading the stage, sbx stopped it about 40 seconds
  later and it stayed stopped for the 3 minutes watched. The hall did not start it again. Started again by hand,
  km's records showed the run and nothing playing.
- **A stage job does.** With `km run` in the foreground of the same script, run as
  `stage exec --detach hall-demo -- sh /home/agent/hall-demo.sh`, the stage stayed running while the job ran.
  The hall reported one running job and read the stage every 10 to 15 seconds, and the page's sessions and
  activity changed with it. When km exited, the job ended and sbx stopped the stage about 20 seconds later.
- **Why km exited here:** its preflight passed both claude chairs and blocked the agy chair after 90 seconds, since
  agy had no login in the stage.
- **The script's path:** `stage put` writes to the agent's home, while `stage exec` runs in the repository, so the
  script is `/home/agent/hall-demo.sh`, not `hall-demo.sh`.

## 2026-11-20: the hall against a real km run, with real logins

- **sbx:** 0.51.0, macOS.
- **stage:** built from main at fdcd3d3 (#72).
- **km:** built from kapellmeister main at 8024aad inside the stage, by its repository kit.
- **Credentials:** real. The maintainer's own agent logins, in the stage-job demo from the 2026-11-20 entry above (`stage exec --detach hall-demo -- sh /home/agent/hall-demo.sh`), with km's `draft-and-check` score on five chairs across codex, claude, agy and copilot in three sections.
- **Run by:** the maintainer. Results were read from the page's `api/hall`.

Checked:

- **20 seconds in:** the row read `km run · duo · round 0/2 · preflight`, with the steps preflight (active), draft and check, three sections and five chairs idle, and no verify steps. The stage asked for attention for one waiting session, and that cleared by itself.
- **After km exited** (approved, 1 round, 1m33s): the activity was `succeeded`, reason `approved`, 41,639 tokens exact and 93 seconds elapsed, with no provider errors. The row read `km · duo · round 1 · succeeded · 1m33s · 42k tokens`; #73 makes a finished run's title `km run` as the live one is.
- **The scoreboard** for the repository went from 9 runs (7 succeeded, 1 failed, 1 stopped, ≥1,127,805 tokens) to 10 runs (8 succeeded, ≥1,169,444 tokens).
- **Seven minutes after the run:** the stage was `stopped` and still listed with the finished run, `remembered` at 2026-11-20T13:26:08Z. Its saved view `takes/hall-demo/.hall-view.json` was on the host, mode 0600, 1,514 bytes.
- **After the hall was stopped and started again** on a build of main, the new process listed `hall-demo` without reading it, since it was still stopped. It showed the same finished run, `remembered` at the same 13:26:08Z, and the scoreboard still showed 10 runs (8 succeeded, 1 failed, 1 stopped, ≥1,169,444 tokens). The saved view and the finished runs survived the restart. The title still read `km · duo`, because the view was saved before #73.
- **After `stage down hall-demo`** (without `--discard-takes`), on a build of main at f734b3a, at 14:17:02Z: the takes
  were kept and the sandbox removed. Eight seconds later the running hall listed `hall-demo` as `gone` with its
  finished run from the saved view, and the scoreboard was unchanged at 10 runs.
- **A rehearsal after that,** on km from kapellmeister main at ca77363c with four tiers: the row went preflight, then
  draft, then check, then `km run · duo · round 1 · succeeded · 1m18s · 29k tokens`, with the title from #73. The
  working drafter's terminal (`3b48b9e6`) and both checkers' terminals were linked.

## 2026-11-20: the README's pictures of the hall

- **sbx:** 0.51.0, macOS.
- **stage:** built from main at f734b3a; km built from kapellmeister at e8a2dad2 inside the stage.
- **Credentials:** real, on the maintainer's go-ahead for this recording only. The temporary `STAGE_HOME` at a
  neutral path set no real secret, and the agents answered through sbx's global `anthropic` and `openai` secrets.
- **Runs:** three of kapellmeister's `draft-and-check` score as stage jobs, every chair on claude or codex. Each was
  approved after one round, in 41 to 52 seconds and 3,753 to 4,790 tokens.
- **Restoration:** both sandboxes created, `stage-draft-demo` and `stage-docs-pass`, were removed with `stage down`,
  with their scoped secrets. sbx's `credentials.yaml` had the same sha256 before and after. No stage process
  remained.

Checked while capturing:

- The table, the graph and the scoreboard drew a live run, its finished runs and the stopped stage as the README
  pictures show. The pictures are screenshots of the page alone, so the hall's token in the address bar is not in
  them.
- **The page lists every `stage-*` sandbox sbx knows,** including sandboxes from other stage homes, in its
  "not running" line. That line is cropped from the pictures.
- **`sbx ls --json` once printed a character after its JSON,** and the hall showed "cannot read sbx ls --json" for
  one refresh while it kept its last view. The next listings were clean.

## 2026-11-22: a book fetched by full commit from a private github.com repository

- **sbx:** not used; the check is of stage's fetch, cache and lock, which run on the host.
- **stage:** the head of the change that added books by reference (3b0f403).
- **Credentials:** real: the host's own git credentials for github.com, which a private repository needs. A temporary
  `STAGE_HOME` held the lock and cache; no secret was set and no sandbox was created.
- **Restoration:** the temporary `STAGE_HOME` was removed. Nothing outside it was written.

Observed:

- A book in a private books repository, named by a full commit, was fetched, cached read-only under
  `books/<fingerprint>/`, recorded in `stage.lock`, and opened again from the cache.
- A version that names only a branch was refused as a branch, and a directory missing at that commit was refused
  naming the directory.
- Not checked: a tag, because that repository had none.

## 2026-11-23: books fetched by their own tags from a private github.com repository

- **sbx:** not used; the check is of stage's tag resolution, fetch, cache and lock, which run on the host.
- **stage:** main at 4f4c5e5, which resolves a book's own `<directory>/<version>` tag before the plain tag. The
  check was a throwaway test in `internal/bookref`, removed afterwards.
- **Credentials:** real: the host's own git credentials for github.com, which a private repository needs. A temporary
  `STAGE_HOME` held the lock and cache; no secret was set and no sandbox was created.
- **Restoration:** the temporary `STAGE_HOME` and the throwaway test were removed. Nothing outside them was written.

Observed:

- Two books in a private books repository, each named as `//<directory>@v0.1.0`, each with its own lightweight
  `<directory>/v0.1.0` tag and no plain `v0.1.0` tag. Both resolved to the book's own tag (`Fetched.Tag` was
  `<directory>/v0.1.0`), were fetched, cached, recorded in `stage.lock`, and opened again from the cache at the same
  commit.
- A version with neither tag was refused: `has neither tag <directory>/v9.9.9 nor tag v9.9.9; nothing was
  changed`.
- Not checked: the plain-tag fallback, an annotated tag, and a tag moved on github.com.

## 2026-11-24: secret commands in a live sandbox, with and without `refresh`

- **sbx:** 0.51.0, macOS.
- **stage:** 5aacee7 with this change's uncommitted work, built from its working tree.
- **Credentials:** none real. Two sandboxes from this repository, each with its own `STAGE_HOME` under a temp
  directory, `[github] mode = "none"`, and the `copilot` agent's secret replaced in that config by a host command
  that appended the time to a log file and printed a dummy value (`stage-probe-dummy-…`). One had `refresh = "1m"`,
  the other no `refresh`. Requests from inside each sandbox went to `https://api.githubcopilot.com/models`, so the
  proxy used the secret; they carried the dummy value and were answered 400.
- **Restoration:** both sandboxes removed with `stage down`, which removed their scoped secrets; `sbx secret ls`
  then listed none for them and `sbx ls` the same number of sandboxes as before. sbx's `credentials.yaml` had the
  same sha256 before and after; it was hashed, not copied. Nothing else was created.

Checked:

- **With `refresh = "1m"`,** the command ran at creation, then again on the first request after each minute had
  passed while the sandbox kept running (requests every 30 seconds for 4 minutes: a run on every other request).
  After `sbx stop` and a start, a request within the minute did not run it, and the first request after the
  minute did.
- **Without `refresh`,** the command ran at creation and on the first request, then not again: not over 4 minutes
  of requests in a running sandbox, and not after `sbx stop` and a start. So an env-file secret with no `refresh`
  keeps its first value for the sandbox's life, which is how a sandbox kept a `gh` token after the host's login
  changed. This also settles, for `copilot`, the part of app mode's open question above about re-running on the
  interval in a running sandbox: sbx does, on a request after the interval.
- **`sbx secret set copilot --sandbox <sandbox> --command <new command> --force --no-input`** on the running
  sandbox without `refresh` printed "Applied secret to sandbox", ran the new command once to check it, and the
  sandbox's next request ran the new command; the old one never ran again. This is the call `stage secrets
  refresh` makes. The command itself was not run against a live sandbox.

## 2026-11-26: the broker's Secrets Manager endpoint in a live sandbox

- **sbx:** 0.51.0 (7d55286de0c7), macOS.
- **stage:** `main` at ea6dd79, cloned from github.com and built to a temporary binary.
- **Credentials:** real but unused. The run had its own `STAGE_HOME` under a temp directory, `[github] mode = "none"`,
  and `[aws.secrets_manager]` listing one real secret for one of the maintainer's real assumed-role profiles, with
  fresh host credentials. A second real secret that profile can read was left unlisted. Every request stopped at the
  broker's token check, so the broker made no call to AWS and no secret value was read or printed.
- **Shared state:** the broker's ports are fixed, so the maintainer stopped their own broker, which serves their
  sandboxes from their own `STAGE_HOME`, for the run and started it again afterwards. sbx's global `anthropic` and
  `openai` secrets were accepted with `--allow-global-secrets`, and the repository's Go kit was approved with
  `--approve-repo-config` in the run's own trust store.
- **Restoration:** four runs. The first two stopped at `stage up`'s refusals, before a sandbox was made, until the
  two flags above were added; the last two made a sandbox, and the fourth added the request to port 9911 below.
  Each ended with `stage down`, which removed the sandbox's scoped secrets, and `stage broker stop` for the run's
  broker. Afterwards `sbx ls -q` listed the same sandboxes as before,
  `sbx secret ls` listed none for the probe, and sbx's `credentials.yaml` had the same sha256 (hashed, not copied).
  No global secret or binding was changed.

Checked:

- **`stage up`** printed the listed secret under its profile, started the broker on both ports, wrote the grant file
  with the one name and granted no AWS profile to the sandbox.
- **The `aws-broker` kit's `~/.aws/config`** had `[default]` with placeholder keys and `services = stage-broker`, and
  `[services stage-broker]` with `secrets_manager` pointing at `http://host.docker.internal:9912`.
- **The token was not injected on port 9912.** `ListSecrets` and `GetSecretValue`, posted from inside to
  `http://host.docker.internal:9912/` without a token, were answered 401 by the broker, which logged no sandbox for
  them. A `GET` to `http://host.docker.internal:9911/credential-process/not-granted` from the same sandbox arrived
  with the token: the broker logged it under the sandbox's slug and answered 403 for the ungranted profile. So the
  proxy applied the kit's `localhost:9911` entry (`Authorization: Bearer`) and not its `localhost:9912` entry
  (`X-Stage-Broker-Token`) of the same `stage-broker` credential. Which of the two causes in Not yet run live is
  behind it was not tested.
- **Secrets Manager itself** stayed closed: a request from inside to
  `https://secretsmanager.us-east-1.amazonaws.com/` got 403 from the proxy.
- Not checked: an SDK reading `[services stage-broker]`, since the `shell` base has no `aws` CLI or Python; the
  allow list against AWS; and refusal of the unlisted secret beyond the token check.

## 2026-11-26: a credential's inject entries with different headers

- **sbx:** 0.51.0 (7d55286de0c7), macOS.
- **stage:** not used. sbx was driven directly with hand-written env files (`sbx env create --force --no-input`) and
  one local kit per variant; `main` was at 7d02c14.
- **Credentials:** none real. Each env file set the `copilot` secret at the sandbox's scope to a command printing a
  fixed dummy value, and the global `copilot` binding authorised it, so no binding was declared or added. On the
  host, a listener on `127.0.0.1:18911` and `127.0.0.1:18912` logged each request's header names and whether the
  `Authorization` and `X-Stage-Probe` values were the dummy, the dummy after `Bearer `, or something else.
- **Shared state:** the maintainer's broker and ports 9911 and 9912 were not touched.
- **Restoration:** three sandboxes, each removed with `sbx env rm --force --no-input`, without `--prune-bindings`,
  which removed its scoped secret. Afterwards `sbx ls -q` listed the same sandboxes as before, `sbx secret ls` none
  for the probe, and sbx's `credentials.yaml` had the same sha256 (hashed, not copied). The listener was stopped.

Each kit had a `network-policy@2` runtime allow for both ports and one `credential@1` for `copilot` with two
`apiKey.inject` entries. A `GET` was sent to each port from inside, at `http://host.docker.internal:<port>/`.

Observed:

- **A credential's first inject entry sets the header and format for all its domains.**
  - With `Authorization: Bearer %s` on 18911 first and `X-Stage-Probe: %s` on 18912 second, both ports got
    `Authorization: Bearer <dummy>` and neither got `X-Stage-Probe`.
  - With the entries' headers swapped, both ports got `X-Stage-Probe: <dummy>` and neither got `Authorization`.
  - With `Authorization: Bearer %s` on both ports, both got it.

  So the secret reaches every entry's domain, but a later entry's own `header` and `format` are not used. This is
  why port 9912 never got `X-Stage-Broker-Token` in the run above: it got `Authorization: Bearer`, which the
  Secrets Manager endpoint does not read.
- **The proxy replaces an `Authorization` header the request already has.** A request from inside carrying its own
  `Authorization: AWS4-HMAC-SHA256 …` arrived with one `Authorization` value, `Bearer <dummy>`, on both ports. The
  request's own value did not reach the listener.
- Not checked: entries listed with port 18912 first. In every variant the first entry was also the 18911 one, so
  whether list order or the domain picks the winning entry is open. Three or more entries were not tried.
