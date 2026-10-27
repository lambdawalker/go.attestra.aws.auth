# Admin runtime configuration

Authorized by David: rebuild lost implementation and push incremental checkpoints to feat/admin-runtime-config in both repositories. Do not deploy, merge, or send invitations now.

## Contract
Dashboard admin.attestrabond.com publishes runtime settings directly. Dedicated invitation-only Cognito admin pool: email/password plus authenticator TOTP; alternative user-verified passkey. First admin email collected in setup. Per-environment explicit grants, audit versions, optimistic revision concurrency, rollback as new revision. Dynamo durable store; deployment defaults must never overwrite admin overrides or restore revoked grants. Public GET https://<env>.api.attestrabond.com/v1/app-config. JSON schemaVersion=1, environment, revision>=0, updatedAt, features.idCapture boolean, capture.documentType/jurisdiction/purpose strings. Disabled default, sample_card cannot be enabled; enabled requires nonempty purpose/jurisdiction. No secrets or identity/routing/build settings in public config.

Android fetch on launch/foreground; HTTPS environment-bound endpoint and cache. Maximum 15 minute stale fallback for transient failures only; invalid document fails closed. Disable new capture after expiry, keep existing status/cancel usable. No reset of authentication on runtime updates.

Capture policy content-sensitive identity, including deployment defaults. Create request field expected_policy_version binds policy user reviewed. Persist expected version with idempotency intent. Backend single policy snapshot validates identity before NEW creation, mismatch 409 policy_changed. Existing-key replay returns original record, original policy snapshot. Android refresh must redisplay changed disclosure and require another explicit start.

## Tasks
1. Go backend, dashboard, infrastructure and setup. Tests and incremental pushes. Implement no mock production behavior. Separate admin JWT issuer/client validation, server-enforced grants, transactional publish, protected table, least privilege. PKCE authorization code, no tokens in URLs/local storage, CSP and same-origin APIs. Configure Cognito MFA/passkey accurately using current AWS documentation, fail setup clearly if unsupported. Stage ACM validation/DNS via existing wizard. Public config and capture read shared table, build packages all new Lambdas. Default synchronization fenced against stale deployments. Document setup and runtime behavior.
2. Android runtime client and capture admission/consent integration. Test validation, expiry, fallback, policy-change race, idempotent replay and disabled management. Update docs.
3. Review both branches, fix findings, run feasible verification, push all commits. Report actual limitations.
