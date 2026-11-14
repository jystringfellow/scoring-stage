# One Go binary with embedded kits

stage installs as one binary, like sbx. sbx accepts local kits only as directories, so the core kits are embedded
and extracted to a content-addressed directory that a new build never rewrites. The AWS broker is the same binary.
