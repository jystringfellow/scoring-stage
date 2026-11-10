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
- `app` mode's token refresh: whether sbx re-runs `stage github token` on the secret's `refresh` interval, so a
  sandbox keeps GitHub access after its first access token expires. Also a refresh-token rotation, and whether
  `/user/installations` reports account permissions (the app checked on 2026-11-10 had none).
- `stage ship` pushing to GitHub, and `--pr`.
- The release workflow's refusals: a tag that is not `vX.Y.Z[-suffix]`, and a tagged commit that is not on `main`.
- A full kapellmeister score on the current code. A score ran on an earlier stage build (2026-11-09, outside this
  log); on the current code only preflight has run.
- Managed settings, beyond unit tests.
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
  `tests/acceptance.sh` from main at d39aee4.
- **Credentials:** real. `[github] mode = "app"` with an enterprise-owned GitHub App the maintainer registered with
  read-only repository permissions and no organisation or account permissions, Device Flow on and expiring user
  tokens; the maintainer's agent logins; the saved agy login through `copy_logins`. No AWS profile.
- **Shared state:** the earlier kapellmeister sandbox, created in gh mode, was removed with `stage down` and
  recreated. The sandbox was left running after this entry to check token refresh.

GitHub App:

- `stage github login` showed a device code; after the maintainer authorised it, stage saved the login, reported
  the access token's expiry (8 hours) and the refresh token's (about 6 months), and listed the app's 11
  installations. The read-only check passed on all of them.
- The sandbox's env file had a `github` secret with the command `stage github token` and `refresh: 30m`.
- Inside the sandbox, `GET /user` returned 200 as the maintainer, and a GraphQL read returned 200. `GH_TOKEN` held a
  `gho_sbxp…` placeholder whose hash matched neither the gh login token nor the app access token; the proxy
  supplies the real token.
- `POST /user/repos`, `PATCH /user` and `DELETE` on a git ref under `/repos/**` were refused by the proxy with 403
  and the body "Blocked by local rule for api.github.com:443", not by GitHub.

kapellmeister (`agent/e7125958/scoring-stage` at 77ff6873):

- `tests/acceptance.sh`: every check that applies without AWS passed, including the new checks that the app's
  refresh token and current access token are nowhere in the sandbox (488,877 strings scanned, every canary found).
- `km preflight` with all four agents, in a throwaway worktree and state directory: claude (22 s), codex (10 s),
  copilot (28 s) and agy (36 s) each answered ready through CAO, 36 s in all. Only `cao-server`'s own tmux session
  remained afterwards. This is the first record of claude, codex, copilot and agy, including agy's copied login,
  authenticating and answering inside a sandbox on the current code.
