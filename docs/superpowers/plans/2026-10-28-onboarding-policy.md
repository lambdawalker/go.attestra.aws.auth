# Configurable Onboarding Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans. Checkboxes track deliverables.

**Goal:** Administrators control enrollment requirements and password rules across backend and Android.
**Architecture:** Extend the existing versioned runtime configuration and preserve its review/history/rollback. Cognito owns credentials; the server returns completion facts, and Android renders the configured journey.
**Tech Stack:** Go, Pulumi, vanilla JS admin console, Kotlin Compose.
**Spec:** ../specs/2026-10-28-onboarding-policy-design.md

## Global Constraints
- Required / Optional / Disabled labels; defaults required ID, required passkey, password disabled and hidden.
- Preserve email verification proofs and email OTP recovery; never persist/log raw passwords.
- Legacy documents retain their capture enablement; no silent overwrite of admin overrides.
- No deployment or merge. Feature branches in fresh isolated checkouts.

## Review Focus
- Missing versus malformed config: legacy migration is explicit; malformed policy fails closed.
- Capture cancellation and stale local skip flags never satisfy required onboarding.
- Cross-pool tokens cannot set a password for another account.
- Lost password entry after process death asks again without disk persistence.
- Admin revision conflict and rollback include every new field.

### Task 1: Runtime policy and console
- [x] Add failing tests in runtimeconfig/onboarding_test.go for defaults, legacy normalization, contradictory flags, password class/length validation, invalid enums.
- [x] Implement runtimeconfig/onboarding.go; wire model, store normalization and default synchronization; maintain legacy features flag.
- [x] Extend adminapi/web controls, review/restore, HTML validation and interaction tests with node --test.
- [x] Run go test ./runtimeconfig ./adminapi and JS tests.

### Task 2: Authentication and authoritative completion
- [x] Add failing signin onboarding/password tests for verified account binding, invalid password, disabled enrollment, provider failure, required-step completion and durable capture facts.
- [x] Add authenticated enrollment, password sign-in and status contracts from spec; wire Dynamo/Cognito, IAM, build archives and reset resources.
- [x] Run go test ./... and go vet ./...; infra and deployment tool tests, package build checks.

### Task 3: Android
- [x] Add runtime model/parser and routing/password-validation tests before implementation.
- [x] Consume shared contract, add password field/strength/requirements, setup and sign-in; hide skips for required steps, omit disabled steps, refresh server completion after capture exits.
- [x] Wire runtime lifecycle/expiry and fail-closed UI; retain no raw password in saved state.
- [x] Run :auth:testDebugUnitTest :identity-mock:testDebugUnitTest :app:testDebugUnitTest :app:assembleDebug :app:assembleRelease; report unavailable device/toolchain checks.

### Task 4: Documentation and review
- [x] Update central design, backend API/runtime docs and Android integration docs, distinguishing legacy behavior and new defaults.
- [x] Run documentation link/build checks and independent whole-change review; address important findings and rerun affected tests.
- [x] Commit and open linked reviewable PRs, without merge or deployment.

## Verification and implementation decisions

- Backend full Go suite and race suite, vet, infrastructure, shared infrastructure, deployment tooling, and five dashboard interaction tests pass. All 21 Lambda archives validate as ARM64 executable packages.
- Independent review identified two important gaps: durable capture completion before first status read, and incomplete explicit Android password policies. Fixed with atomic account completion and required policy keys, with regression tests.
- Password length bounds are 8–128 (dormant default 12–128), printable ASCII only. Strength feedback is a local heuristic separate from rule compliance.
- Existing explicit capture deployment settings and published admin overrides are preserved. New unset deployment defaults require ID; legacy documents preserve earlier enablement/optional semantics. Upgrading an existing override to required is an explicit dashboard publication.
- A verified authentication session precedes onboarding completion. Capture counts when server file validation reaches ready; it does not assert identity verification. A permanent account boolean survives evidence retention, with legacy ready-record backfill. Pre-upgrade expired evidence cannot be reconstructed.
- Password entered with email stays only in memory, bound to email and verification request. Process loss or cross-device confirmation requires re-entry.
- No AWS deployment, main-branch merge, or live environment mutation is part of this change. Real Cognito/device lifecycle checks remain deployment smoke tests.
- Backend documentation tests/build pass. Central documentation unit tests pass, but full build is blocked by an existing screenshot SHA mismatch in email_verification_manual_code_input/screen.png. Screenshot and manifest are unchanged.
