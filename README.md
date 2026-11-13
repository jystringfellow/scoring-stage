# scoring-stage

`stage` runs coding agents (Claude Code, Codex, Copilot CLI, Antigravity, and anything else packaged as a kit) in
[Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) microVMs with every permission prompt off. The sandbox is
the boundary:

- **Egress is deny-all** apart from what the sandbox's kits allow, enforced by the sbx proxy.
- **Credentials stay on the host**, with the exceptions under [Limitations](#limitations). The sandbox sees
  placeholders; the proxy adds the real values to outbound requests. AWS credentials come from a host broker that
  serves only short-lived keys, and only for the profiles granted to that sandbox.
- **Pushes happen from the host.** `git push` over HTTP or HTTPS from the sandbox is refused at the proxy;
  `stage ship` pushes the sandbox's commit from the host, to a branch under `agent/` unless you set another prefix. GraphQL writes are not refused; see
  [Limitations](#limitations).
- **Each task gets its own scratch clone**, so the agent never touches your working copy.

Organizations adapt it with **books**: a directory of [kits](https://github.com/docker/sandbox-kit-spec) plus the
host-side wiring they need (secrets, credential bindings, AWS profiles, per-repository additions). Like the book a
session player is handed for a gig, a book holds what the stage needs for one kind of work.

stage works with repositories on GitHub.com only: `stage up` refuses a repository whose `origin` is anywhere else,
and `stage ship` pushes to `https://github.com/`. It runs on Unix hosts: macOS, Linux, and Windows through WSL.

> Status: early. Some of it has been checked against live sandboxes on sbx 0.51; much is covered only by unit tests
> with fakes. [VERIFICATION.md](VERIFICATION.md) says which. The command line and book format may still change.

## Requirements

macOS or Linux with `sbx` 0.51.0 or later (signed in), `git`, and optionally `gh` (Copilot credentials, `gh`
mode and `stage ship --pr`) and the `aws` CLI (the AWS broker). Building `stage` needs Go 1.26 or later.

## Install

`stage` is a single binary: the core kits are embedded in it and extracted to `~/.scoring-stage/kits/<content
hash>/` the first time `stage up` needs them. On Windows, install and run the Linux binary inside WSL.

**From a release.** [Releases](https://github.com/jystringfellow/scoring-stage/releases) carry a tar.gz for
`darwin_amd64`, `darwin_arm64`, `linux_amd64` and `linux_arm64`, each holding `stage`, `LICENSE` and `README.md`,
plus a `SHA256SUMS` file. Download with curl rather than a browser, so macOS does not quarantine the binary:

```sh
version=v0.0.1
asset="stage_${version}_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
base="https://github.com/jystringfellow/scoring-stage/releases/download/$version"
curl -fsSLO "$base/$asset.tar.gz" -O "$base/SHA256SUMS"
grep " $asset.tar.gz\$" SHA256SUMS | shasum -a 256 -c -    # or sha256sum -c -
gh attestation verify "$asset.tar.gz" --repo jystringfellow/scoring-stage \
  --signer-workflow jystringfellow/scoring-stage/.github/workflows/release.yml --source-ref "refs/tags/$version"
tar -xzf "$asset.tar.gz" && install -m 755 "$asset/stage" ~/.local/bin/stage
```

Each archive has a [build provenance attestation](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations)
from the release workflow. With these flags, `gh attestation verify` checks that `release.yml` in this repository
built the archive from the tag you asked for, and prints the commit it was built from. `SHA256SUMS` catches a
corrupted download, not a tampered one; the attestation is the provenance check.

**With Go** (1.26 or later):

```
go install github.com/jystringfellow/scoring-stage/cmd/stage@latest
```

or, from a clone, `go build -o bin/stage ./cmd/stage`. `stage version` prints the version.

## Use

```
stage setup --agent claude --agent codex  # save the agents you use; check sbx, the policy and credentials
stage init --agent claude --book backend --kit rules  # draft and check this repository's config and an empty kit
stage up ~/Code/api fix-login        # sandbox stage-fix-login on a scratch clone of ~/Code/api
stage up ~/Code/api fix-login --agent agy --book dotnet  # add an agent or an installed book for one box
stage up ~/Code/api fix-login --aws-profile staging    # grant an AWS profile to one box and make it the default
stage up ~/Code/api fix-login --plan # write the env file and show sbx's plan without creating (stops first if approvals are missing)
stage shell fix-login                # login shell in the repository; run claude, codex or copilot
stage exec fix-login -- git log -1
stage put fix-login ./notes.txt '~/notes.txt' # copy one file into the sandbox as the agent
id=$(stage exec --detach fix-login -- sh -c 'sleep 150; echo done; exit 7')
stage job list fix-login --json
stage job status fix-login "$id" --json
stage job output fix-login "$id" --follow
stage job wait fix-login "$id" --timeout 10m # exits with the command's status
stage job stop fix-login "$id"       # alternatively, stop it while it is still running
stage ship fix-login [--pr]          # review and push the sandbox's HEAD to agent/fix-login/<sha> (or your [ship] branch_prefix) from the host
stage list
stage login fix-login                # save the sandbox's agy login for future sandboxes (copy_logins)
stage take fix-login                 # copy the sandbox's takes to ~/.scoring-stage/takes/fix-login/ now
stage down fix-login                 # keep the sandbox's takes, then remove it, its scoped secrets and the scratch clone
stage broker start|stop|status       # the AWS broker (up starts it when a sandbox needs it)
stage broker serve                   # run the broker in the foreground
stage token set acme                 # save a fine-grained token for owner acme ([github] mode = "fine-grained")
stage github login                   # log in to your GitHub App ([github] mode = "app")
stage github token                   # print a fresh app token; sbx runs this on the host
```

`stage ship` pushes to `<prefix><slug>/<first 8 characters of the sha>` with your host git credentials; `--branch`
names another branch, which must also start with the prefix. The prefix is `agent/` unless you set `branch_prefix`
under `[ship]` in your config. It must be a namespace, one or more legal branch name components ending in `/`
(`bot/`, `team/agents/`), because the prefix is what keeps `ship` off the branches people work on; a
repository or book cannot set it. The rest of this section writes `agent/` for it. `ship` shows the commits and a diffstat, with control
characters escaped, and asks before pushing (`--yes` skips the question). It refuses changes to CI and workflow
files (`.github/`, `azure-pipelines*.yml`, `.azure-pipelines/`, `.pipelines/`, GitLab, Jenkins, CircleCI,
Buildkite), files that run code when someone opens or
checks out the branch (`.devcontainer/`, `.vscode/`, `.husky/`, pre-commit and lefthook configs, `.envrc`, mise and
asdf tool files), `.gitmodules` and `.scoring-stage/`, unless you pass `--allow-protected-paths`: CI runs a pushed
branch's workflow files with the repository's secrets, and `.scoring-stage/` widens future sandboxes.
`protected_paths` under `[ship]` adds your own paths to that list; nothing removes a path from it. An entry is
matched as written and case-sensitively against paths from the repository root: `Makefile` is that one file, and
`deploy/`, with its trailing `/`, is everything under that directory. Patterns, `./`, `..` and absolute paths are
refused. A rename counts as a change to the path it moves from. Azure
Pipelines can read YAML from any path a pipeline names, so the file check is a heuristic there; use branch policies
so `agent/*` branches cannot trigger pipelines that hold secrets. Pushing an `agent/*` branch triggers whatever CI
the repository runs on branch pushes, so that CI should not hold privileged secrets for unreviewed branches: on
GitHub, keep secrets in environments with required reviewers or deployment branch rules, or limit push triggers to
protected branches. Commits carry your git identity; with `warn_missing_trailer = true` under `[ship]`, `ship`
warns about commits without a `Co-authored-by` trailer.

`stage ship --pr` opens a draft pull request with `gh`. Its body always starts by saying that an AI coding agent
in a scoring-stage sandbox produced the change, and always names the commit. `pr_body` under `[ship]` replaces the
paragraph between the two, which by default says the changes were generated by an LLM and have not yet been
reviewed by a human; `pr_body = ""` removes that paragraph. `pr_footer` adds a last paragraph. Neither may hold
invisible characters or open an HTML comment (`<!--`), which would hide what follows it.

Git on the host runs with hooks and fsmonitor off. `stage up` records a hash of the scratch clone's git config
(`.git/config`, `.git/info/` and related files) after sbx creates the sandbox, and `ship` refuses to run git there
if it changed, or if it includes other files. The hash leaves out the `sandbox-<sandbox>` remote while it has exactly
the shape sbx writes (`git://127.0.0.1:<port>/src` and its two fetch refspecs), since sbx removes it when the sandbox
stops and adds it on a new port when it starts. The env file asks sbx to clone the scratch clone into the sandbox
(`workspace.clone`) rather than share it; `tests/acceptance.sh` checks that the sandbox's writes to its `.git` do not
reach the host scratch clone, and that the host source mount, if sbx adds one, is read-only.

`stage up` clones the repository to `~/.scoring-stage/tasks/<slug>/src`, writes an
[sbx environment file](https://docs.docker.com/ai/sandboxes/configuration/environment-files/) next to it and runs
`sbx env create`. The sandbox sees the repository at the scratch clone's host path. `stage down` deletes the scratch
clone, so ship anything you want to keep first.
The scratch clone's `origin/*` refs come from GitHub as of `stage up`.

## First task

One agent, no orchestrator, from an empty sandbox to a pushed branch. It assumes [one-time setup](#one-time-setup)
is done, here with `stage setup --agent claude --github fine-grained` and a token saved with `stage token set`.

```
stage up ~/src/api fix-login         # on the host: scratch clone and sandbox stage-fix-login
stage shell fix-login                # a login shell in the sandbox, in the repository
  claude                             # in the sandbox: run the agent; the first run asks you to sign in
  git log -1                         # in the sandbox: the agent's work must be committed to be shipped
  exit
stage ship fix-login --pr            # on the host: review the commits, push agent/fix-login/<sha>, open a draft PR
stage down fix-login                 # on the host: remove the sandbox and the scratch clone
```

- **Commit inside the sandbox.** `ship` pushes a commit, by default the sandbox's `HEAD`; uncommitted changes stay
  behind and are deleted by `stage down`. The sandbox cannot push, so nothing leaves it until you ship.
- **A sandbox stops shortly after its last session ends.** sbx stops it 30 seconds after the last `stage shell` or
  `stage exec` exits. A stopped sandbox keeps its files, and the next `stage exec` or `stage shell` starts it again.
- **`ship` needs the sandbox running.** It fetches the commit from the sandbox over a git remote that sbx removes
  when the sandbox stops, and it does not start the sandbox. If `ship` says there is no `sandbox-stage-<slug>`
  remote, keep a `stage shell` open in another terminal while you ship.

To run an agent without a terminal, give its non-interactive command to `stage exec`, for example:

```
stage exec fix-login -- claude -p "Fix the login redirect and commit the change"
```

The command runs in the repository as the sandbox user. For unattended work that outlives the caller, use
`stage exec --detach fix-login -- claude -p "Fix the login redirect and commit the change"` and read its job with
`stage job`. Its host process holds an attached sbx session until the command ends; see [Jobs](#jobs).
Work left in the background inside a sandbox still stops with the sandbox; see [Limitations](#limitations).

## Takes

A sandbox's files go with it on `stage down`. A **take** is what you keep: list paths inside the sandbox, and stage
copies them to the host before removing it, and whenever you run `stage take <slug>`.

```toml
takes = ["~/.tool/runs", "/var/log/tool"]   # in ~/.scoring-stage/config.toml, a book.toml or a repository's config
```

A path starts with `/` or `~/` (the sandbox user's home) and has no `..`. A path that does not exist in the sandbox
is passed over, a listed path that is a link is followed, and paths that overlap are kept once. Each copy lands in its own directory,
`~/.scoring-stage/takes/<slug>/<UTC time>/`, under the path it had in the sandbox, such as
`home/agent/.tool/runs/`. A sandbox uses the takes it was created with; a later change to a config applies to the
next `stage up`. A book's takes apply to every sandbox the book is turned on for; a `[[repos]]` rule cannot add
takes.

The files come from a sandbox that ran with prompts off, so stage treats them as untrusted. It keeps regular files
and directories only, with modes 600 and 700, and says how many links and special files it left behind. A file
listed twice, which the sandbox's tar sends the second time as a link to itself, is kept once and not counted as
left behind. The copy stops and keeps nothing when:

- an entry would land outside the take's directory, or its name has control characters, is not UTF-8 or is over
  4,096 bytes;
- the take is over 1 GiB or 100,000 entries, or the sandbox sends more than 2 GiB in all, where overlapping paths
  and skipped links count each time they are sent;
- the sandbox has not finished within 10 minutes;
- the takes hold only links and special files.

When that happens during `stage down`, the sandbox is left in place; fix the cause and run it again, or pass
`--discard-takes` to remove the sandbox without its takes.

Each take has a `stage.json` at its root, written by stage, that says what the take belongs to: the slug and
sandbox, the GitHub repository, the base branch, the agents, the listed paths, the time of the take, and the
ships recorded on the task so far: each with its commit, branch, time and, when `--pr` made one in the task's
repository, the pull request. `stage down` deletes the task, so this file is what links a kept record to the branch
and pull request it produced. The list of ships is a record, not proof that nothing else was pushed; see
[Limitations](#limitations). A sandbox cannot supply the
file: an entry named `stage.json` at the root of what it sends, in any letter case, stops the copy.
If a task's own record cannot be read, `stage down` cannot tell which takes to keep, so it leaves the sandbox in
place unless you pass `--discard-takes`.

Takes are records for you and your tools to read. stage never copies them into a sandbox; only [gear](#gear),
which you list yourself, goes in. A repository's takes
need your approval like its kits, and `repo_kits = "deny"` in managed settings refuses them. The approval is for
that list in that repository: the same list in another repository asks again.

## Gear

Takes leave a sandbox; **gear** goes in. Gear is your own files on this machine, such as a tool's personal config
or a directory of presets, that stage copies into every new sandbox at `stage up`, so the sandbox uses your setup
instead of whatever a repository or kit would default to.

```toml
gear = [                                                 # in ~/.scoring-stage/config.toml only
  "~/.tool/config.toml",                                 # the same path under your home and the sandbox user's
  { from = "~/presets/sandbox", to = "~/.tool/presets" },  # or a different place on either side
]
```

**Everything you list is readable by the agent, which runs with prompts off.** List only what you would hand to
it. Credentials stay on the host, behind secrets and bindings, so stage refuses gear that is, is inside or holds:

- stage's own directory (`~/.scoring-stage`, or `STAGE_HOME`), which keeps tokens, saved logins and broker grants;
- a known credential location under your home: `.ssh`, `.aws`, `.azure`, `.gnupg`, `.kube`, `.netrc`, `.npmrc`,
  `.pypirc`, `.git-credentials`, `.vault-token`, `.pgpass`, `.my.cnf`, `.password-store`, `.terraform.d`,
  `.docker/config.json`, `.cargo/credentials` and `.cargo/credentials.toml`, `.gem/credentials`,
  `.m2/settings.xml`, `.claude/.credentials.json`, `.codex/auth.json`, `.config/git/credentials`, `.config/gh`,
  `.config/github-copilot`, `.config/gcloud`, `.config/sbx`, `.config/rclone`, `.config/op`,
  `.config/containers/auth.json`, `.local/share/keyrings`, `Library/Keychains`, the shell histories
  `.bash_history`, `.zsh_history` and `.local/share/fish/fish_history`, and every agent's login files;
- where these variables move one, when they are set: `AWS_SHARED_CREDENTIALS_FILE`, `AWS_CONFIG_FILE`,
  `KUBECONFIG` (each file in the list), `GNUPGHOME`, `DOCKER_CONFIG` (its `config.json`), `HISTFILE`, and
  `XDG_CONFIG_HOME` for the `.config` entries, in addition to the places above;
- anything outside one of those directories that a link inside it points to, such as the file behind
  `~/.aws/credentials` when a dotfiles manager links it.

So `~/.config`, `~/.docker`, `~/.cargo` and `~/.local/share` are refused whole; list the files or directories
beside the credential that you mean. The comparison follows links and ignores letter case. If stage cannot find
your home directory, cannot read one of these directories, or finds more than 100,000 entries in one, it refuses
all gear, since it could not make the check.

**The list is a guard against mistakes, not a guarantee.** stage does not look inside your files, so a token
written into a config file you list, or kept anywhere the list does not name, is copied like anything else. A
hard link to a credential is not detected. Everything that is copied is readable by the agent.

A `to` is refused when it is, is inside or holds one of an agent's login files, which stage copies and saves
itself with `copy_logins`, or the sandbox's workspace, where `stage ship` would find your files among the agent's
work.

These checks run at `stage up`, and `stage setup` reports what they would refuse. They do not stop other
commands, so a gear entry stage refuses never keeps you from `stage down`, `stage take` or `stage ship` on a
sandbox you already have.

Only your own config may list gear. A repository's config, a book and a `[[repos]]` rule that list it are refused,
since any of them could otherwise have stage hand your files to an agent. There is no flag to leave gear out of
one sandbox; remove the entry before `stage up`.

A string entry starts with `~/`. In `{ from, to }`, each path starts with `/` or `~/` and has no `..`; `from` is on
your machine and `to` is in the sandbox, where `~/` is the sandbox user's home. Two entries cannot share a `to`.
What is copied:

- A regular file, or a directory with the regular files and directories under it. A listed path that is a link is
  followed. Links and special files inside a listed directory are never followed or copied, wherever they point,
  and `stage up` says how many it left out.
- A path that is not on this machine is passed over with one line. A listed path that is neither a file nor a
  directory, or a file or directory stage cannot read, stops `stage up` before the sandbox is created.
- At most 100 MiB and 10,000 files and directories across all gear. More stops `stage up` before the sandbox is
  created. A file that changes size, or turns into a link, between that check and its copy fails the copy.
  stage holds each listed directory open from the check to the copy, so a directory under it that is swapped for
  a link in between cannot bring in a file from outside.
- Files keep their owner's permission bits, so an executable stays one, and get none for group or others.
  Directories gear creates are mode 700; a directory that is already there, such as `~/.config`, keeps its mode.
  Everything is owned by the sandbox user, and written as that user, so a destination that user cannot write
  stops `stage up`.

Your copy wins. Gear is unpacked over whatever is at the destination, so a file a kit wrote there is replaced, and
a directory is merged: your files replace those of the same name, and files only the kit wrote stay. Entries are
copied in the order listed, so a later entry wins over an earlier one it overlaps.

stage can first reach a sandbox when `sbx env create` returns, so that is when gear lands: through `sbx exec`, the
way saved logins are copied, before those logins and before stage says the sandbox is up. By then sbx has run
the kits' install commands, and may have run their `startup` commands for the first time, so a kit must not expect
your gear in either. A kit that needs a file there ships its own default, which your gear then replaces. Whatever
runs later sees your gear: `stage shell`, `stage exec`, the agent, and a `startup` command on a later start. Gear
is copied once: a later change on your machine reaches the next sandbox, not this one. None of this has been run
against a live sandbox yet; see [VERIFICATION.md](VERIFICATION.md).

The copy of all gear together has 5 minutes. If it runs out, if a copy fails, or if sbx finishes without the
sandbox confirming that it unpacked what stage sent, `stage up` fails: the sandbox is created and may hold only
part of your gear. It is still a working sandbox, but stage does not call it up; fix the cause, then run
`stage down <slug>` and `stage up` again. `stage up --plan` shows what would be copied, and `stage setup` shows the
setting. An administrator can refuse gear with `allow_gear = false` in [managed settings](#managed-settings).

## Put

`stage put <slug> <file> [<destination>]` copies one regular host file, including binary content, into an existing
sandbox and says where it went. The destination names the file, defaults to `~/<basename>`, and replaces an
existing file. Quote `~/` so your host shell does not expand it: `stage put fix-login ./notes.txt '~/notes.txt'`.
The copy runs as `agent`, owns the file as that user, and retains only its owner's permission bits. Parent
directories are created as needed. It uses the same tar stream, 100 MiB limit and five-minute timeout as gear;
file content never appears on a command line.

Put shares [gear's](#gear) source and destination checks, including credential locations, links followed on the
host, stage's own directory, agent login files, the sandbox workspace and managed `allow_gear = false`.
The destination starts with `/` or `~/`, has no `..`, doubled or trailing slash, and has no control characters.
A missing file, directory or special file is an error. A destination the agent cannot write fails the copy.
Like gear, these path checks do not inspect file contents or detect hard links to credentials.

## Jobs

`stage exec --detach <slug> -- <command> [args]` starts a job and returns immediately. `--detach` may also go
directly after the slug. Stdout contains only its numeric job id; a wait hint goes to stderr. The command runs as
`agent` in the repository, as with ordinary exec, with no terminal and stdin from `/dev/null`.
Without `--detach`, exec behaves as before.

Each job has one host process running as you, holding an attached `sbx exec` for exactly that job. There is no
shared daemon or listening port. Its arguments contain only `job hold <slug> <id>`; command arguments are kept in
the private job record. Output and status live under `~/.scoring-stage/tasks/<slug>/jobs/<id>/`, in mode-700
directories and mode-600 files: `job.json` (command and start time), `output` (merged stdout and stderr),
`exit.json` (result and end time), `lock` (holder liveness), and `stop` when stopping was requested.

- `stage job list <slug> [--json]` lists jobs in id order with state, status when available, and command.
- `stage job status <slug> <id> [--json]` reads one job. States are `running`, `exited` with a numeric status,
  `stopped` after a stop request, and `lost` when the holder disappeared without recording a result.
- `stage job wait <slug> <id> [--timeout 10m] [--json]` waits indefinitely unless given a timeout and exits with
  the job's status, 124 on timeout while still running, or 125 for `lost` or `stopped`. A command may itself exit
  124 or 125; JSON's state distinguishes those cases. A timeout does not stop the job.
- `stage job output <slug> <id> [--follow]` prints saved output, or follows until the job leaves `running` and
  drains the remaining bytes. Control characters are escaped as `\xNN`, including terminal escapes, carriage
  returns and bidi marks; newlines remain newlines. Invalid UTF-8 bytes are escaped too. Stage never interprets
  the output. Capture is capped at 1 GiB per job, after which bytes are dropped and the result records
  `output_truncated`; reading truncated output prints a note on stderr. Follow exits 0 even when the job failed.
- `stage job stop <slug> <id>` asks a running holder to terminate its sbx client, then kills that client after
  five seconds if needed. It waits for the result, keeps output, and reports `stopped`, or the real exit if the
  command finished first. Stopping a finished or lost job is an error.

JSON status and wait emit one object; list emits an array (empty is `[]`). They carry `id`, `slug`, `sandbox`,
`state`, `command` as an argv array, `started_at`, `ended_at` when known, `status` only for `exited`,
`output_bytes` and `output_truncated`. Failure to start sbx is recorded as exit 127 with its error in the output.

`stage down` refuses while any job is running, before copying takes, saving logins or removing anything, even
with `--discard-takes`. Wait for or stop those jobs first. When the sandbox is already gone, down proceeds.
Finished and lost jobs do not block down; their host records and output are deleted with the task. Takes preserve
only the sandbox paths already listed, and do not copy host job records. Output and status survive a sandbox
stopping until down removes the task. Put and jobs have been tested against fakes only; the host maintainer must
check real sbx lifetime, exit status propagation and stop behavior.

## One-time setup

1. **Global deny-all policy.** A sandbox's egress comes from its kits, so any global allow rule (such as sbx's
   default "balanced" lists) widens every sandbox. `stage up` refuses to run while global allow rules exist unless
   you pass `--allow-global-policy`. `stage setup --yes` runs `sbx policy reset --force` and
   `sbx policy init deny-all`; that deletes every local policy and stops running sandboxes.
2. **GitHub access (required choice).** `stage up` refuses to start until you choose how sandboxes reach GitHub,
   with `stage setup --github MODE` or `[github] mode` in `~/.scoring-stage/config.toml`. There is no default,
   unless [managed settings](#managed-settings) allow exactly one mode.

   | Mode | The sandbox gets | Use it when |
   | --- | --- | --- |
   | `fine-grained` | Your fine-grained token for its repository's owner, which you make read-only | You want sandboxes that cannot write to GitHub |
   | `app` | A token from your own read-only GitHub App, refreshed on the host | You want read-only access across several organizations without a token per owner |
   | `gh` | Your gh login (`gh auth token`), as a per-sandbox secret | You accept that the sandbox can write as you |
   | `none` | No GitHub credential | Public repositories only, or no GitHub API at all |

   **fine-grained.** A fine-grained personal access token belongs to one owner (a user or an organization), so
   stage keeps one per owner at `~/.scoring-stage/secrets/github/<owner>.token` (mode 600) and gives each sandbox
   only the token for its repository's owner. A sandbox works in one owner's repositories. stage checks that the
   token is fine-grained, that GitHub accepts it and when it expires, but it cannot see the token's permissions, so
   making it read-only is up to you. Limit it to the repositories you work on and give it only Contents: read and
   Metadata: read (add Pull requests: read if agents should read pull requests). A read-only token can still read
   every repository it is scoped to, so keep that list short. gh login tokens (`gho_`), classic tokens (`ghp_`) and
   GitHub App tokens are refused (use `app` mode for an app). The old single `~/.scoring-stage/secrets/github-readonly-token` file is no longer
   read; move it to `secrets/github/<owner>.token` for the owner it was created for.

   Save each token with `stage token set <owner>`. When stdin is a terminal it reads the token without echoing it;
   otherwise it reads stdin, so `pbpaste | stage token set acme` works. It refuses anything but a single fine-grained token,
   checks it with GitHub (`GET /rate_limit`) and refuses to save one GitHub rejects with 401 (if GitHub cannot be
   reached, it warns and saves the token unchecked), then writes
   `secrets/github/<owner>.token` atomically with mode 600, replacing any token already there. It never prints the
   token. A file you write by hand still works.

   stage reads a token's expiry from the `GitHub-Authentication-Token-Expiration` header GitHub sends on
   authenticated requests (a token with no expiry gets no header). `stage setup` reports each owner's token
   expiry and warns when it is within 7 days or GitHub rejects the token. In fine-grained mode `stage up` calls
   `GET /repos/<owner>/<repo>` with the sandbox's token: it refuses when GitHub answers 401 (expired or revoked),
   warns when GitHub answers 404 (the token does not include that repository, or was created for another owner) or
   403 (also: the token is not authorized for the organization's SAML SSO, or a rate limit), and warns when the
   token expires within 7 days. An expiry that reads as past on a token GitHub accepts is a warning to check the
   clock, not a refusal. If GitHub cannot be reached, `stage up` warns and
   continues. These checks send the token only to `api.github.com` and do not follow redirects; in the sandbox,
   the proxy adds it to requests for `api.github.com` and `github.com`.

   **app.** stage ships no GitHub App; you register your own (enterprise-owned, organization-owned or public all
   work) and install it on every organization whose repositories sandboxes work in. Configure the app with:

   - Repository permissions: Contents, Metadata, Pull requests, Issues and Actions set to Read-only, and no
     permission set to Read and write or Admin
   - Organization permissions: none, or Read-only
   - Account permissions: none. stage cannot check these: they act on your own account (such as Git SSH keys or
     email addresses) and do not appear in an installation's permissions. If one is granted, the stage kit's proxy
     rules still refuse REST writes to `/user/**` and `/gists/**` and every PUT, PATCH and DELETE to GitHub, but
     GraphQL mutations on your account are not refused
   - Enable Device Flow: on
   - Webhook: off
   - Expire user authorization tokens: on (recommended; stage also works without expiry)

   Then put its Client ID in `~/.scoring-stage/config.toml` and log in on the host:

   ```toml
   [github]
   mode = "app"
   app_client_id = "Iv23li..."
   ```

   ```sh
   stage github login     # shows a code to enter at https://github.com/login/device
   ```

   Under [managed settings](#managed-settings), app mode works only when `[github] allowed_modes` lists `"app"`;
   otherwise `stage up`, `stage github login` and `stage github token` refuse it, so sandboxes created earlier stop
   getting new tokens. Managed settings can also pin the app with `[github] app_client_id`.

   Login uses GitHub's device flow with the Client ID only; there is no client secret. The access token (`ghu_`,
   about 8 hours) and refresh token (`ghr_`, about 6 months) are saved with their expiry times in
   `~/.scoring-stage/secrets/github/app.json` (mode 600). Each sandbox gets a `github` secret whose command is
   `stage github token`, with `refresh: 30m` in its env file: sbx runs it on the host at most every 30 minutes, and
   it refreshes the access token when fewer than 40 minutes remain. The refresh token never enters the sandbox,
   and concurrent refreshes take a lock so a rotated refresh token is not lost. If a refresh fails because GitHub
   cannot be reached or answers with a server error, and the current token has more than 5 minutes left,
   `stage github token` warns and returns the current token; a refused refresh token fails at once. Because every
   `stage github token` also checks the app's installations (below), it fails whenever `api.github.com` cannot be
   reached.

   On login, on every `stage github token` (so at least every 30 minutes per sandbox) and on every `stage up`,
   stage lists the app's installations (`GET /user/installations`) and refuses, removing the saved login, if any
   installation grants a write or admin permission; the error names the account and the permission. stage has no
   client secret, so it cannot revoke tokens already handed out: they stay valid at GitHub until they expire, and
   sbx may keep serving a cached one, so run `stage down` on running sandboxes. `stage setup` and `stage github login` list the
   accounts the app is installed on: a sandbox can read what the app and you can both read in all of them, so app
   mode is not limited to one owner per sandbox. If the refresh token expires or is revoked, or `app_client_id`
   changes, run `stage github login` again.

   App mode has been tested only against a simulated GitHub, not yet against a registered app; in particular,
   whether `/user/installations` reports every permission a user token carries is unverified. That sbx re-runs a
   secret's command on its `refresh` interval is also unverified: if it does not, a sandbox loses GitHub access
   when its first token expires.

   **gh.** stage warns on `stage setup` and every `stage up` in this mode. GraphQL lets a sandbox do anything your gh
   login can, in every repository it can write to: commit to and move or delete branches (`createCommitOnBranch`,
   `updateRef`, `deleteRef`), open and merge pull requests, comment and change settings. In effect that is a push;
   the proxy refuses only `git push` and REST writes. Under [managed settings](#managed-settings), gh mode is
   refused unless `[github] allowed_modes` lists `"gh"`.

   `stage up` also refuses while sbx has a global `github` secret (`sbx secret ls --global`), because any sandbox
   with a `github` binding, including one saved by an earlier sandbox, can fall back to it. Remove it with
   `sbx secret rm github -f`. Your config and books cannot set the `github` secret or binding, and stage refuses any
   of their bindings on `github.com`, `github.ai`, `githubusercontent.com` or their subdomains, apart from
   `copilot.github.com`. A wildcard domain is refused when the literal text after its last `*` could end a GitHub
   host name, and a domain with any other glob character (`?`, `[`, `]`, `{`, `}`, `\`) is refused.

   The proxy refuses `git push` over HTTP and HTTPS; PUT, PATCH and DELETE to `api.github.com`, `github.com` and
   `uploads.github.com`; and POST to the API's repository, user, organization, team, project, enterprise, gist,
   app and webhook-subscription endpoints (the list is in [`kits/stage/stage.yaml`](kits/stage/stage.yaml)).
   GraphQL writes share `POST /graphql` with reads, so only the token's own permissions stop those.
3. **Agent logins.** See [Agent logins](#agent-logins).
4. **Global credential bindings.** sbx merges every environment's bindings into the machine-wide
   `~/.config/sbx/credentials.yaml` (stage assumes `$XDG_CONFIG_HOME/sbx/credentials.yaml` when that is set, as sbx's
   own messages suggest; only the `~/.config` path is verified) and keeps them after the sandbox is gone. A binding
   authorises a service by name: any kit that declares `credential@1` for a bound service can have that service's
   secret injected on any domain the kit names. Outside stage sandboxes, a binding is inert unless its service also
   has a global secret (`sbx secret ls --global`); that pairing is the standing risk, even under a plain `sbx run`.
   `stage setup` (and `stage up --plan`) reads that file, never writes it, and lists each bound service with its
   domains, noting `global secret set` where there is one, and marks it:
   - `expected`: stage binds it for your agents, `[github]` mode, the AWS broker, or your config and books.
     Expected does not mean safe. If your agent sign-ins live as global sbx secrets (such as `anthropic` or
     `openai`), any kit run under a plain `sbx run` outside stage can redirect them, so "run only kits you trust"
     covers your own agent sign-ins too, not only the secrets stage creates;
   - `not configured`: stage binds it for an agent or mode you have not configured; remove it only if you
     no longer use that agent or mode;
   - `stale`: a stage service listing a domain that is not, as literal text ignoring case, one stage binds, such as
     `localhost` beside `localhost:9911`;
   - `other`: anything else. When it has a global secret, `sbx secret rm <service>` removes that secret.

   sbx has no command that withdraws one binding, so edit the file. Never pass `--prune-bindings` to `sbx env rm`
   for a stage sandbox: it deletes the whole shared service entry machine-wide.

## What a sandbox gets

Nothing you did not choose. Every sandbox gets:

| Kit | Purpose |
| --- | --- |
| `docker.io/sbx/shell` | Debian base with git, gh, curl, jq, Python and Docker |
| [`kits/stage`](kits/stage/stage.yaml) | Refuses `POST /**/git-receive-pack` on ports 443 and 80 of every host and GitHub REST writes, sets your git identity, tells the agent the rules |
| [`kits/aws-broker`](kits/aws-broker/aws-broker.yaml) | Only when AWS profiles are configured: `~/.aws/config` with a `credential_process` per profile, plus `[default]` when a default profile applies |

plus the agents chosen by your config, the repository, a book or `--agent`. Agents are Docker's published mixins,
each with its own egress and credentials:

| Name | Kit | Credential binding |
| --- | --- | --- |
| `claude` | `docker.io/sbx/claude-mixin` | `anthropic` (OAuth) |
| `codex` | `docker.io/sbx/codex-mixin` | `openai` (OAuth) |
| `copilot` | `docker.io/sbx/copilot-mixin` | `copilot`, from `gh auth token`, sent only to Copilot's API hosts |
| `agy` | `docker.io/sbx/antigravity-mixin` | none; see [Agent logins](#agent-logins) |

A kit reference works wherever a name does in your own config and `--agent`, for another agent or another version
of one of these. A bare reference brings no bindings, secrets or login files unless it is another version of a kit
an agent already has.

### Defining an agent

These four are defaults. Your `~/.scoring-stage/config.toml` can define more, or redefine one of them, in an
`[agent.<name>]` table. A defined agent is used like a built-in one: in `agents`, in a book's or repository's
`agents`, with `--agent`, with `stage setup --agent` and in `copy_logins`.

```toml
agents = ["claude", "example"]
copy_logins = ["example"]                            # only if you want its login files copied; see Agent logins

[agent.example]
kit = "registry.example.com/sbx/example-mixin:1.2.3" # required: a published kit, or a ./ directory under ~/.scoring-stage
login_files = [                                      # files in the sandbox that hold its sign-in
  "/home/agent/.example/token",
  { path = "/home/agent/.example/onboarding.json", optional = true },
]

[agent.example.bindings.example]                     # written like [bindings.<service>]
apiKey = { domains = ["api.example.com"] }

[agent.example.secrets.example]                      # the default secret; [secrets.example] replaces it
command = "{gh} auth token"                          # {gh} is the gh CLI stage found
```

- `kit` is the only required key. A name is lower-case letters, digits, `-` and `_`.
- A published kit reference, here and anywhere else stage reads one, is written in lower case up to its tag. A
  registry reads an upper-case first part as a host, so stage refuses upper case there and does not guess.
- Service names are lower case with no spaces around them, in an agent's tables and in your own `[secrets]` and
  `[bindings]`; any other spelling is refused with the one to use.
- Defining a built-in name replaces that agent. `bindings`, `secrets` and `login_files` you leave out keep the
  built-in values, so `[agent.claude]` with only `kit = "docker.io/sbx/claude-mixin:<version>"` moves Claude Code to
  another version of its mixin. An empty table or list clears the built-in value.
- A kit belongs to one agent: two agents cannot use the same kit, whatever its version.
- A secret is `command`, a host command that prints the value, with an optional `refresh`. In a command, `{gh}`
  stands for the gh CLI stage found on your machine. Write it unquoted as a word of its own, as in
  `{gh} auth token`: stage puts gh's shell-quoted path in its place, and refuses a command that has `{gh}` inside
  quotes, escaped or joined to other text. When gh is not found, `stage up` leaves that secret out and says so; the
  agent then has its binding but no credential.
- When two agents in one sandbox set the same service differently, `stage up` stops, with or without gh, unless
  your own `[bindings.<service>]` or `[secrets.<service>]` settles it.
- A login file is an absolute path in the sandbox. stage reads and writes it as the sandbox's user, and it need not
  be under that user's home. stage keeps it in `~/.scoring-stage/logins/<name>/` under its file name, so two login
  files of one agent cannot share a file name, whatever its letter case. A file marked `optional` is carried when
  the sandbox has it.
- An agent cannot set the `github` or `stage-broker` service, however the name is cased or spaced, and its bindings
  cannot cover GitHub's hosts.
- Only your own config defines agents. A book or a repository that has an `agent` table is refused; both can only
  name agents. The services an agent you define binds or supplies are reserved like a built-in agent's, so no book or
  repository kit can claim them.
- Under [managed settings](#managed-settings), an agent list (`allowed_agents` or `denied_agents`) refuses every
  definition except one that only sets `kit`, to another version of a built-in agent's own kit. A `copy_logins`
  list refuses any definition that sets `login_files`, and any definition that puts a name in that list on another
  kit. The names an administrator lists therefore keep their meaning.

The `github` binding (in `fine-grained`, `app` and `gh` modes), an agent's bindings and, with AWS, `stage-broker` are written to sbx's
`~/.config/sbx/credentials.yaml`. A binding only authorises a service's credential for use; it does not limit
where the credential goes. Kits decide that: any kit in a sandbox that declares a credential for a service with a
binding on this machine has the secret injected on the hosts the kit names, whichever config or book made the
binding. Bindings are shared by every sandbox, so `stage down` leaves them in place and never prunes them; pruning
one sandbox's bindings removes them for every sandbox.

## Agent logins

| Agent | Sign-in | Where the token lives |
| --- | --- | --- |
| `copilot` | Nothing to do: a per-sandbox secret runs `gh auth token` | Host |
| `codex` | `sbx secret set openai --oauth` on the host | Host; the proxy holds it |
| `claude` | Run `claude` in the sandbox and choose a login method | Host; the proxy holds it |
| `agy` | Run `agy` in the sandbox and complete Google's sign-in | **Inside the sandbox** |

Docker's Antigravity kit has no proxy-managed Google sign-in, so agy's token is a file in the sandbox
(`~/.gemini/antigravity-cli/antigravity-oauth-token`) that the agent can read. Choose one of:

- **Sign in per sandbox** (the default). The token exists only in that sandbox and goes away with `stage down`;
  each new sandbox needs a new sign-in.
- **Copy one sign-in into every sandbox.** Add `copy_logins = ["agy"]` to `~/.scoring-stage/config.toml`, sign in
  once in `stage shell`, then run `stage login <slug>`. stage keeps the token in
  `~/.scoring-stage/logins/agy/` (mode 600), copies it into each new sandbox that has agy, and saves it again on
  `stage down` so a refreshed token is kept. Every sandbox with agy then holds a live copy of your Google sign-in.
  stage also carries agy's record that its first-run setup (color scheme and terms) is done,
  `~/.gemini/antigravity-cli/cache/onboarding.json`, once you have finished that setup in a sandbox. Without it
  agy stops at that setup screen in every new sandbox, which an unattended run cannot get past.

The kit also has a Gemini API-key mode that would keep a key on the host: a `google` secret plus
`[bindings.google]` with `apiKey = { domains = ["generativelanguage.googleapis.com"] }` in your config. It uses the
Gemini API and the key's project billing rather than your Google account's Antigravity access, and has not been
tested with stage.

## Configuration and books

Configuration has two levels: yours in `~/.scoring-stage/config.toml`, which applies to every sandbox you create
and may set everything below, and a repository's in its `.scoring-stage/config.toml`, which travels with the code and may
only ask for agents, installed books, its own kits and takes. [Gear](#gear) is yours alone: neither a repository
nor a book may list it.

`~/.scoring-stage/config.toml` (all keys optional):

```toml
agents = ["claude", "codex"]                       # the agents you use; names or kit references
books = ["~/Code/my-org-books/backend"]            # installed books, relative to ~/.scoring-stage; each applies only when asked for
base = "docker.io/sbx/shell:1.1.0"                 # the workload kit
kits = ["./kits/personal"]                         # extra kits, relative to ~/.scoring-stage
takes = ["~/.tool/runs"]                           # sandbox paths kept on your machine; see Takes
gear = ["~/.tool/config.toml"]                     # your files copied into every new sandbox; see Gear

[github]
mode = "fine-grained"                              # or "app" (with app_client_id), "gh" (your gh login, which can write) or "none"

[aws]
profiles = ["sandbox"]
region = "us-west-2"

[secrets.openai]
command = "op read op://Private/openai/key"

[bindings.openai]
apiKey = { domains = ["api.openai.com"] }

[ship]
branch_prefix = "agent/"                           # every shipped branch starts with this; must end in /
protected_paths = ["deploy/", "Makefile"]          # refused like CI files, in addition to stage's own list
warn_missing_trailer = true                        # warn about commits without a Co-authored-by trailer (default false)
pr_body = "Review against the team checklist."     # replaces the default text after the AI disclosure in a --pr body
pr_footer = "Generated with an AI coding agent."   # last paragraph of a --pr body
```

A book is a directory with a `book.toml`:

```toml
name = "backend"
agents = ["claude"]                                       # agents this book's work needs
kits = ["./kits/go"]                                      # local kits only, starting with ./ or ../

[aws]
profiles = ["dev", "staging"]
region = "us-west-2"
default_profile = "dev"                                   # also written as [default]; must be granted

[secrets.artifacts]
command = "python3 -I {book}/host/feed-token.py"          # {book} is the book directory

[bindings.artifacts]
apiKey = { domains = ["pkgs.example.com"] }

[[repos]]
match = ["example/api", "Example.Api"]                    # globs on owner/name or the directory name
kits = ["./kits/api-rules"]
aws = { profiles = ["api-dev"] }
```

An installed book applies to a sandbox when the repository's config names it, when `--book` names it, or when one
of its `[[repos]]` entries matches the repository; the `[[repos]]` rules are for repositories you cannot add a
config to. `--book ./dir` also uses a book that is not installed.

The `book.toml` above shows every kind of entry; its feed, profiles and `[[repos]]` kit are placeholders.
[`examples/books/go`](examples/books/go) is a complete book you can run: one local kit that installs a Go toolchain
and allows the hosts `apt` and the Go module proxy need. Try it with
`stage up <repo> <slug> --book ./examples/books/go`, or copy it as the start of your own. It passes stage's checks
on a book; its install steps follow a kit that runs in a sandbox, but this copy has not been run in one.

A book's `name` matches `^[a-z0-9][a-z0-9._-]*$`, and its `description` has no control characters. A book cannot
set `base`. It cannot set a secret or binding for a reserved service: any service an agent binds (`anthropic`,
`openai`, `copilot`), sbx's built-in services (such as `google`, `groq` or `xai`), `github` or `stage-broker`. It may
bind only services it supplies a `[secrets.<service>]` for. As a further check, its bindings' domains cannot cover a
reserved service's hosts, but binding domains do not limit where a credential goes, so this is not a boundary.

Kits, not bindings, decide where a credential is injected, so the rules that matter are on kits. Books and
repository configs may use only local kits: a published kit cannot be inspected, and a kit can send any credential
sbx holds to any host. That includes agents given as kit references; books and repositories name agents from the
agent table. Each credential a book's local kit declares must be one the book supplies in `[secrets.*]` and not a
reserved service. A repository's local kits may declare no credentials at all, since a repository supplies none.
Service names are compared in lower case without surrounding spaces, and names with control or invisible characters
are refused. Kits that extend another kit (`extends:`), or have capabilities stage does not recognise, are refused.

**This fence is not a boundary against a malicious book.** It stops a book from silently rebinding the credentials
stage sets up, and it guards against mistakes. A book's secret commands still run on your machine with all your
access: `[secrets.feed] command = "gh auth token"`, with the book's own kit injecting `feed` on a host the book
controls, sends your GitHub token there. Turn on only books from protected repositories whose changes are reviewed.
That review is the real control. See [Limitations](#limitations).

A repository's `.scoring-stage/config.toml`, read from the branch the sandbox starts from:

```toml
agents = ["claude", "codex"]     # the agents this repository's contributors run
books = ["backend"]              # installed books, by name
kits = ["./.scoring-stage/kits/rules"]  # inside the repository; approved by you before use
```

Draft this file with `stage init [<repo>] [--agent NAME]... [--book NAME]... [--kit NAME]... [--dry-run]`;
`<repo>` defaults to the current directory. Repeated flags keep their first occurrence. Only named agents,
installed books and scaffolded kits appear in the draft, in that order; takes are omitted. Without `--agent`,
the `agents` key is omitted, so the repository asks for no agents; your own configured agents still apply.
With no flags and no existing config, nothing is written. Unnamed installed books are listed with their
descriptions, along with the known agent names. An unknown book is refused with the installed choices listed.

`--kit rules` adds `"./.scoring-stage/kits/rules"` to `kits` and writes
`.scoring-stage/kits/rules/rules.yaml`: a spec v3 mixin with no capabilities, hosts or credentials. A kit name
must be one lowercase path component, using letters, digits, dots, underscores or dashes, and start with a
letter or digit. Add the hosts you need yourself; see [Writing a kit](#writing-a-kit).

`init` never edits an existing file. If the config exists, all scaffolding is skipped and the existing config
is checked; flags missing from it are reported for you to add yourself. An existing kit file is kept when
creating a new config. Symbolic links and targets that are not regular files are refused. Before writing,
`init` checks the draft with the same strict decoding, composition and repository-kit rules as `stage up`.
It reports the agents, books, kits and takes a sandbox would get, and shows missing approvals using
`stage up --plan`'s terms. It approves nothing, calls no sbx command and leaves the stage home unchanged.
`--dry-run` prints the exact proposed file contents and performs that same check without writing them.

Review the files and commit them directly on the branch sandboxes start from, since `stage up` reads the
config from that branch, then run `stage up <repo> <slug>`. `stage ship` refuses changes under
`.scoring-stage/` unless you pass `--allow-protected-paths`.

A repository is not trusted with anything that acts on the host. It can add agents by name, installed books by
name, its own kits and [takes](#takes), but it cannot set secrets, bindings, AWS profiles, GitHub access, [gear](#gear) or the base. A book it asks
for that you have not installed is an error. A repository's kit is a `./` path inside the repository, with no
symbolic links; published kits are refused.

Kits and books can widen a sandbox's network access, send credentials and run commands, so `stage up` uses the
kits a repository adds, and the installed books it turns on, only after you approve them. It lists each one with
what it does (network rules, credentials, host commands, AWS profiles) and asks. Without a terminal, pass
`--approve-repo-config` after reviewing them. An approval covers that exact content (a local kit's files, a published
kit's digest and arguments, a book's files) and is kept in `~/.scoring-stage/trusted-kits`, one JSON object per
line, so a change asks again. `stage up --plan` lists what needs approving and stops before writing the env file.

Known gap: a book you turn on yourself, with `--book` or a `[[repos]]` rule, is not reviewed. Its host commands
(such as `secrets` commands) run as code on your host, with whatever the book's files say after your last
`git pull`. Until books you turn on are approved by content, only turn on books from repositories whose main branch
is protected and reviewed.

Books and both configs are decoded strictly: an unknown key is an error. Two books that set the same
region, AWS default profile, secret or binding differently are an error rather than a silent winner. Your config
overrides books.

The sandbox's `~/.aws/config` has a `[default]` profile when a default profile is set, or when exactly one profile
is granted and nothing names one, so the `aws` CLI and SDKs work without `AWS_PROFILE`. The default must be one of
the granted profiles; set `default_profile` in your `[aws]` table to choose another. `stage up --aws-profile NAME`
grants a profile from your host AWS config to that one sandbox and makes it the default, over books and your config.

A kit that a book adds for its own service declares a `credential@1` entry for that service and names the hosts it is
injected on; the book's `secrets` entry supplies the value for each sandbox, and its `bindings` entry authorises the
service.

### Writing a kit

[`examples/books/go/kits/go/go.yaml`](examples/books/go/kits/go/go.yaml) is a small kit to start from. Two things
about the sandbox have caught kits in use; both come from one kit's notes and have not been checked again for
this README:

- **Install Python tools with pip in a venv, not with uv.** uv aborts on the sandbox's 16 KB memory pages.
  `python3 -m venv <dir>` and that venv's `pip install` work.
- **Start a long-running server from a `startup` command inside a `tmux` session.** A process started straight
  from the hook has no terminal; one started with `tmux new-session -d -s <name> <command>` gets a full terminal
  environment from the tmux server. Set `TERM`, `SHELL` and `LANG` first, and guard with `tmux has-session` so a
  second start does not fail. The kit has to install `tmux`. Like everything else in the sandbox, the server stops
  when the sandbox does, and the `startup` command runs again on the next start.

## Managed settings

An administrator can limit what every user, book and repository on a machine may choose with a managed settings
file, much like Claude Code's `managed-settings.json`. stage reads it from:

| Platform | Path |
| --- | --- |
| macOS | `/Library/Application Support/scoring-stage/managed.toml` |
| Linux | `/etc/scoring-stage/managed.toml` |
| WSL | `/etc/scoring-stage/managed.toml`, and `C:\ProgramData\scoring-stage\managed.toml` (read as `/mnt/c/ProgramData/...`) so Windows device management can supply it |

stage on Windows runs inside WSL. When more than one file exists, each can only narrow the others. Every key is
optional, and an unknown key is an error:

```toml
allowed_agents = ["claude", "copilot"]   # only these agents
denied_agents = ["agy"]
repo_kits = "deny"                       # "ask" (default) or "deny": refuse kits and books a repository asks for
copy_logins = []                         # agents whose logins may be copied between sandboxes
allow_global_policy = false              # refuse stage up --allow-global-policy
allow_gear = false                       # refuse gear: no user files are copied into sandboxes

[aws]
allowed_profiles = ["dev-*", "sandbox"]  # glob patterns a granted profile must match
denied_profiles = ["*prod*", "*admin*"]

[github]
allowed_modes = ["fine-grained", "none"] # GitHub modes users may choose; [] allows none
app_client_id = "Iv23li..."              # the only GitHub App app mode may use
```

`[github] allowed_modes` limits the modes in `[github] mode`, and `stage setup --github` refuses any other mode
before saving anything. A managed file without `allowed_modes` adds no constraint, so the lists of the files that do
set it decide. When managed settings are in place but none sets `allowed_modes`, only `fine-grained` and `none` are
allowed: an administrator has to list any other mode, such as `"gh"`, to allow it. When exactly one mode is
allowed and the user has not chosen one, stage uses it without asking. An empty list allows no mode, so `stage up`
refuses every sandbox.

`[github] app_client_id` pins the GitHub App that app mode uses. Users then need no `app_client_id` of their own; one
that differs from the pin is ignored with a warning, and a saved login for any other app is refused, so
`stage github token` stops handing its tokens to sandboxes. Managed files that pin different apps are an error.
Without a pin, users under managed settings can point app mode at any app, and only stage's read-only installation
check stands between them and an app that can write; `stage setup` warns when `allowed_modes` allows `"app"` without
a pin, and when a pin is set but app mode is not allowed.

`allow_gear = false` refuses [gear](#gear): `stage up` refuses while the user's config lists any, and
`stage setup` warns. Other commands still run, so existing sandboxes can be shipped, taken from and removed.
Without the key gear is allowed, and a `false` in any managed file wins. It is all or nothing; managed settings
cannot allow some paths and refuse others.

`stage setup` shows the managed settings in effect and warns about a managed file or directory that is not owned by
root or that its group or others can write. A managed file stage cannot read stops stage rather than being skipped. They only
restrict, so they guard against mistakes and record policy; a user with administrator rights on the machine can
still remove the file.

Agent policy covers the agents stage puts in a sandbox: agents named anywhere, and Docker's agent mixins
(`sbx/*-mixin` on any registry or mirror) wherever they appear, including as plain kits. While either agent list is
set, an agent given as a kit reference stage does not recognise is refused. It cannot
stop a kit's setup commands from installing an agent CLI by other means; what stops that agent from working is the
sandbox's egress to its vendor.

## What is verified

`go test ./...` runs every command against fakes, so it shows what stage asks sbx, git and GitHub to do, not what
they then do. [VERIFICATION.md](VERIFICATION.md) records what has been checked against a live sbx or GitHub, when,
and what has not been run live yet.

## Limitations

- **The empty kit scaffold needs a live check.** `stage init`'s spec v3 mixin with `capabilities: []` passes
  stage's repository-kit checks. A live sbx run of that scaffold, and `stage up` against an init-written config,
  have not been verified; the maintainer must check them on the host.
- **Books run code on your host.** A book's secret commands run on your machine as you, before the sandbox exists.
  The checks on books stop them from rebinding stage's credentials (agent, sbx built-in, GitHub and broker) and catch
  mistakes. They cannot stop a book whose secret runs a command such as `gh auth token` and whose own kit injects
  the result on a host the book controls. Turn on only books from protected repositories whose changes are
  reviewed; see the known gap under [Configuration and books](#configuration-and-books).
- **Any kit can send any bound credential anywhere.** A kit that declares a credential for a service with a global
  sbx binding gets that secret injected on whatever hosts the kit names. That holds for published kits you add to
  your own config and for Docker's agent mixins that stage pins in its agent table, which are trusted by design.
  Add only kits you trust as much as stage itself. Books and repository configs may use only local kits, which stage
  checks.
- **A binding plus a global secret is standing risk outside stage.** If a service has a global sbx secret,
  including agent sign-ins such as `anthropic` or `openai`, any kit run under a plain `sbx run` can redirect it.
  `stage setup` lists these as `global secret set`.
- **`[bindings]` domains do not limit where a credential is injected.** A binding authorises a service; kits decide
  the hosts. Treat the domains as documentation, not as a boundary.
- **sbx stops a sandbox 30 seconds after its last session ends or disconnects.** A session is a `stage shell`, `stage exec` or
  `sbx exec`. Work left running in the background inside the sandbox, such as under `nohup` or a detached `tmux`,
  stops with it. Use `stage exec --detach` for long work: its host holder keeps a session open until the command
  ends.
- **A disappeared job holder means a lost job.** A killed holder or a host restart releases its file lock; without
  a recorded result, stage reports `lost`, never success or still running. Output saved so far remains readable.
  Stage cannot tell whether the in-sandbox command stopped; a killed holder may leave the sbx client and command
  running. Host sleep can interrupt sbx too; no reconnection or restart is attempted. If sbx reports an exit,
  stage records that status; if the holder disappears, the result is lost.
- **Job stop ends the host sbx client.** Whether this also stops its command inside a real sandbox, and whether
  sbx passes through every command status, still needs a live check. A stopped job does not prove the sandbox's
  command is gone. Job output is merged, untrusted stdout and stderr, capped at 1 GiB.
- **Starting jobs and down are not serialized.** A job started after down's running-job check can lose its output
  when down removes the task. Do not start new jobs while removing their sandbox.
- **The ship record can miss a push.** A ship is written to the task after the push returns, and the list reaches a
  take only when `stage down` or `stage take` keeps one. It misses a ship when:
  - the push was interrupted after GitHub accepted it, or the record could not be written (ship warns);
  - two ships finish their writes at the same instant, or a ship pushes while `stage down` is copying takes;
  - the task has no takes, the sandbox has nothing to take or is already gone, or `--discard-takes` was given: the
    task is deleted and its ships with it.

  A ship is recorded without its pull request when `gh` does not print one in the task's repository, for example a
  second `--pr` to a branch that already has one. The same commit shipped twice is recorded twice.
- **Do not run `sbx env rm --prune-bindings` on stage sandboxes.** Bindings are shared, and pruning one sandbox's
  bindings removes the service's binding for the whole machine. `stage down` never passes it.
- GraphQL writes are not refused. They share `POST /graphql` with reads, so only the token's permissions stop
  them: nothing does in `gh` mode, and stage cannot see a fine-grained token's permissions.
- `app` mode's check sees installation permissions only. An app's account permissions (on your own user) do not
  appear there, and GraphQL can use them.
- agy's Google sign-in is a token file inside the sandbox that the agent can read. With `copy_logins`, every
  sandbox with agy holds a live copy.
- [Gear](#gear) is copied into the sandbox as files the agent can read, and stage does not look inside them. The
  refused credential locations are a fixed list that guards against mistakes; a secret kept anywhere else,
  written into a config file you list, or reached by a hard link, goes in. Gear arrives after kit install commands have run, and is not
  refreshed while the sandbox lives. The copy has run only against fakes.
- AWS keys from the broker enter the sandbox and stay usable until they expire, up to about 12 hours, even after
  `stage down`.
- Bindings are shared by every sandbox. One written for a sandbox stays after `stage down` and approves that service
  for all sandboxes; a sandbox gets a value only from its own secret or a global sbx secret, and
  `stage up` checks for a global secret only for `github`.
- The push refusal covers HTTP on ports 443 and 80. A kit that allows SSH or another port to a git host opens a
  push path the proxy does not refuse.
- Deny-all egress limits where data can go, not what goes there: an agent can send what it can read to any host
  its kits allow, including its vendor's API.
- `stage up` checks the global sbx policy only when it creates a sandbox. Allow rules added later widen running
  sandboxes, and so does approving a refused host when sbx offers to, which happens outside stage's view.
- A credential the proxy injects can do anything it permits on its bound domains. stage restricts methods and paths
  only for GitHub: a book's binding, such as a package feed token, is added on every method, so the sandbox can
  publish or write wherever that credential allows.
- With the `copilot` agent, the `copilot` secret is your gh login (`gh auth token`), in every GitHub mode,
  `fine-grained` and `app` included. The proxy adds it only on Copilot's hosts (`api.githubcopilot.com`, its
  `individual`, `business` and `enterprise` variants, and `copilot.github.com`). Whether any of them accepts that
  token for more than Copilot is unverified.
- stage's defaults are named by tag, not digest: the base `docker.io/sbx/shell:1.1.0` and the agent mixins
  (`claude-mixin:2.1.319`, `codex-mixin:0.167.0`, `copilot-mixin:1.0.86`, `antigravity-mixin:1.0.0`). A tag can be
  repointed without stage noticing.
- A take is whatever the sandbox chose to put at the listed paths, up to 1 GiB each time. Treat kept files as
  untrusted input: do not run them, and do not feed them to an agent without reading them. `stage down` deletes
  the task but never its takes, so `~/.scoring-stage/takes/` grows until you clear it.
- On a host file system that ignores case, a take with two names that differ only by case stops the copy.
- A book's approval covers the files in its directory. Files its host commands read, and kits it uses from outside the
  directory can change without asking again.

## Development

```
go test ./...                                # unit tests with fakes; see VERIFICATION.md
tests/acceptance.sh <slug> [granted] [other] # checks a live sandbox made with stage up
```

[docs/design.md](docs/design.md) records why the tool is shaped this way; [VERIFICATION.md](VERIFICATION.md) records what was checked live.

### Releasing

Push a `vX.Y.Z` tag on `main` (`git tag v0.1.0 && git push origin v0.1.0`). The release workflow
(`.github/workflows/release.yml`) rejects a tag that is not `vX.Y.Z` with an optional `-suffix`, and a tagged
commit that is not on `main`. A build job with read-only permissions then runs the tests, builds the four archives
with the tag as `stage version` and writes `SHA256SUMS`. A separate release job attests the archives and creates
the GitHub release with generated notes. A tag with a hyphen, such as `v0.1.0-rc.1`, makes a prerelease. See
[VERIFICATION.md](VERIFICATION.md) for its runs.

The tag and `main` checks catch mistakes, not a malicious writer: a tag push runs `release.yml` as it exists at
the tagged commit, so anyone who can push a tag can also change the checks. The real controls are branch
protection on `main` and a tag ruleset that limits creating, updating and deleting `v*` tags to admins.
`SHA256SUMS` detects corruption, not tampering.

## License

Apache License 2.0; see [LICENSE](LICENSE).
