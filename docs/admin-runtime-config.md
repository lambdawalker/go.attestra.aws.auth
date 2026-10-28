# Admin runtime configuration

After setup, the shared console at `https://admin.attestrabond.com` changes application behavior without rebuilding Android or deploying an application stack. Other base domains use `admin.<base-domain>`. The shared `infra-index` stack owns the admin identity boundary and runtime table; each environment's API and capture Lambda read only that environment's runtime partition.

## Setup and access

For the first upgrade of an existing environment, run the full setup wizard:

```powershell
.\setup.bat -Stack dev -force-setup
```

On macOS/Linux, use `./setup.sh -stack dev -force-setup`. Repeat for each existing environment, substituting its name. `deploy.bat` updates only the environment stack; it does not provision the shared admin resources. If deployment reports missing `runtimeTableName` or `runtimeTableArn`, complete this setup first. Do not invent those values or create an unrelated table manually: setup writes both from the shared stack outputs. After setup finishes, ordinary updates can use `.\deploy.bat -Stack dev`.

 Setup reuses the shared state bucket and original shared Pulumi passphrase. It collects the first administrator's lowercase email once, stages both index and admin ACM certificates through the existing Route 53/Cloudflare wizard, waits for validation, then deploys the shared resources and publishes their DNS records. Normal environment deployments reuse the shared resources; they do not deploy the shared stack.

The admin pool is separate from the application user pool, permits administrator-created accounts only, and uses Cognito Essentials managed login. Password sign-in requires authenticator TOTP. Setup creates the initial admin with a cryptographically random temporary password and sends the standard Cognito invitation only when the account does not already exist. Managed login handles changing that password and enrolling TOTP. Keep the account's password and authenticator recovery material through your organization's account-recovery process; self-service recovery is disabled.

Setup configures and reads back required pool MFA, enabled TOTP, WebAuthn `UserVerification=required`, and `FactorConfiguration=MULTI_FACTOR_WITH_USER_VERIFICATION` using AWS SDK v2's current Cognito API. It preserves existing pool properties when enabling the PASSWORD/WEB_AUTHN choices. The older Pulumi provider cannot express the WebAuthn factor configuration, so the shared pool ignores changes to those SDK-owned fields. Setup fails clearly if the region/account rejects the factor or read-back verification differs; it never silently lowers MFA. Repeated setup verifies MFA again and preserves existing accounts and grant state.

Sign in with password and authenticator first. Select **Add a passkey** to visit Cognito's authenticated `/passkeys/add` managed-login flow. Back in the console, select **Activate passkey MFA**. The same-origin API checks that the signed-in user has TOTP enabled and a credential for the configured relying-party domain, then calls `SetUserMFAPreference` to activate WebAuthn MFA while retaining TOTP. A registered, user-verified passkey can then satisfy required MFA; password plus TOTP remains available.

The initial grant is explicit for each configured environment. A permanent per-environment bootstrap marker prevents setup from restoring a revoked grant or silently creating another initial administrator. Additional administrators must first be invited into the dedicated admin pool by an authorized operator, for example with AWS Cognito `AdminCreateUser` (include a strong temporary password), then receive an environment grant from an existing grant manager. The console's grant form uses the administrator's Cognito **subject**, not their email. **Load current grant** retrieves its revision; saving a stale revision conflicts. Active and revoked grants remain durable, and an administrator cannot modify their own grant.

Pool creation, invitations, cloud deployment, and real sign-in were not performed during this implementation.

## Publishing and history

The environment selector contains only active grants for the current admin. Each form load clears the previous environment before requesting new data. Review the proposed value changes, then confirm publication. Both the configuration revision and the caller's current grant revision/active state are checked in one DynamoDB transaction with an immutable audit version. A concurrent publish or revocation returns `409 revision_conflict`; refresh and review again.

The history panel shows the latest 100 revisions with timestamp, actor, and action. Restoring one appends a new revision, retaining all previous audit records. The API can restore any retained revision by its exact number, including revisions older than the displayed 100. Grant writes also have immutable audit rows and an atomic manager-grant check.

The table has on-demand capacity, encryption, point-in-time recovery, DynamoDB deletion protection, and Pulumi protection. Application teardown retains shared runtime history, admin users, and grants. It also retains that environment's override: recreating an environment does not reset approved configuration or restore a revoked grant. Removing the shared stack requires a separately reviewed retention/backup decision.

## Public application contract

`GET https://<environment>.api.attestrabond.com/v1/app-config` is public and returns `Cache-Control: no-store`:

