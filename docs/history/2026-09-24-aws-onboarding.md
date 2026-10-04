# AWS Onboarding Implementation Plan (historical)

This is the original execution plan from [`go.onboarding`](https://github.com/lambdawalker/go.onboarding/pull/1), kept to explain how the first implementation was scoped. Its Node/TypeScript steps and unchecked task boxes no longer describe the current Go implementation. Use the [current onboarding architecture](https://github.com/lambdawalker/design.attestra/blob/main/auth/onboarding/architecture.md), [Stitch brief](https://github.com/lambdawalker/design.attestra/blob/main/auth/onboarding/stitch.md), and code repository for current decisions.

> Archived implementation record only. Do not execute these superseded tasks; use the current repository README for build and deployment instructions.

**Goal:** Ship a reusable Go onboarding API and Pulumi AWS stack for email confirmation, immediate sign-in, email OTP recovery, and passkey registration without Cognito-hosted pages.

**Architecture:** Go Lambda invokes Cognito's public user-pool APIs and exposes a small HTTPS API. A Node Lambda formats Cognito's signup message as an application-domain verification link. Pulumi provisions the pool, SES sender identity, Lambda functions and HTTP API. Browser and native app UIs call these endpoints and invoke their platform passkey API themselves.

**Tech Stack:** Go 1.25, AWS SDK for Go v2, AWS Lambda Go, Node 22, TypeScript, Pulumi AWS.

**Spec at the time:** combined design in the original repository; see the [current onboarding architecture](https://github.com/lambdawalker/design.attestra/blob/main/auth/onboarding/architecture.md).

## Global Constraints

- No Cognito managed-login redirect or domain is required.
- Opening the verification URL with GET cannot confirm the account; a user action triggers POST /confirm.
- `ConfirmSignUp.Session` is immediately reused with `InitiateAuth(USER_AUTH)`; expired session falls back to email OTP sign-in.
- Both web and mobile frontends implement passkey platform APIs; the Go server relays registration options and credentials to Cognito.
- Identity and address verification are separate future projects.
- Configuration requires an application HTTPS origin and a verified SES sender domain; DNS records may be published by Pulumi when a Route 53 zone ID is configured.

## Review Focus

- Invalid or expired confirmation session: account stays confirmed and client is told to use email OTP.
- Unexpected identity or malformed credential payload: Cognito rejects without storing a passkey.
- Unauthorized passkey endpoints: no access token means no registration.
- Email scanner GET: only app/web UI route is opened; no state-changing API call.
- Failure or retry during signup: expected Cognito errors remain distinguishable and no false success is returned.

## Tasks

### Task 1: Go onboarding API and tests

**Files:** `go.mod`, `cmd/onboarding/main.go`, `onboarding/service.go`, `onboarding/http.go`, `onboarding/http_test.go`.

**Interfaces:** Exported `onboarding.Service` wraps `SignUp`, `ConfirmSignUp`, `InitiateAuth`, `ResendConfirmationCode`, `RespondToAuthChallenge`, `StartWebAuthnRegistration`, `CompleteWebAuthnRegistration`; HTTP routes consume `Service` and return JSON. A caller supplies bearer access token for passkey endpoints.

- [ ] Write table tests covering missing input, signup, confirmation issuing tokens, expired-session recovery response, email-OTP start/complete, and passkey registration auth.
- [ ] Run `go test ./...` and observe missing code failures.
- [ ] Implement minimal service with an injectable Cognito client and handler. Ensure server-side bounds on body size and do not log secrets.
- [ ] Run `go test ./...` and `go vet ./...` with no failures.
- [ ] Commit the Go implementation.

### Task 2: Custom message Lambda and Pulumi stack

**Files:** `infra/Pulumi.yaml`, `infra/package.json`, `infra/tsconfig.json`, `infra/index.ts`, `infra/email-message/index.mjs`, `infra/email-message/index.test.mjs`.

**Interfaces:** Cognito invokes the message Lambda with `CustomMessage_SignUp` and `CustomMessage_ResendCode`; it returns `emailSubject` and HTML `emailMessage` containing `request.codeParameter`. Pulumi reads `appUrl`, `rpId`, `senderDomain`, `senderAddress`, `goBinaryPath`, and optional `route53ZoneId`. One public app client is shared by app and website for cross-device email confirmation.

- [ ] Write tests of safe link construction, escaping, event filtering, and placeholder preservation; observe tests fail.
- [ ] Implement the message Lambda.
- [ ] Define the Pulumi user pool (Essentials; email OTP and WebAuthn), one public app client, SES sender domain/DKIM, optional Route 53 records, Lambda roles/permissions, API Gateway v2 stage, and strict CORS.
- [ ] Install dependencies and run `npm test` and `npm run typecheck` in `infra`.
- [ ] Commit infrastructure.

### Task 3: Deployment and integration documentation

**Files:** `README.md`, `.gitignore`, `Makefile`.

**Interfaces:** `make build` creates the Go Lambda `bootstrap`; Pulumi references it through `goBinaryPath`. README gives config, DNS, deploy order, public routes, token handling, and app-link/association tasks.

- [ ] Document all required config values and concise request/response examples, including explicit link confirmation and fallback recovery.
- [ ] Build the Go Lambda and run `go test ./...`, `go vet ./...`, `npm test`, and `npm run typecheck`.
- [ ] Self-review README and code against Feature 1 of the spec and fix any discrepancies.
- [ ] Commit documentation and publish the feature branch for review.
