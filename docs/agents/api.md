# HTTP integration reference

All application calls use the deployed API origin, not `appOrigin`. Transport types and status mappings are owned by the linked source and [existing protocol reference](../../README.md#protocol). Authentication tokens and temporary flow sessions must be stored securely by the client; do not put them in URLs or logs.

| Method and path | Input and authentication | Success / recovery |
| --- | --- | --- |
| POST `/signup` | `email`, `code_challenge`, `code_challenge_method: S256` | Generic 202 with `request_id`; no delivery/account-existence guarantee |
| POST `/resend` | `request_id` | Generic 202; server cooldown/budgets still apply |
| POST `/confirm` | `request_id`, `token_b`, and exactly one of `token_a` or `token_c` | Session on success; 422 incorrect code, 429 attempts, 410 unusable link, 409 confirmation recovery |
| POST `/passkeys/options` | Bearer access token | `creation_options`; pass to native credential provider |
| POST `/passkeys/complete` | Bearer access token; JSON `credential` | Registration result after server acceptance; cancellation is not success |
| POST `/auth/email/start` | `email` | Cognito challenge/session; source result is authoritative |
| POST `/auth/email/complete` | `email`, `session`, `code` | Token result; rejected challenge requires recovery |
| POST `/auth/passkey/start` | `email` | WebAuthn challenge/session for native provider |
| POST `/auth/passkey/complete` | `email`, `session`, JSON `credential` | Token result after assertion verification |
| POST `/auth/refresh` | `refresh_token` | Refreshed tokens or authentication failure |
| POST `/auth/status` | Bearer access token | `passkey_registered` boolean; 401 without a valid session |

[Email handlers](../../api/handler.go), [passkey handlers](../../passkey/handler.go), [sign-in handlers](../../signin/handler.go), and [sign-in service types](../../signin/service.go) define the complete concrete behavior. These Go handlers accept a `context.Context` and API Gateway v2 request and return a v2 response/error; Lambda entry points are in `cmd/`. Consumers should integrate through HTTPS rather than import internal deployment packages as an SDK.

Passkey failures map to 401 `sign_in_required`, 400 `invalid_credential`, or 503 `temporarily_unavailable`. Sign-in failures map to 401 `sign_in_required`, 400 `authentication_failed`, or 503 `temporarily_unavailable`. Do not retry a consumed proof to regain a lost session; use sign-in recovery. A GET of an email link cannot confirm an account.

## Capture and index

The [capture API reference](../id-capture.md#http-contract) owns every capture route, body, state, error, checksum, revision and retention limit. Fetch document policy first. Preserve operation keys across uncertain retries; PUT bytes to S3 using the exact returned headers, then finalize the selected front/back upload IDs. Poll real status; cancellation and evidence cleanup are asynchronous. Never upload via a Lambda request body.

The [index contract](../environment-index.md#public-response) exposes public GET `/v1/environments` with `schemaVersion: 1`. POST `/v1/environments/<environment>/changes` requires AWS SigV4 and environment-scoped IAM permission. This is an operator endpoint; mobile clients must not receive AWS deployment credentials. `registry/registry.go` owns the public types and conditional-write behavior.