```json
{
  "schemaVersion": 1,
  "environment": "dev",
  "revision": 0,
  "updatedAt": "2026-10-27T12:00:00Z",
  "features": { "idCapture": true },
  "onboarding": {
    "idCapture": "required", "passkey": "required", "password": "disabled",
    "passwordPlacement": "hidden",
    "passwordPolicy": {
      "minLength": 12, "maxLength": 128,
      "lowercase": "allowed", "uppercase": "allowed", "numbers": "allowed", "symbols": "allowed"
    }
  },
  "capture": {
    "documentType": "id_card",
    "jurisdiction": "",
    "purpose": "Scan your ID to provide your identity details. You'll review and confirm the information afterward.",
    "messages": {
      "en": "Scan your ID to provide your identity details. You'll review and confirm the information afterward.",
      "es": "Escanea tu identificación para proporcionar tus datos de identidad. Después podrás revisar y confirmar la información.",
      "fr": "Numérisez votre pièce d’identité pour fournir vos renseignements d’identité. Vous pourrez ensuite les vérifier et les confirmer."
    }
  }
}
```

See [onboarding policy](onboarding-policy.md) for mode/placement validation, legacy normalization, password security and coordinated rollout. The example shows new defaults; it does not describe live environment settings. Only these behavior settings are allowed. There are no credentials, proof keys, identities, routing settings, Cognito identifiers, deployment controls, or build configuration in this document. Environment names match `[a-z][a-z0-9-]{0,31}`; document types match `[a-z][a-z0-9_]{0,63}`. Purpose and jurisdiction are each at most 1024 UTF-8 bytes and contain no control characters. New publications use fixed `id_card`, empty jurisdiction and generic messages in English, Spanish and French. English is required; blank translations fall back to English. Each message is limited to 1024 UTF-8 bytes with no control characters. Legacy fields remain readable; current policy projection uses the generic disclosure and does not request jurisdiction before scanning. Revision is nonnegative and increases on every effective value change; rollback is a new revision. An unavailable or malformed durable configuration returns 503, never an enabled fallback.

Android retrieves this environment-bound HTTPS endpoint at launch and foreground, validates the document, and caches only validated data. Transient failures permit at most 15 minutes of stale use; invalid documents fail closed. On expiry, new capture admission stops while existing status and cancellation remain usable. Updating runtime behavior does not reset authentication. Runtime configuration is distinct from the existing environment index and build-time routing/authentication settings.

## Deployment defaults and consent

Pulumi capture settings are defaults until the first administrator publication. Under the existing signed registry deployment lease, the deployment tool synchronizes defaults before publishing the environment index. The registry lease token/revision is checked atomically with the runtime write, and a monotonic deployment generation rejects stale synchronization. Unchanged defaults retain the public revision and timestamp; changed defaults append an audit revision. A racing admin override wins its revision condition, and all later deployments preserve the override. A failed synchronization retains the existing publication receipt for `-publish-index` recovery. Direct Pulumi/AWS changes bypass this workflow.

Capture policy identity is `capture-sha256-<hash>` over the complete consent-relevant policy, including all localized messages, enabled state, document type, purpose, jurisdiction, slots, type/size limits, and retention. Metadata-only revision changes do not change that identity. Both deployment defaults and admin overrides use this rule.

New capture creation requires `expected_policy_version`, equal to the policy the user reviewed. The backend reads one current policy snapshot before admission, validates the expected identity, and persists that exact snapshot and expected identity with the create intent. A mismatch returns `409 policy_changed`; Android refreshes the disclosure and requires another explicit start. A replay of an existing operation key returns the original record and snapshot even if capture has since been disabled or policy changed. Reusing a key with a different expected identity conflicts. Legacy records omit `policy` because the old policy snapshot cannot be reconstructed truthfully.

## Security and verification boundaries

The console uses authorization-code PKCE S256, a short-lived access token kept only in page memory, and session storage only for the one-time PKCE verifier/state. Callback codes are removed from the URL before token exchange. There are no tokens in URLs or local storage. Reloading requires a new authorization redirect. Static assets have a restrictive CSP, HSTS, no-referrer and no-store headers. APIs are same-origin, require the dedicated issuer/client's access token and scope, and validate Origin on writes. End-user pool tokens are rejected. Runtime API roles have only environment-keyed DynamoDB GetItem permission; the admin role has only the runtime data actions it needs and no Cognito administrative rights. Passkey preference operations use the user's access token, not IAM escalation.

Unit tests, race tests, infrastructure mocks, ARM64 package inspection and a DOM smoke test provide local evidence. They do not certify real Cognito managed login, browser passkeys/TOTP, invitation delivery, ACM/DNS propagation, deployed IAM, live DynamoDB transactions, or Android device behavior. Those remain deployment smoke checks.

Current AWS references: [WebAuthn pool configuration](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_WebAuthnConfigurationType.html), [per-user WebAuthn MFA](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_WebAuthnMfaSettingsType.html), [SetUserMFAPreference](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SetUserMFAPreference.html), and [managed-login endpoints](https://docs.aws.amazon.com/cognito/latest/developerguide/managed-login-endpoints.html).

## Environment reset

Managers can reset a selected environment from the dashboard using its exact name as confirmation. This is separate from publishing runtime settings. See [reset scope and recovery](environment-reset.md). Deploy both backend and shared infrastructure to install the worker, permissions, routes and controls. Existing deployment roles may require setup reconciliation to grant the new resource prefixes.
