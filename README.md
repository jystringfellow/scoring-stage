# scoring-stage

`stage` runs coding agents (Claude Code, Codex, Copilot CLI, Antigravity, and anything else packaged as a kit) in
[Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) microVMs with every permission prompt off. The sandbox is
the boundary:

- **Egress is deny-all** apart from what the sandbox's kits allow, enforced by the sbx proxy.
- **Credentials stay on the host**, with the exceptions under [Limitations](#limitations). The sandbox sees
  placeholders; the proxy adds the real values to outbound requests. AWS credentials come from a host broker that
  serves only short-lived keys, and only for the profiles granted to that sandbox.
- **Pushes happen from the host.** `git push` over HTTP or HTTPS from the sandbox is refused at the proxy;
  `stage ship` pushes the sandbox's commit to an `agent/` branch from the host. GraphQL writes are not refused; see
  [Limitations](#limitations).
- **Each task gets its own scratch clone**, so the agent never touches your working copy.

Organizations adapt it with **books**: a directory of [kits](https://github.com/docker/sandbox-kit-spec) plus the
host-side wiring they need (secrets, credential bindings, AWS profiles, per-repository additions). Like the book a
session player is handed for a gig, a book holds what the stage needs for one kind of work.

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
stage up ~/Code/api fix-login        # sandbox stage-fix-login on a scratch clone of ~/Code/api
stage up ~/Code/api fix-login --agent agy --book dotnet  # add an agent or an installed book for one box
stage up ~/Code/api fix-login --aws-profile staging    # grant an AWS profile to one box and make it the default
stage up ~/Code/api fix-login --plan # write the env file and show sbx's plan without creating (stops first if approvals are missing)
stage shell fix-login                # login shell in the repository; run claude, codex or copilot
stage exec fix-login -- git log -1
stage ship fix-login [--pr]          # review and push the sandbox's HEAD to agent/fix-login/<sha> from the host
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

`stage ship` pushes to `agent/<slug>/<first 8 characters of the sha>` with your host git credentials; `--branch`
names another branch, which must also start with `agent/`. It shows the commits and a diffstat, with control
characters escaped, and asks before pushing (`--yes` skips the question). It refuses changes to CI and workflow
files (`.github/`, `azure-pipelines*.yml`, `.azure-pipelines/`, `.pipelines/`, GitLab, Jenkins, CircleCI,
Buildkite), files that run code when someone opens or
checks out the branch (`.devcontainer/`, `.vscode/`, `.husky/`, pre-commit and lefthook configs, `.envrc`, mise and
asdf tool files), `.gitmodules` and `.scoring-stage/`, unless you pass `--allow-protected-paths`: CI runs a pushed
branch's workflow files with the repository's secrets, and `.scoring-stage/` widens future sandboxes. Azure
Pipelines can read YAML from any path a pipeline names, so the file check is a heuristic there; use branch policies
so `agent/*` branches cannot trigger pipelines that hold secrets. Pushing an `agent/*` branch triggers whatever CI
the repository runs on branch pushes, so that CI should not hold privileged secrets for unreviewed branches: on
GitHub, keep secrets in environments with required reviewers or deployment branch rules, or limit push triggers to
protected branches. `ship` warns about commits without a `Co-authored-by` trailer, since commits carry your git identity.

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

## Takes

A sandbox's files go with it on `stage down`. A **take** is what you keep: list paths inside the sandbox, and stage
copies them to the host before removing it, and whenever you run `stage take <slug>`.

```toml
takes = ["~/.tool/runs", "/var/log/tool"]   # in ~/.scoring-stage/config.toml, a book.toml or a repository's config
```

A path starts with `/` or `~/` (the sandbox user's home) and has no `..`. A path that does not exist in the sandbox
is passed over. Each copy lands in its own directory, `~/.scoring-stage/takes/<slug>/<UTC time>/`, under the path it
had in the sandbox, such as `home/agent/.tool/runs/`. A sandbox uses the takes it was created with; a later change
to a config applies to the next `stage up`.

The files come from a sandbox that ran with prompts off, so stage treats them as untrusted. It keeps regular files
and directories only, with modes 600 and 700, and says how many links and special files it left behind. An entry
that would land outside the take's directory, a take over 1 GiB or 100,000 entries, or a sandbox that does not
answer stops the copy and keeps nothing from it. When that happens during `stage down`, the sandbox is left in
place; fix the cause and run it again, or pass `--discard-takes` to remove the sandbox without its takes.

Takes are records for you and your tools to read. stage never copies them into a sandbox. A repository's takes
need your approval like its kits, and `repo_kits = "deny"` in managed settings refuses them.

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

A kit reference works wherever a name does, for another agent or another version of one of these.

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
  `~/.gemini/antigravity-cli/cache/onboarding.json`, once you have finished that setup in a sandbox. Without it an agy
  chair under kapellmeister or any other orchestrator waits on that setup screen.

The kit also has a Gemini API-key mode that would keep a key on the host: a `google` secret plus
`[bindings.google]` with `apiKey = { domains = ["generativelanguage.googleapis.com"] }` in your config. It uses the
Gemini API and the key's project billing rather than your Google account's Antigravity access, and has not been
tested with stage.

## Configuration and books

Configuration has two levels, like kapellmeister's: yours in `~/.scoring-stage/config.toml`, and a repository's
in its `.scoring-stage/config.toml`.

`~/.scoring-stage/config.toml` (all keys optional):

```toml
agents = ["claude", "codex"]                       # the agents you use; names or kit references
books = ["~/Code/my-org-books/backend"]            # installed books, relative to ~/.scoring-stage; each applies only when asked for
base = "docker.io/sbx/shell:1.1.0"                 # the workload kit
kits = ["./kits/personal"]                         # extra kits, relative to ~/.scoring-stage

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
pr_footer = "Generated with an AI coding agent."
```

A book is a directory with a `book.toml`:

```toml
name = "backend"
agents = ["claude"]                                       # agents this book's work needs
kits = ["./kits/toolchain", "./kits/mise"]               # local kits only, starting with ./ or ../

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
agents = ["claude", "codex"]     # for example, the agents its .kapellmeister ensembles seat
books = ["backend"]              # installed books, by name
kits = ["./.scoring-stage/kits/rules"]  # inside the repository; approved by you before use
```

A repository is not trusted with anything that acts on the host. It can add agents by name, installed books by
name, its own kits and [takes](#takes), but it cannot set secrets, bindings, AWS profiles, GitHub access or the base. A book it asks
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
- **Do not run `sbx env rm --prune-bindings` on stage sandboxes.** Bindings are shared, and pruning one sandbox's
  bindings removes the service's binding for the whole machine. `stage down` never passes it.
- GraphQL writes are not refused. They share `POST /graphql` with reads, so only the token's permissions stop
  them: nothing does in `gh` mode, and stage cannot see a fine-grained token's permissions.
- `app` mode's check sees installation permissions only. An app's account permissions (on your own user) do not
  appear there, and GraphQL can use them.
- agy's Google sign-in is a token file inside the sandbox that the agent can read. With `copy_logins`, every
  sandbox with agy holds a live copy.
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
