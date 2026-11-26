# Secrets Manager secrets are listed by exact name

A book or the user's config lists the Secrets Manager secrets a sandbox may read, by exact name, under the host
profile for them. When anything lists one, nothing in the sandbox may reach Secrets Manager itself.

Why: once a profile reaches Secrets Manager, it can read every secret its role allows. sbx's proxy sees only TLS to
AWS, so it cannot filter by secret; only something that reads the request can.

Rejected:
- **Prefixes and wildcards** (`app/*`). A prefix also grants secrets created later by anyone who can create secrets
  in the account. The book's approval would stay the same while what it grants grew. If a team needs one, it can
  be added as an explicit form with its own test.
- **Filtering at the proxy.** sbx cannot see inside the TLS. A network-policy@2 path rule cannot help either:
  every Secrets Manager call is a `POST /`, and the operation and secret are in a header and the body.
- **Enumerating the shapes of Secrets Manager hosts in allow rules.** A `*` inside a label, such as
  `*-east-1.amazonaws.com`, matched none of the enumerated shapes and was let through. An entry with a `*` is now
  refused when the text after its last `*` could end any Secrets Manager host, whatever comes before.

Evidence: `TestABookListsSecretsManagerSecretsByExactNameUnderAHostProfile`,
`TestSecretsManagerEntriesAreExactSecretNamesUnderProfileNames`,
`TestNoKitMayAllowASecretsManagerHostWhenSecretsAreListed`.
