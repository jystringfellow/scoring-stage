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

Organizations adapt it with **packs**: a directory of [kits](https://github.com/docker/sandbox-kit-spec) plus the
host-side wiring they need (secrets, credential bindings, AWS profiles, per-repository additions).

> Status: early. The core flow is tested end to end on sbx 0.51; the command line and pack format may still change.

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
stage up ~/Code/api fix-login --agent agy --pack dotnet  # add an agent or an installed pack for one box
stage up ~/Code/api fix-login --plan # write the env file and show sbx's plan without creating
stage shell fix-login                # login shell in the repository; run claude, codex or copilot
stage exec fix-login -- git log -1
stage ship fix-login [--pr]          # push the sandbox's HEAD to agent/fix-login/<sha> from the host
stage list
stage down fix-login                 # remove the sandbox, its scoped secrets and the scratch clone
stage broker start|stop|status       # the AWS broker (up starts it when a sandbox needs it)
stage broker serve                   # run the broker in the foreground
```

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
3. **Agent logins.** Copilot uses a per-sandbox secret from `gh auth token`. Codex can log in on the host with
   `sbx secret set openai --oauth`. Claude Code logs in from inside the sandbox; the proxy keeps the real token on
   the host.

## What a sandbox gets

Nothing you did not choose. Every sandbox gets:

| Kit | Purpose |
| --- | --- |
| `docker.io/sbx/shell` | Debian base with git, gh, curl, jq, Python and Docker |
| [`kits/stage`](kits/stage/stage.yaml) | Refuses `POST /**/git-receive-pack` on every host, sets your git identity, tells the agent the rules |
| [`kits/aws-broker`](kits/aws-broker/aws-broker.yaml) | Only when AWS profiles are configured: `~/.aws/config` with a `credential_process` per profile |

plus the agents chosen by your config, the repository, a pack or `--agent`. Agents are Docker's published mixins,
each with its own egress and credentials:

| Name | Kit | Credential binding |
| --- | --- | --- |
| `claude` | `docker.io/sbx/claude-mixin` | `anthropic` (OAuth) |
| `codex` | `docker.io/sbx/codex-mixin` | `openai` (OAuth) |
| `copilot` | `docker.io/sbx/copilot-mixin` | `copilot`, from `gh auth token` |
| `agy` | `docker.io/sbx/antigravity-mixin` | none; sign in to Google inside the sandbox |

A kit reference works wherever a name does, for another agent or another version of one of these.

The `github` binding, an agent's bindings and, with AWS, `stage-broker` are written to sbx's
`~/.config/sbx/credentials.yaml`. A binding approves a service's credential on named domains, capped by what the
kits declare. Bindings are shared by every sandbox, so `stage down` leaves them in place.

## Configuration and packs

Configuration has two levels, like kapellmeister's: yours in `~/.scoring-stage/config.toml`, and a repository's
in its `.scoring-stage/config.toml`.

`~/.scoring-stage/config.toml` (all keys optional):

```toml
agents = ["claude", "codex"]                       # the agents you use; names or kit references
packs = ["~/Code/my-org-packs/backend"]            # installed packs; each applies only when asked for
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

A pack is a directory with a `pack.toml`:

```toml
name = "backend"
agents = ["claude"]                                       # agents this pack's work needs
kits = ["./kits/toolchain", "docker.io/sbx/mise:1.0.0"]   # local kits start with ./ or ../

[aws]
profiles = ["dev", "staging"]
region = "us-west-2"

[secrets.artifacts]
command = "python3 -I {pack}/host/feed-token.py"          # {pack} is the pack directory

[bindings.artifacts]
apiKey = { domains = ["pkgs.example.com"] }

[[repos]]
match = ["example/api", "Example.Api"]                    # globs on owner/name or the directory name
kits = ["./kits/api-rules"]
aws = { profiles = ["api-dev"] }
```

An installed pack applies to a sandbox when the repository's config names it, when `--pack` names it, or when one
of its `[[repos]]` entries matches the repository; the `[[repos]]` rules are for repositories you cannot add a
config to. `--pack ./dir` also uses a pack that is not installed.

A repository's `.scoring-stage/config.toml`, read from the branch the sandbox starts from:

```toml
agents = ["claude", "codex"]     # for example, the agents its .kapellmeister ensembles seat
packs = ["backend"]              # installed packs, by name
kits = ["./.scoring-stage/kits/rules"]
```

A repository is not trusted with anything that acts on the host. It can add agents, kits and installed packs, but
it cannot set secrets, bindings, AWS profiles or the base, and it names packs rather than pointing at them: a pack
it asks for that you have not installed is an error.

Packs and both configs are decoded strictly: an unknown key is an error. Two packs that set the same base,
region, secret or binding differently are an error rather than a silent winner. Your config overrides packs.

A kit that a pack adds for its own service declares a `credential@1` entry for that service; the pack's `secrets`
entry supplies the value for each sandbox, and its `bindings` entry approves it.

## Development

```
go test ./...                                # unit tests, with a fake sbx on PATH and an offline git remote
tests/acceptance.sh <slug> [granted] [other] # checks a live sandbox made with stage up
```

[docs/design.md](docs/design.md) records why the tool is shaped this way and what was verified on sbx.

## License

Apache License 2.0; see [LICENSE](LICENSE).
