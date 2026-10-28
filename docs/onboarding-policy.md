# Configurable onboarding policy

The revisioned per-environment runtime document controls enrollment. Email verification remains mandatory. Authentication alone does not establish onboarding completion or identity verification.

| Control | Modes | New default |
| --- | --- | --- |
| ID capture | Required, Optional, Disabled | Required |
| Passkey enrollment | Required, Optional, Disabled | Required |
| Password enrollment | Required, Optional, Disabled | Disabled |
| Password placement | With email, After email verification, Hidden | Hidden |

Required steps block completion until the server confirms their durable fact. Optional steps offer an explicit skip. Disabled steps are omitted from onboarding; disabling enrollment does not revoke existing credentials or disable their sign-in. Current policy applies when unfinished onboarding resumes; local skip/deferred flags cannot bypass a newly required step. Unavailable or invalid settings block new completion and offer retry.

## Runtime contract

Schema version remains `1`. `onboarding` contains `idCapture`, `passkey`, and `password` (`required`, `optional`, `disabled`), `passwordPlacement` (`with_email`, `after_verification`, `hidden`) and `passwordPolicy`. Password is disabled if and only if placement is hidden. Reject invalid combinations.

`passwordPolicy` has `minLength`, `maxLength`, `lowercase`, `uppercase`, `numbers`, and `symbols`. Each class is `allowed`, `required`, or `disallowed`. Defaults are 12–128 characters with all four classes allowed. Valid bounds are 8–128, minimum no greater than maximum, and at least one enabled class. Only ASCII letters, digits and printable ASCII punctuation are accepted; no whitespace, trimming or normalization. The server validates the current policy before calling Cognito.

An old document without `onboarding` preserves historical behavior: ID capture is optional when `features.idCapture` is true, otherwise disabled; passkey is required and password disabled/hidden. New defaults instead require capture. New publications derive the legacy `features.idCapture` flag from whether capture is enabled. Reuse existing revision checks, review, audit history and rollback; rollback appends a revision.

## Password and completion boundaries

With-email placement collects the password beside email but retains it only in memory until verification succeeds. Never include it in signup proofs, URLs, logs, saved state or analytics. Process loss or confirmation on another device asks for the password again after verification. After-verification placement collects it only after the session is established.

`POST /auth/password/set` accepts an access-token bearer and `{password}` and returns `{password_registered:true}`. The server validates the verified, pool-bound account and password policy before `AdminSetUserPassword`. Cognito owns the credential; the application does not persist passwords. DynamoDB stores enrollment booleans, not credentials.

`POST /auth/password/start` accepts `{email}`; `POST /auth/password/complete` accepts `{email,session,password}` and follows the Cognito `USER_AUTH` `PASSWORD` challenge. Existing enrolled passwords remain usable if password enrollment is later disabled. Email OTP remains available for recovery.

Authenticated `POST /auth/status` returns `passkey_registered`, `password_registered`, `id_captured`, and `onboarding_complete`. Completion combines current requirements with durable server enrollment facts. The capture service atomically stores a permanent `IDCaptured` boolean in the account record (`ACCOUNT#`, in the capture table) when evidence becomes ready, incrementing the account revision. Status reads that fact independently of capture retention or record expiry; observing status immediately after capture is not required. Legacy ready records backfill the fact when status discovers them. Already expired pre-upgrade records cannot be reconstructed. Enrollment booleans are also retained in the email-proofs store. Cancel/back and local completion flags do not. Capture and parsing never assert identity verification. Refresh status after enrollment or capture before continuing.

## Rollout and verification

Backend and Android must be upgraded together before publishing new requirements. Legacy clients cannot enforce this policy and must upgrade; the compatibility flag is not an enforcement mechanism. Review the effective configuration per environment, exercise required/optional/disabled combinations, password placement and loss/re-entry, policy changes on resume, canceled capture, and authenticated status recovery. Source changes and documentation do not establish deployed readiness. This change does not deploy services or alter running environment settings.

Implementation sources: [runtime validation](../runtimeconfig/onboarding.go), [enrollment and status](../signin/enrollment.go), and [admin runtime configuration](admin-runtime-config.md).
