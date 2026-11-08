# scoring-stage

`stage` runs coding agents (Claude Code, Codex, Copilot CLI, and anything else packaged as a kit) in
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
stage setup                          # check sbx, the global policy, credentials and packs
stage up ~/Code/api fix-login        # sandbox stage-fix-login on a scratch clone of ~/Code/api
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

## What every sandbox gets

| Kit | Purpose |
| --- | --- |
| `docker.io/sbx/shell` | Debian base with git, gh, curl, jq, Python and Docker |
| `docker.io/sbx/claude-mixin`, `codex-mixin`, `copilot-mixin` | The agents, with their own egress and credentials |
| [`kits/stage`](kits/stage/stage.yaml) | Refuses `POST /**/git-receive-pack` on every host, sets your git identity, tells the agent the rules |
| [`kits/aws-broker`](kits/aws-broker/aws-broker.yaml) | Only when AWS profiles are configured: `~/.aws/config` with a `credential_process` per profile |

Credential bindings for `github`, `copilot`, `anthropic` (OAuth), `openai` (OAuth) and, with AWS, `stage-broker`
are written to sbx's `~/.config/sbx/credentials.yaml`. A binding approves a service's credential on named domains,
capped by what the kits declare. Bindings are shared by every sandbox, so `stage down` leaves them in place.

## Configuration and packs

`~/.scoring-stage/config.toml` (all keys optional):

```toml
packs = ["~/Code/my-org-packs/backend"]
base = "docker.io/sbx/shell:1.1.0"                 # the workload kit
agents = ["docker.io/sbx/claude-mixin:2.1.319"]    # replaces the default agent list
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

Packs and the user config are decoded strictly: an unknown key is an error. Two packs that set the same base,
region, secret or binding differently are an error rather than a silent winner. The user config overrides packs.

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
