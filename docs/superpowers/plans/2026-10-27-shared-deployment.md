# Shared Deployment Implementation Plan

> Execute inline in this session. Approved scope: keep index/admin together and deploy separately from environment backends.

**Goal:** Add `deploy -scope shared` for existing shared infrastructure; default `app` retains the current behavior.

**Architecture:** An independent interactive operation authenticates AWS, selects the account/region's existing shared S3 backend, and holds the same conditional bootstrap lock as setup/teardown. It refreshes existing shared configuration, builds index/admin only, previews and applies the complete shared stack, and reconciles admin MFA without inviting users or modifying grants. Initial provisioning and external DNS changes stay in setup.

**Constraints:** No environment stack operations, backend initialization, environment registry leases, grant creation, or automatic approvals. Retain resources and state on errors. Reject incompatible CLI flags. Use existing credential renewal and vault mechanisms.

## Tasks
- [x] Add scope validation and tests for default app/shared routing and incompatible flags.
- [x] Add existing-stack shared deployment orchestration and tests for isolation, absent stack/config, failed commands, and lock ownership/release.
- [x] Add admin authentication-only setup mode without invitations or grants.
- [x] Document commands and boundaries; run Go tests, vet and documentation checks; push checkpoints and open a PR.
