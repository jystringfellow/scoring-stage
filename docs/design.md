# Design notes

## Books are kits plus host wiring

Docker Sandboxes kits (spec v3) already declare most of what a sandbox needs: egress (`network-policy@2`), service
credentials kept on the host (`credential@1`), files and setup hooks (`lifecycle@1`), agent guidance
(`agent-context`), git identity (`git-identity@1`) and shared caches (`host-mount@1`). A kit cannot run anything on
the host, compute a secret, or choose which sandbox gets which grant. Those are the jobs `stage` keeps:

| Concern | Owner |
| --- | --- |
| Egress, push refusal, agent files and guidance | Kits |
| Secret values (`gh auth token`, a token file, a feed password) | `stage`, as per-sandbox `secrets` with a host `command` |
| Credential approval per service and domain | `stage`, as `bindings` |
| AWS credentials | `stage`'s host broker, plus the `aws-broker` kit |
| Scratch clone, `ship`, cleanup | `stage` |
| sbx version floor and the global deny-all check | `stage` |

`stage up` expresses all of it as one sbx environment file and lets `sbx env create` apply it, so a sandbox's
policy, secrets and bindings are created and removed together.

## Nothing by default

A sandbox carries only the agents someone chose: each agent's mixin widens egress and brings credential
bindings, so an agent nobody runs is attack surface with no use. Agents come from the user's config, the
repository's config, a book or `--agent`, and an agent's bindings come with it.

Configuration follows kapellmeister's split between the user's directory and the repository's. The repository's
config is input from whoever can commit to it, so it may only ask for things that stay inside the sandbox (agents,
kits) or that the user has already accepted (installed books, by name). Secrets run commands on the host, bindings
and AWS profiles hand out credentials, and a book location would let a repository supply its own host commands, so
those stay with the user and the books they install.

## Logins the proxy cannot hold

Claude Code, Codex and Copilot sign in through credentials the proxy keeps on the host. Docker's Antigravity kit
declares only an optional Gemini API key, so an agy sign-in is a token file inside the sandbox. `copy_logins` is the
opt-in exception to credentials staying on the host: stage saves that file from a sandbox (`stage login`, and again
on `stage down` to keep a refreshed token) and copies it into new ones through `sbx exec -i`, so it never appears on
a command line or in the env file. It applies only to agents with a known login file, and only from the user's
config.

## One binary

`stage` is written in Go so it installs as one binary, like `sbx`. sbx accepts local kits only as directories, so
the core kits are embedded with `go:embed` and extracted to `~/.scoring-stage/kits/<content hash>/`; a new build
with changed kits gets a new directory and never rewrites one an existing sandbox was created from. The broker is
the same binary run as `stage broker serve`.

## AWS broker

The broker listens on `127.0.0.1:9911` on the host and answers `GET /credential-process/<profile>` with
`aws configure export-credentials` output. Each sandbox has its own token and its own list of granted profiles
(`~/.scoring-stage/broker/grants/<slug>.{token,profiles}`). The token is a per-sandbox secret for the
`stage-broker` service. The `aws-broker` kit declares it as an inject-only credential, so the proxy adds
`Authorization: Bearer <token>` to broker requests and the token never enters the sandbox.

sbx names `host.docker.internal` "localhost" when matching policy and credentials. The kit allows only
`localhost:9911` and injects the token only on `localhost:9911` (verified on sbx 0.51.0: the broker answers, other
host ports are refused with 403); injecting on `host.docker.internal` reaches the broker without the header. The
broker serves only temporary credentials (an `ASIA` key with a `SessionToken`) that expire within about 12 hours,
so an IAM user's long-lived keys never reach a sandbox. Those temporary keys do enter the sandbox, and stay usable
until they expire; signing requests at the proxy instead would keep them out.

## Verified on sbx 0.51.0

- `network-policy@2` bounded allow and deny entries are enforced and refused with HTTP 403. A kit's deny wins over a
  global allow. IP globs such as `1.1.*.*:443` work.
- Bounded entries cannot match query strings. Refusing `POST /**/git-receive-pack` is what stops a push; the
  `info/refs?service=git-receive-pack` discovery request is left to the origin, which answers 403 for anonymous
  callers.
- A bounded entry makes its hosts require HTTP inspection, and sbx fails closed on connections it cannot inspect
  ("HTTP policy requires L7 enforcement, which is unavailable for transparent connections"). The push deny is
  therefore limited to `**:443` and `**:80`; on `**` it refused every non-HTTP connection, such as SQL Server on
  1433. Port suffixes on bounded deny hosts are honored.
- `host-port@1` is not supported; `host-mount@1` is, and is shared by every sandbox composing the same kit.
- `sbx secret set <service> --sandbox <name>` accepts services that are not built in, which is how `stage-broker`
  and book services get values.
- `sbx env create --force --no-input` creates a sandbox, its secrets and its bindings without prompting.
  `sbx env rm` removes the sandbox and its scoped secrets; `--prune-bindings` would also remove bindings shared by
  other sandboxes, so `stage down` does not pass it.
- Docker's published agent mixins use `agent-context@1`, which cannot compose with `@2`. The `stage` kit declares
  both as optional with the same content so either selection composes.
- Typing a built-in agent name (`claude`) selects Docker's v2 kits, which cannot compose with v3 mixins; `stage`
  always names a v3 workload by reference.
- `kit.allowedSources` defaults to Docker Hub and dhi.io. Books published to another registry need it widened, and
  signed kits must be published images: local and git sources cannot be signed.
