# The broker serves only temporary AWS keys

The broker answers only with credentials that carry a session token and expire within about 12 hours, so an IAM
user's long-lived keys never reach a sandbox. The keys a sandbox gets stay usable until they expire. Evidence:
`TestOnlyTemporaryCredentialsThatExpireSoonAreServed`.
