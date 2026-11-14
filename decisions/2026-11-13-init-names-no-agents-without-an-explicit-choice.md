# Init names no agents without an explicit choice

`stage init` omits `agents` unless `--agent` names one. The Nothing by default decision in
[docs/design.md](../docs/design.md#nothing-by-default) makes each agent's egress and credential bindings an
explicit choice; drafting a repository config must not widen every contributor's sandbox without evidence.
The user's configured agents still apply. Evidence: `TestInitWithoutAnAgentFlagWritesNoAgentsKey`.
