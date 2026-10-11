# Source installation status

This repository is deployed from source. It has no confirmed packaged Go client SDK or GitHub release as of the 2026-10-11 documentation audit. Do not invent a `go get ...@v1` command or infer publication from a successful AWS deployment.

Clone the repository, select the intended source commit, and follow [the quickstart](docs/agents/quickstart.md). The module path is `github.com/lambdawalker/go.attestra.aws.auth`; the root and operator modules require Go 1.26.6. Cloud runtime: Lambda `provided.al2023`, Linux ARM64 archives. Pulumi application and shared-index projects have separate Go modules and state.

This file owns installation status, not AWS credentials or architecture. No package publishing/finalization workflow was added. If distribution is introduced, confirm artifact availability before replacing this status and seed immutable release records with exact code/documentation identities.
