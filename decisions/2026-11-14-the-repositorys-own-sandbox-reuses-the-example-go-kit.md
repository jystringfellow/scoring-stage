# The repository's own sandbox reuses the example Go kit

`.scoring-stage/config.toml` names the four agents and one kit, `./examples/books/go/kits/go`, so a sandbox for
this repository needs no book given by path and the example kit is exercised by every such sandbox. It names no
book: stage stays usable alone, and a person who wants another tool in the sandbox adds its book with `--book`.
`scripts/verify` is the check an agent ensemble runs: module verification, gofmt, vet and the whole suite. A first
draft left 38 tests out by name as timing-dependent; none had failed, the whole suite takes under a minute in a
sandbox, so they stay in. Evidence: `stage init . --dry-run` reports `check passed` with kits
`shell, ..., stage, go`; `km prove` passed the verify twice on a clean copy and saw it fail for a source break and
a test break.
