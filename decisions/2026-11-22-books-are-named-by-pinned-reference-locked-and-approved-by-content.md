# Books are named by pinned reference, locked, and approved by content

A book is named by a reference with a version, fetched by stage, locked by content and approved by content. Nobody
clones a books repository or edits a path to use one. Local directories stay for writing a book. This replaces
the 2026-11-09 decision, under which books a user turned on were not reviewed: every fetched book is now approved
by content, whoever named it. A book from a local directory keeps that earlier behaviour and its warning.

- **Reference.** `github.com/<owner>/<repo>//<directory>@<version>`, wherever a book is named: the user's config,
  a repository's config and `--book`. The version is a tag, resolved under `refs/tags/` with annotated tags peeled,
  or a full 40-character commit. A short commit, a name that exists only as a branch, and "latest" are refused, so
  a book that can run host commands never changes without the user choosing it. Only github.com at first; another
  source (an OCI registry) can be added as its own form without changing the rest.
- **Short names.** `books` in the user's config is either today's list or a `[books]` table whose values are a
  reference or a local directory; each key is the short name a repository or `--book` uses. A key that is another
  book's name is an error, and so are two books with the same name. A directory is never locked.
- **Lock.** `~/.scoring-stage/stage.lock` records every reference stage resolves, whoever named it: the reference,
  the commit, the fingerprint and the fingerprint's scheme. A reference is a global fact, so entries are keyed by
  reference. `stage up` adds entries and never changes one, and later runs fetch the locked commit and refuse
  content that does not match. A repository's committed `.scoring-stage/stage.lock` is read from the scratch clone
  as a constraint: for a reference only it holds, stage fetches its commit without resolving the tag, verifies it
  and records it in the user's lock, which is the point of a team lock; where the two disagree, the run stops,
  names both and says how to settle it. `stage up` writes nothing inside a repository: it works from a scratch
  clone, and the user's working copy may be on another branch.
- **Changing a lock.** `stage book update` is the only command that changes an entry in the user's lock. It fetches,
  says plainly when a tag moved, shows what changed in terms of what the book does, asks, and only then writes the
  lock entry, the approval and (for `--to`) the one quoted value in the user's config together; a decline or a
  failure partway leaves all three as they were. `--from REPO` takes a repository's entry, which settles locks that
  disagree one way. `stage book lock REPO` settles them the other way: it makes a clone's committed lock match the
  user's for the references its config names, on disk, for the user to review and commit. stage never rewrites a
  repository's config. Both run only in a terminal, and callers through `stage mcp` cannot use them.
- **Fingerprint.** Computed from the fetched git tree: each path, whether it is executable, and its content,
  prefixed by a scheme name (`book-tree/1`). The same value is the lock's `sha256`, the cache key and the approval
  fingerprint. Symbolic links and submodules are refused rather than hashed. The scheme is recorded in each lock
  entry, so a later change to the formula is reported as such and is never read as a moved tag. A local book keeps
  its own fingerprint over the files on disk, which includes their permissions and so could not be shared with a
  read-only cache.
- **Fetch and cache.** With the host's git and stage's hardened git flags, the user's own credentials, as for a
  scratch clone, so a private books repository works wherever `git fetch` does. A throwaway bare repository fetches
  the one commit at depth 1 and is deleted; only the book's directory is kept, read-only, in
  `~/.scoring-stage/books/<fingerprint>/`. The blob filter is not used: a fetch by URL cannot use one without a
  configured partial-clone remote, and lazy blob fetches would ask GitHub once per file. Files over 1 MiB, books over
  8 MiB or 1,000 files, and paths a book cannot hold are refused. The cache is checked against the fingerprint on
  every use.
- **When fetching happens.** Only commands that compose a sandbox fetch (`up`, `approve`, `init`, `setup`).
  Reading the config, as `stage list`, `stage hall` and `stage mcp` do, uses the cache and never the network. Every
  reference in the user's config is fetched so its `[[repos]]` rules can be read; reading them only parses TOML, and
  only books that apply are approved. A fetch that fails names the reference and changes nothing.
- **Approval.** Every fetched book is approved by content before it applies, with the same list of what it does
  that a repository's books get. A repository may name a reference the user never installed: stage fetches it,
  shows what it does and asks, so cloning a repository never runs its book's host commands unreviewed. Without a
  terminal the run stops. `stage mcp` callers and anything inside a sandbox still cannot record an approval, and
  managed settings' `repo_kits = "deny"` refuses references a repository names.
- **Includes.** A book's `includes` apply whenever it does. A relative include in a fetched book is a book in the
  same repository at the same commit, named and locked by that commit, so a book and what it includes move together;
  it cannot leave the repository. Cycles are an error. A book reached twice at one version applies once; at two
  versions it is an error naming both include paths, rather than stage choosing one. Each included book is approved
  on its own fingerprint, and the existing conflict rule holds across includes. Every include is a fetch made before
  the approval prompt, so includes are bounded: a chain of at most 8 books, and at most 50 books in one run; past
  either limit stage stops before fetching more and names the chain.
- **Trust levels.** Each book is `local` (a directory) or `pinned` (fetched, locked, approved), and both always
  ask. `trusted` (fetched, locked and signed by a publisher that managed settings name) is reserved: it may later
  let a book apply without a prompt, or let managed settings name books every sandbox gets. Neither may be built
  without its own decision record, because each removes a person's check on code that runs on the host.

Not in this work: signatures and trusted publishers, prompt-free books, the OCI source, and a baseline book list in
managed settings.

Rejected:

- **Branches or "latest" as versions.** A book can run host commands; it must not change without the user choosing
  it.
- **Cloning the whole books repository into the cache.** It keeps files nobody approved on the machine.
- **Updating the lock on `stage up`.** The lock would then record whatever was fetched, and catch nothing.
- **Writing a repository's lock from `stage up`.** `stage up` reads the repository from a scratch clone that becomes
  the agent's workspace, and the user's working copy may be on another branch.
- **A book registry service.** Git now, and an OCI registry later, already give versions and access control.

Evidence: `TestALockedReferenceKeepsItsCommitWhenTheTagMoves`,
`TestATeamLockPinsTheCommitWithoutResolvingTheTagAndIsCopiedToYours`,
`TestABookYouTurnOnYourselfByReferenceIsApprovedToo`,
`TestARepositoryMayNameABookByReferenceYouNeverInstalledAndYouAreAsked`, `TestReadingConfigNeverFetchesABook`.
Fetching from github.com itself has not been checked live; the README lists it under Limitations.
