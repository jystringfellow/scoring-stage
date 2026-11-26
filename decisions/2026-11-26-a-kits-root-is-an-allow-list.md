# A kit's root is an allow list

A book's or repository's kit, and the user's own when Secrets Manager secrets are listed, may hold at its root only
one descriptor (`.yaml`, `.yml` or `.json`), a short list of documentation names and `files/`. Anything else is
refused by name, unread. `files/` is not read.

Why: stage checks the capabilities in the files it reads, and the kit specification (SPEC-v3 §3.3) lets a
Dockerfile carry a descriptor in a `# kit:` comment block that stage does not read. The specification does not say
where a runtime looks for a descriptor in a local kit directory.

Rejected:
- **Detecting the `syntax=docker/sandbox-kit` parser directive in any other file.** It is a deny list that has to
  match BuildKit's directive parser and sbx's loader exactly. Review found it missed a directive after 64 KiB of
  padding, after a `#!` line and in BuildKit's JSON form, refused documentation that merely showed an example, and
  walked every file under `files/`. It cannot see a frontend published under another name either.

A caution, not a finding: a descriptor with a `# kit:` comment line, in any spacing or case, is refused. Nobody has
shown that sbx reads a comment block inside a YAML descriptor; the rule keeps the one file stage reads from also
reading as a comment descriptor.

Cost: a local kit cannot carry a companion `<stem>.dockerfile` recipe or a script at its root. Content goes under
`files/`; a published kit can still have a recipe.

Evidence: `TestAKitRootHoldsOnlyItsDescriptorDocumentationAndFiles`,
`TestAKitRootRefusesDirectoriesLinksAndAnotherDescriptor`, `TestADescriptorWithAKitCommentBlockIsRefused`.
