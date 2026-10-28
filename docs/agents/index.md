# Backend integration guide

Scope: current `main` source, reviewed at `64f4faea74e71a82795560e4b43e195d820a5242`. This is a deployable service and operator tool, not a published Go client SDK. Read [source installation](../../IMPORT.md).

| Task | Read next | Source owner |
| --- | --- | --- |
| Start from zero or update an environment | [Quickstart](quickstart.md), [operations](operations.md) | `tools/deploy/internal/workflow` |
| Integrate email, passkeys, sessions | [HTTP API](api.md) | `api`, `passkey`, `signin` |
| Capture an ID | [Capture contract](../id-capture.md) | `capture`, `captureapi`, `awscapture` |
| Discover environments or export Android settings | [Integration](integration.md) | `registry`, `internal/android` |
| Recover a failed deploy or remove resources | [Operations](operations.md) | application vs shared-index teardown |
| Assess readiness | [Limitations](limitations.md), [coverage](../coverage.md) | tests and explicit runtime evidence |

A generic signup response does not prove email delivery. Only the confirming client gets a session. Capture `ready` means decoded, pinned evidence. Read `extraction.status` for parsed fields and `reviewed` for user corrections; neither verifies identity. Never log proof values, tokens, ID images or presigned URLs. The index is public configuration, not authentication authority or health monitoring.

System requirements live in [central agent docs](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/docs/agents/index.md), [onboarding](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/auth/onboarding/architecture.md), and [evidence contracts](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/auth/onboarding/id-evidence-contracts.md). Exact serialization lives here; do not infer routes from a prototype.
