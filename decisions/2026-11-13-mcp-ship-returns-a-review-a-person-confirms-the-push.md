# MCP ship returns a review; a person confirms the push

Ship's interactive question is the person's chance to inspect what leaves the sandbox. An agent reading its
own review is not that person, and the caller may be following repository or sandbox text. Even an agent branch
can trigger CI running code with repository secrets. The MCP tool therefore shares preparation and inspection,
returns commits, diffstat and a shell-quoted CLI command, and never pushes or records a ship. Namespace and
protected-path checks still apply; protected paths are refused outright. Rejected: a yes input or a configurable
opt-in to push, each of which would let an agent take the person's decision. Pull request creation is absent too.
Evidence: `TestMcpShipReturnsTheReviewAndCommandAndNeverPushes`,
`TestMcpShipRefusesProtectedPathsAndNothingToShip`.
