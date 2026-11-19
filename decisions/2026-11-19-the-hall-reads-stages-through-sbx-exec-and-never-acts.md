# The hall reads stages through sbx exec, and never acts

`stage list` and `stage hall` show every stage's record, the agent sessions inside it and the activity a provider
reports. The design, the activity format and the slice plan are in the pull request that added this file; what
follows are the choices no test can hold.

- **Read-only, with no actions.** The hall has no control that sends input, approves, ships, stops or refreshes. Its
  page is served with GET only, so nothing on the page can make stage act. Rejected: buttons for ship or stop,
  which would put a host action one click from text an agent wrote.
- **Reads go through `sbx exec`, once per running stage per read.** One script, carried on stdin, reads the CAO
  server on the stage's loopback, the stage's disk and each provider, under a timeout inside the stage. Rejected:
  publishing CAO's port, whose API is unauthenticated and drives agents; a host listener that stages push status
  to, which gives every stage a path to the host; and a shared host mount, rejected for takes for the same reason.
- **Reads respect sbx's auto-stop.** `sbx exec` starts a stopped sandbox, and every exec session defers sbx's
  30-second auto-stop. So only a stage `sbx ls` shows running is read, and a stage is read no more often than once a
  minute unless one of its stage jobs is running, which keeps it up anyway. Rejected: a session held open per stage
  for live output, which would keep every stage running for as long as the hall is open.
- **Providers come only from the user's config and stage's built-ins.** A repository's config is input from whoever
  can commit, and a book's providers were left out until someone needs one. A built-in provider's translation runs
  on the host, in Go, from a tool's own output. Rejected: translator scripts run inside the stage, which an agent
  there could replace.
- **The page is static files embedded in the binary, with no build step, framework or third-party script.**
  Rejected: a JavaScript build, which adds a second toolchain and a package supply chain to a tool whose point is
  containment.
- **History comes from stage's own records.** A take's `stage.json`, and a hall snapshot stage writes into each
  take, hold what a finished stage did. Rejected: reading tools' own run records out of takes on the host.

Evidence: `sbx exec --help` on sbx 0.51.0 ("If the sandbox is stopped, it is started first"); VERIFICATION.md,
2026-11-10, sbx auto-stop.
