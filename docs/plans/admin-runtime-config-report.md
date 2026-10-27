# Admin runtime configuration implementation report

Implemented on `feat/admin-runtime-config` in the Go backend. The binding scope is [the reconstruction plan](admin-runtime-config.md). No AWS deployment, invitation, merge, teardown, or package publication was performed during reconstruction.

## Delivered

- Shared protected DynamoDB runtime table with immutable configuration versions, explicit per-environment grants, transactional optimistic concurrency, transactional grant-revocation/manager checks, audit rows, and rollback as a new revision.
- Invitation-only admin Cognito pool, separate issuer/client and authorizer, required password/TOTP MFA, current-SDK configuration/read-back of user-verified passkey MFA, managed login branding, enrollment and explicit per-user activation preserving TOTP.
- Same-origin production console with PKCE authorization code S256, access tokens only in memory, restrictive CSP, review-before-publish, environment-safe loading, grant revision lookup, history and rollback.
- Public environment-bound `GET /v1/app-config`, schema/byte/control-character validation, disabled defaults and unavailable/invalid fail-closed responses. Public data excludes secrets, identity/routing/authentication/build configuration.
- Shared runtime reads for capture and public configuration. Content-based capture policy identity includes deployment defaults. New create requests require `expected_policy_version`; a mismatch returns `409 policy_changed`. Exact accepted policy and idempotency intent persist; existing-key replay retains the original snapshot despite policy changes/disablement. Legacy records omit unavailable snapshots.
- Signed registry/default synchronization integrated into deployment and publication recovery. Exact lease token/revision checks, monotonic default generation and config revision/override conditions fence stale deployments and racing admin changes. Identical defaults do not append public revisions; admin overrides and revoked bootstrap grants survive reruns.
- Shared admin ACM/DNS staging through the existing setup wizard, first-admin email collection, one-time per-environment initial grants, scoped environment-read IAM, all new Lambda build/package wiring, operator and consumer documentation, and refreshed Spanish integration translation provenance.

## Local verification

All following checks passed on the final code and documentation snapshot (Go 1.26.6):

| Check | Result |
| --- | --- |
| `go test -race ./...` | All root-module tests passed, including runtime, capture, admin API, MFA setup and passkey activation |
| `go vet ./...` | Passed |
| `go -C infra test ./...` | Passed |
| `go -C infra-index test ./...` | Passed, including dedicated admin authorizer/pool and deletion protection assertions |
| `go -C tools/deploy test ./...` | Passed; additional workflow test verifies defaults block index publication on failure and retain the recovery receipt |
| `go -C infra build ./...` | Passed; generated binary removed |
| `go -C infra-index build ./...` | Passed; generated binary removed |
| `go mod tidy` in root, infra, infra-index and tools/deploy | Completed; committed dependency graphs/checksums updated |
| `bash build.sh` | Built 12 unique ARM64 binaries for all 17 application/admin Lambda archives |
| `python3 scripts/check_lambda_archives.py` | All 17 archives passed executable-bootstrap and ARM64 ELF checks |
| `node --check adminapi/web/app.js` | Passed |
| Prettier check for console JS/HTML/CSS | Passed |
| DOM smoke via jsdom | OAuth callback, scoped environment load, review/confirm publication, expected revision/reload, failed-environment admission safety, and absence of persisted tokens passed |
| `sites/test_build.py` | Four tests passed using the pinned Python dependencies |
| `sites/build.py` | HTML/project-base link and anchor validation passed; English/Spanish output uses explicit fallback for untranslated guides |
| `git diff --check` | Passed |

Tests specifically cover invalid public schemas/fields/UTF-8 byte limits, environment boundaries, missing/stale policy identities, exact snapshot/replay and disabled replay, legacy records, stale publication, transactional revocation conditions, default-generation fencing and override preservation, bootstrap markers, wrong admin issuer/client/token type, write origins, strict request bodies, rollback, mandatory MFA/passkey factor read-back, unsupported/weak MFA rejection, TOTP/credential prerequisites and fallback-preserving per-user passkey activation. The actual registry HTTP defaults operation is exercised, not only isolated packages.

## Remaining evidence and operating limits

No live Cognito managed-login, TOTP/passkey browser authentication, invitation delivery, ACM/DNS, IAM, DynamoDB transactions, S3 capture, or Android device behavior was exercised here. Those require the normal staged deployment and smoke gates. A real browser visual check was unavailable: Chromium download failed in this environment; DOM behavior was checked, and console source is formatted for review.

Shared administration currently serves one AWS account/region per shared stack, matching the existing index. Cross-account federation remains outside this implementation. The console displays the latest 100 config revisions; exact retained revisions remain available for API rollback. Additional user invitations are an authorized operator action in Cognito, followed by explicit environment grants. Direct Pulumi/AWS edits and older deployment tooling bypass synchronized-default publication. The admin pool's advanced WebAuthn factor configuration remains SDK-owned until the IaC provider exposes it; setup verifies it and fails instead of weakening MFA.

Android implementation, its CI/device evidence and branch publication are tracked by the coordinating task. This report does not substitute local Go tests for that client evidence.
