---
name: stage-repo-needs
description: Use when working out a repository's sandbox configuration, or when stage up or stage exec fails because a host, tool or toolchain version is missing.
---

Run `stage init --guide` and follow its instructions.

You may run `stage init`, `stage up --plan`, `stage up` and `stage exec`.
Always propose and never approve: do not pass `--approve-repo-config`, any
approval flag or any `--allow-*` flag. Do not edit approval records or managed
settings. Do not add credentials, bindings or secrets. When approval or a
credential is required, stop and show the person what is needed.
