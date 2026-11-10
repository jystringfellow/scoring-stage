# Verification

What has been checked against a live sbx or GitHub, when, on what, and how shared state was put back.
`go test ./...` runs every command against fakes (an `sbx` script on PATH, a local repository standing in for
github.com, a simulated GitHub API and device flow, and a stub `aws` CLI behind the broker), so it shows what stage
asks sbx, git and GitHub to do, not what they then do. Those runs are not recorded here.

Entries are added at the end and not rewritten. When a later run contradicts an entry, add a new entry that says so.

## Not yet run live

- The current `tests/acceptance.sh` as a whole. Beyond the first version's checks (see 2026-11-08), it checks that
  the sandbox's writes to its `.git` do not reach the host scratch clone (`workspace.clone`), that the task
  directory is hidden, and that the host source mount is read-only (skipped when sbx adds none). Given an AWS
  profile, it checks that the broker serves that profile and refuses another. It has no CI.
- The acceptance script's secret scan, which has run only in a local harness. As the sandbox's own user, the
  sandbox hashes the token-like words in its environment, the environments and command lines of processes that user
  can see (not root's or the proxy's), its home directory, `/tmp`, the repository and its git history, the strings
  in binary files under the home directory and repository, and `/etc`. The host checks those hashes against its
  secrets, as written and base64-encoded. The scan plants a canary in the environment, the home directory, the
  repository, `/tmp` and a binary file, and adds an end marker; a missing canary fails the run. `/etc` cannot hold
  a canary, so its coverage is unproven. Matching is by whole token-like word, so a base64 token sitting at an
  offset inside a longer blob is not found.
- The GitHub REST write refusals in the stage kit. They are bounded deny entries like the push refusal, but no
  request has been checked against them.
- `fine-grained` token checks and `stage token set` against GitHub itself.
- `app` mode end to end: a registered GitHub App, `stage github login`, the installation check, and sbx re-running
  `stage github token` at the secret's `refresh` interval.
- `stage ship` pushing to GitHub, and `--pr`.
- The release workflow's refusals: a tag that is not `vX.Y.Z[-suffix]`, and a tagged commit that is not on `main`.
- Agent sign-ins, and `copy_logins` with a real agy login, on the current code.
- Books, repository kit approval and managed settings, beyond unit tests.
- Whether `sbx env create --force` prints the plan it approves.
- Where sbx keeps `credentials.yaml`, beyond `~/.config/sbx/credentials.yaml` on macOS. `stage setup`'s bindings
  audit also looks under `$XDG_CONFIG_HOME` and `%APPDATA%`, inferred from strings in the sbx binary.
- The audit's parsing of `sbx secret ls --global --json` against real sbx output.
- That `sbx secret rm <service>` removes the global secret by default, which comes from sbx's help text only.
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
