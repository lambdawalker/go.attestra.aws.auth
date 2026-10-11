# Documentation coverage and validation scope

Current-source audit at `64f4fae`. The API is an HTTPS service, not a published Go consumer SDK. HTML is generated from the same guides listed below; agent retrieval starts at [index](agents/index.md).

| Feature | Human / agent guide | Runnable evidence (from root) | Image policy |
| --- | --- | --- | --- |
| Email signup/resend/confirmation | [API](agents/api.md), [protocol](../README.md#protocol) | `go test ./api ./email` — [handler fixtures](../api/handler_test.go) | Nonvisual service; no screenshots |
| Passkey registration | [API](agents/api.md) | `go test ./passkey` — [source](../passkey/handler.go) | Native UI belongs to Android |
| Email/passkey sign-in, refresh/status | [API](agents/api.md) | `go test ./signin` — [source](../signin/handler.go) | Device validation is separate |
| Capture policy/upload/finalization/cancellation | [Capture](id-capture.md) | `go test -race ./capture ./captureapi ./awscapture` | No ID data in documentation images |
| Index read/publication/concurrency | [Index](environment-index.md) | `go test ./registry`; `go -C tools/deploy test ./internal/registry` | Public example JSON only |
| Setup/credentials/capacity/health/export | [Operations](agents/operations.md), [Android](agents/integration.md) | `go -C tools/deploy test ./...` | Tests use fakes, no cloud mutations |
| Application/shared-index infrastructure | [Deployment](deployment.md), [index](environment-index.md) | `go -C infra test ./...`; `go -C infra-index test ./...` | Pulumi mocks do not prove a live deploy |
| Environment and index teardown | [Environment teardown](teardown.md), [index teardown](teardown-index.md) | `go -C tools/deploy test ./internal/workflow` | Destructive commands not run for docs |
| Parsing/independent validation | [Limitations](agents/limitations.md) | No implemented parser/validation demo | No invented success captures |

## Release automation audit

Existing verify and Deploy AWS workflows remain application tooling. Deploy AWS uses OIDC and environment-scoped configuration with an S3 Pulumi backend. There is no package publication workflow or confirmed GitHub release to finalize; Maven/PyPI delays are inapplicable. [IMPORT.md](../IMPORT.md) states source-only installation. The Documentation workflow builds and optionally publishes only the site, independently of AWS updates.

## Checks and limits

The documentation change runs the documentation build/link/raw/translation checks and selected existing Go suites. Unit tests do not send email, exercise platform passkeys or upload real IDs. The Android Gradle example is checked against its source configuration but needs that repository's toolchain and generated configuration to execute. Historical/UI evidence and architecture are linked from [central docs](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/docs/agents/ui.md).

See the [documentation audit](documentation-audit.md) for the checks actually executed and remaining limits.
