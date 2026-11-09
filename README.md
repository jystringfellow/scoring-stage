# scoring-stage

`stage` runs coding agents (Claude Code, Codex, Copilot CLI, Antigravity, and anything else packaged as a kit) in
[Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) microVMs with every permission prompt off. The sandbox is
the boundary:

- **Egress is deny-all** apart from what the sandbox's kits allow, enforced by the sbx proxy.
- **Credentials stay on the host.** The sandbox sees placeholders; the proxy adds the real values to outbound
  requests. AWS credentials come from a host broker that serves only the profiles granted to that sandbox.
- **Pushes happen from the host.** `git push` from the sandbox is refused at the proxy; `stage ship` pushes the
  sandbox's commit to an `agent/` branch from the host.
- **Each task gets its own scratch clone**, so the agent never touches your working copy.

Organizations adapt it with **books**: a directory of [kits](https://github.com/docker/sandbox-kit-spec) plus the
host-side wiring they need (secrets, credential bindings, AWS profiles, per-repository additions). Like the book a
session player is handed for a gig, a book holds what the stage needs for one kind of work.

> Status: early. The core flow is tested end to end on sbx 0.51; the command line and book format may still change.

## Requirements

macOS or Linux with `sbx` 0.51.0 or later (signed in), `git`, and optionally `gh` (Copilot credentials and
`stage ship --pr`) and the `aws` CLI (the AWS broker). Building `stage` needs Go 1.26 or later.

## Install

```
go install github.com/jystringfellow/scoring-stage/cmd/stage@latest
```

or, from a clone, `go build -o bin/stage ./cmd/stage`. `stage` is a single binary: the core kits are embedded in it
and extracted to `~/.scoring-stage/kits/<content hash>/` the first time `stage up` needs them.

## Use

```
stage setup --agent claude --agent codex  # save the agents you use; check sbx, the policy and credentials
stage up ~/Code/api fix-login        # sandbox stage-fix-login on a scratch clone of ~/Code/api
stage up ~/Code/api fix-login --agent agy --book dotnet  # add an agent or an installed book for one box
stage up ~/Code/api fix-login --aws-profile staging    # grant an AWS profile to one box and make it the default
stage up ~/Code/api fix-login --plan # write the env file and show sbx's plan without creating
stage shell fix-login                # login shell in the repository; run claude, codex or copilot
stage exec fix-login -- git log -1
stage ship fix-login [--pr]          # review and push the sandbox's HEAD to agent/fix-login/<sha> from the host
stage list
stage login fix-login                # save the sandbox's agy login for future sandboxes (copy_logins)
stage down fix-login                 # remove the sandbox, its scoped secrets and the scratch clone
stage broker start|stop|status       # the AWS broker (up starts it when a sandbox needs it)
stage broker serve                   # run the broker in the foreground
```

`stage ship` shows the commits and a diffstat and asks before pushing (`--yes` skips the question). It refuses
changes to CI and workflow files (`.github/`, Azure Pipelines, GitLab, Jenkins, CircleCI, Buildkite), `.gitmodules`
and `.scoring-stage/` unless you pass `--allow-protected-paths`: CI runs a pushed branch's workflow files with the
repository's secrets, and `.scoring-stage/` widens future sandboxes. It warns about commits without a
`Co-authored-by` trailer, since commits carry your git identity. Git on the host runs with hooks and fsmonitor
off, so nothing in the scratch clone's `.git` runs on your machine.

`stage up` clones the repository to `~/.scoring-stage/tasks/<slug>/src`, writes an
[sbx environment file](https://docs.docker.com/ai/sandboxes/configuration/environment-files/) next to it and runs
`sbx env create`. The sandbox sees the repository at the scratch clone's host path. `stage down` deletes the scratch
clone, so ship anything you want to keep first.

## One-time setup

1. **Global deny-all policy.** A sandbox's egress comes from its kits, so any global allow rule (such as sbx's
   default "balanced" lists) widens every sandbox. `stage up` refuses to run while global allow rules exist unless
   you pass `--allow-global-policy`. `stage setup --yes` runs `sbx policy reset --force` and
   `sbx policy init deny-all`; that deletes every local policy and stops running sandboxes.
2. **GitHub token (recommended).** Without one, sandboxes use the global sbx `github` secret, which is usually your
   full `gh` token: the agent cannot read it, but it can use it. To limit that, create a fine-grained read-only
   token (Contents, Metadata and Pull requests: read) and save it as
   `~/.scoring-stage/secrets/github-readonly-token` (mode 600). Pushes are refused at the proxy either way, but only
   a read-only token stops API writes such as comments or merges.
3. **Agent logins.** See [Agent logins](#agent-logins).

## What a sandbox gets

Nothing you did not choose. Every sandbox gets:

| Kit | Purpose |
| --- | --- |
| `docker.io/sbx/shell` | Debian base with git, gh, curl, jq, Python and Docker |
| [`kits/stage`](kits/stage/stage.yaml) | Refuses `POST /**/git-receive-pack` on ports 443 and 80 of every host, sets your git identity, tells the agent the rules |
| [`kits/aws-broker`](kits/aws-broker/aws-broker.yaml) | Only when AWS profiles are configured: `~/.aws/config` with a `credential_process` per profile, plus `[default]` when a default profile applies |

plus the agents chosen by your config, the repository, a book or `--agent`. Agents are Docker's published mixins,
each with its own egress and credentials:

| Name | Kit | Credential binding |
| --- | --- | --- |
| `claude` | `docker.io/sbx/claude-mixin` | `anthropic` (OAuth) |
| `codex` | `docker.io/sbx/codex-mixin` | `openai` (OAuth) |
| `copilot` | `docker.io/sbx/copilot-mixin` | `copilot`, from `gh auth token` |
| `agy` | `docker.io/sbx/antigravity-mixin` | none; see [Agent logins](#agent-logins) |

A kit reference works wherever a name does, for another agent or another version of one of these.

The `github` binding, an agent's bindings and, with AWS, `stage-broker` are written to sbx's
`~/.config/sbx/credentials.yaml`. A binding approves a service's credential on named domains, capped by what the
kits declare. Bindings are shared by every sandbox, so `stage down` leaves them in place.

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
books = ["~/Code/my-org-books/backend"]            # installed books; each applies only when asked for
base = "docker.io/sbx/shell:1.1.0"                 # the workload kit
kits = ["./kits/personal"]                         # extra kits, relative to ~/.scoring-stage

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
kits = ["./kits/toolchain", "docker.io/sbx/mise:1.0.0"]   # local kits start with ./ or ../

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

A repository's `.scoring-stage/config.toml`, read from the branch the sandbox starts from:

```toml
agents = ["claude", "codex"]     # for example, the agents its .kapellmeister ensembles seat
books = ["backend"]              # installed books, by name
kits = ["./.scoring-stage/kits/rules"]  # inside the repository; approved by you before use
```

A repository is not trusted with anything that acts on the host. It can add agents by name, installed books by
name, and its own kits, but it cannot set secrets, bindings, AWS profiles, GitHub access or the base. A book it asks
for that you have not installed is an error. A repository's kit is either a `./` path inside the repository, with no
symbolic links, or a published kit pinned by digest (`registry/name@sha256:...`).

Kits and books can widen a sandbox's network access, send credentials and run commands, so `stage up` uses the
kits a repository adds, and the installed books it turns on, only after you approve them. It lists each one with
what it does (network rules, credentials, host commands, AWS profiles) and asks. Without a terminal, pass
`--approve-repo-kits` after reviewing them. An approval covers that exact content (a local kit's files, a published
kit's digest and arguments, a book's files) and is kept in `~/.scoring-stage/trusted-kits`, so a change asks
again. A book you ask for yourself, with `--book` or a `[[repos]]` rule, needs no approval.

Books and both configs are decoded strictly: an unknown key is an error. Two books that set the same base,
region, AWS default profile, secret or binding differently are an error rather than a silent winner. Your config
overrides books.

The sandbox's `~/.aws/config` has a `[default]` profile when a default profile is set, or when exactly one profile
is granted and nothing names one, so the `aws` CLI and SDKs work without `AWS_PROFILE`. The default must be one of
the granted profiles; set `default_profile` in your `[aws]` table to choose another. `stage up --aws-profile NAME`
grants a profile from your host AWS config to that one sandbox and makes it the default, over books and your config.

A kit that a book adds for its own service declares a `credential@1` entry for that service; the book's `secrets`
entry supplies the value for each sandbox, and its `bindings` entry approves it.

## Development

```
go test ./...                                # unit tests, with a fake sbx on PATH and an offline git remote
tests/acceptance.sh <slug> [granted] [other] # checks a live sandbox made with stage up
```

[docs/design.md](docs/design.md) records why the tool is shaped this way and what was verified on sbx.

## License

Apache License 2.0; see [LICENSE](LICENSE).
