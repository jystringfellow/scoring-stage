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
| Credential authorisation per service (kits decide the hosts) | `stage`, as `bindings` |
| AWS credentials | `stage`'s host broker, plus the `aws-broker` kit |
| Scratch clone, `ship`, cleanup | `stage` |
| sbx version floor and the global deny-all check | `stage` |
| GitHub access for the sandbox (`[github] mode`: a fine-grained token per owner, a GitHub App token refreshed on the host, the gh login, or none) | `stage`, as the `github` secret from a host command; sbx cannot pick a token per owner or refresh one |
| Logins the proxy cannot hold (agy) | `stage`'s opt-in `copy_logins`; see below |
| Which agents, books and kits a sandbox gets, and what a book or repository may set (local kits only, no reserved services) | `stage`; sbx has no notion of who may ask for what |
| Approval of a repository's kits and books | `stage`'s trust store, by content fingerprint |
| Organisation policy (allowed GitHub modes, AWS profiles) | `stage`'s managed settings |
| Reporting sbx's global credential bindings | `stage setup`, read-only; `credentials.yaml` stays sbx's |

`stage up` expresses all of it as one sbx environment file and lets `sbx env create` apply it, so a sandbox's
policy, secrets and bindings are created and removed together.

## Nothing by default

A sandbox carries only the agents someone chose: each agent's mixin widens egress and brings credential
bindings, so an agent nobody runs is attack surface with no use. Agents come from the user's config, the
repository's config, a book or `--agent`, and an agent's bindings come with it. Typing a built-in agent name such as
`claude` would select Docker's v2 kits, which cannot compose with v3 mixins, so stage always names a v3 workload
by reference.

Configuration follows kapellmeister's split between the user's directory and the repository's. The repository's
config is input from whoever can commit to it, so it may only ask for agents by name, things the user has already
installed (books, by name), and its own kits. Kits and books can widen egress, send credentials and run host
commands, so a repository's kits must be link-free `./` directories in the repository, and its kits and the books
it turns on are used only after the user approves each one by content: a local kit's or book's files. Books and
repository configs cannot name published kits, because a kit decides where a credential is injected and a published
kit cannot be inspected; their local kits are checked for the credentials they declare. Secrets run commands on the host, bindings
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
so an IAM user's long-lived keys never reach a sandbox through the broker. Those temporary keys do enter the
sandbox, and stay usable until they expire; signing requests at the proxy instead would keep them out.

## Verified on sbx

What has been checked against a live sbx, and what has not, is in [VERIFICATION.md](../VERIFICATION.md).
