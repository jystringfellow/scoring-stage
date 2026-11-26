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

A caution, not a finding: a descriptor with a line that is only `# kit:` at the start of a line, the way a
comment descriptor opens its block, is refused. Nobody has shown that sbx reads such a block inside a YAML
descriptor; the rule keeps the one file stage reads from also reading as a comment descriptor. A descriptive comment
(`# kit: installs node`) or an indented line, such as one inside a script, passes.

Editor swap and backup files and AppleDouble files are refused with their own message rather than allowed, because
one can carry a descriptor's extension (`._tool.yaml`) and stage would accept it unread.

The remaining limit: an allowed documentation or system file (README, LICENSE, `.DS_Store` and the rest) is
accepted unread. The rule relies on no runtime choosing a descriptor from a file with one of those names by its
content. That is not verified.

Cost: a local kit cannot carry a companion `<stem>.dockerfile` recipe or a script at its root. Content goes under
`files/`; a published kit can still have a recipe.

Evidence: `TestAKitRootHoldsOnlyItsDescriptorDocumentationAndFiles`,
`TestAKitRootRefusesDirectoriesLinksAndAnotherDescriptor`, `TestADescriptorWithAKitCommentBlockIsRefused`.
