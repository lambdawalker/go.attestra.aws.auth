# Attestra AWS authentication

The first implemented subfeature is [email confirmation](https://github.com/lambdawalker/design.attestra/tree/main/auth/onboarding/email-confirmation). This repository contains separate Go Lambdas for signup, resend, and confirm, a one-use Cognito custom-challenge trigger, a DynamoDB proof store, SES delivery, and a Pulumi Go stack. It does not contain passkey registration or ID capture yet.

## Protocol

1. The client generates 32 random bytes A, stores them with the pending signup, and sends `base64url(SHA256(A))` as `code_challenge` with `code_challenge_method: "S256"` to `POST /signup`. The API responds `202 {"request_id":"..."}` for new and existing addresses alike. For an eligible new address it emails an HTTPS link with B and a separately printed six-digit C.
2. The website/app handles `GET https://<appOrigin>/verify-email?request_id=...&b=...`. Loading that route must not call confirmation until the client is ready. With matching local A, it automatically submits A+B. Without A, it shows an empty C field and waits for the user's Verify action before submitting B+C. The website/app provides the actual screens; this backend never handles that GET route.
3. `POST /confirm` atomically claims the pending transaction, creates a passwordless, email-verified Cognito user, then uses a short-lived single-use grant and Cognito CUSTOM_AUTH to issue tokens to this response only. If token issuance fails after confirmation, the response is `409 confirmed_sign_in_required` and the user signs in with Cognito email OTP. An interrupted claim can be retried after 60 seconds only if no Cognito user exists yet. It never returns tokens from a previously confirmed transaction.
4. `POST /resend` rotates B and C while preserving the A challenge. It can also replace a failed proof if Cognito did not create the account. Old links fail. The transaction holds at most five incorrect manual attempts per generation, ten per account per UTC hour, three resends, and a 60-second resend delay. DynamoDB TTL is cleanup; requests check expiry explicitly.

| Method | Route | JSON body | Result |
| --- | --- | --- | --- |
| POST | `/signup` | `{"email":"person@example.com","code_challenge":"<base64url SHA256(A)>","code_challenge_method":"S256"}` | Generic `202 {"request_id":"..."}`; does not prove delivery. |
| POST | `/resend` | `{"request_id":"..."}` | Generic `202 {"status":"accepted"}`. |
| POST | `/confirm` | `{"request_id":"...","token_b":"...","token_a":"<A>"}` **or** `{"request_id":"...","token_b":"...","token_c":"012345"}` | Cognito token set on success; `422 incorrect_code` with attempts remaining, `429 attempt_limit`, `410 link_unusable`, `409 confirmed_sign_in_required` or `confirmation_in_progress`. |

B alone never confirms. A+C together are rejected. An incorrect A or B does not spend a manual-code attempt. The SES message contains B in the link and C in the text; an email processor with access to *both* can still perform manual confirmation. This is email verification, not a second factor. Protect the browser route with HTTPS, a strict referrer policy and no third-party scripts, remove B from the URL after parsing, and store A so a new tab on the same origin can find it. No access token, A, or C belongs in a URL.

## Deployment

Requires Go 1.26.6+, Pulumi, AWS credentials with creation rights, an HTTPS app origin, and an SES sender domain. The app origin hosts the client verification route; the Pulumi stack only provisions the API. Build the four `provided.al2023` ARM64 Lambdas **from the repository root before each `pulumi preview` or `pulumi up`**. Pulumi reads the local `dist/signup.zip`, `dist/resend.zip`, `dist/confirm.zip`, and `dist/challenge.zip` archives; it does not run the build.

### Authenticate to AWS on Windows

Pulumi uses the AWS credentials available to the shell running `pulumi preview` or `pulumi up`. Signing in to pulumi.com does not sign the CLI in to AWS. First, in the **same PowerShell window** you will use for deployment, list your AWS CLI profiles:

```powershell
aws configure list-profiles
```

If your organization uses IAM Identity Center (SSO), select its configured profile and log in:

```powershell
$env:AWS_PROFILE = "your-profile"
aws sso login --profile $env:AWS_PROFILE
aws sts get-caller-identity --profile $env:AWS_PROFILE
```

If you use an access-key profile instead, configure it once with `aws configure --profile your-profile`, then select and verify it:

```powershell
$env:AWS_PROFILE = "your-profile"
aws sts get-caller-identity --profile $env:AWS_PROFILE
```

Verify that `get-caller-identity` shows the AWS account you intend to deploy into before continuing. Configure the Pulumi stack's region and profile from the `infra` directory (set the region to the one you intend to use):

```powershell
cd D:\dev\go.attestra.aws.auth\infra
pulumi config set aws:region us-east-1
pulumi config set aws:profile $env:AWS_PROFILE
pulumi preview
```

If the AWS CLI works but Pulumi still reports `Invalid credentials configured`, inspect `aws configure list` and `pulumi config get aws:profile`. Environment variables such as `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_SESSION_TOKEN` can override a profile; inspect their **names** with `Get-ChildItem Env:AWS_* | Select-Object -ExpandProperty Name`. If they are stale and you intend to use a profile, clear them in this PowerShell window and retry:

```powershell
'AWS_ACCESS_KEY_ID', 'AWS_SECRET_ACCESS_KEY', 'AWS_SESSION_TOKEN' | ForEach-Object {
    Remove-Item "Env:$_" -ErrorAction SilentlyContinue
}
aws sts get-caller-identity --profile $env:AWS_PROFILE
pulumi preview
```

Do not paste access keys, secret keys, or session tokens into an issue or chat. `pulumi config set aws:profile` saves only the profile name; the actual credentials remain in the local AWS configuration.

### Build and deploy

On Windows PowerShell, run:

```powershell
cd D:\dev\go.attestra.aws.auth
.\build.ps1
python scripts/check_lambda_archives.py
cd infra
pulumi preview
pulumi up
```

Run the AWS authentication steps above before this sequence; you only need to create the stack and set the other project config values once. Run `pulumi up` after reviewing the preview.

If PowerShell blocks local scripts, use `powershell -ExecutionPolicy Bypass -File .\build.ps1` (or `pwsh -File .\build.ps1`) from the repository root. The script cross-compiles Linux ARM64 binaries and packages `bootstrap` with executable permissions, which Lambda needs. Do not use `Compress-Archive` for the deployment archives.

On macOS/Linux, run:

```bash
go mod tidy
chmod +x build.sh
./build.sh
cd infra
go mod tidy
pulumi stack init dev
pulumi config set aws:region us-east-2
pulumi config set appOrigin https://attestrabond.com
pulumi config set senderDomain info.attestrabond.com
pulumi config set senderAddress verify@info.attestrabond.com
pulumi config set --secret proofKey "$(openssl rand -base64 32)"
pulumi preview
pulumi up
```

### Configure sender DNS in Cloudflare

This stack creates an Amazon SES identity for `senderDomain` and Easy DKIM in the configured AWS region. For example, with `senderDomain: info.attestrabond.com` and `senderAddress: verify@info.attestrabond.com`, add the records below to the **attestrabond.com** zone in Cloudflare. DNS for this domain stays in Cloudflare: leave `route53ZoneId` unset (or run `pulumi config rm route53ZoneId` in `infra` if it was previously set). A Route 53 zone ID is only for a domain whose authoritative DNS zone is managed by Route 53.

1. Get the values from the **deployed stack**, in PowerShell from the `infra` directory:

   ```powershell
   cd D:\dev\go.attestra.aws.auth\infra
   pulumi stack select dev
   pulumi config get senderDomain
   pulumi stack output sesVerificationRecord
   pulumi stack output sesDkimTokens --json
   ```

   `sesVerificationRecord` is a whole TXT record in the form `_amazonses.info.attestrabond.com TXT <verification-token>`. Its value in Cloudflare is **only** the part after `TXT`. `sesDkimTokens --json` gives an array of three strings; each string is used twice, once in its CNAME name and once in its target as shown below. These values belong to the selected stack and its configured AWS region. Copy the values from *your* outputs, not from a previous preview or an example.

   If `pulumi stack output` says a property is missing after an incomplete first deployment, read the identity already created in SES without starting a new identity verification:

   ```powershell
   $domain = pulumi config get senderDomain
   aws ses get-identity-verification-attributes --identities $domain --region us-east-2 --profile attestra
   aws ses get-identity-dkim-attributes --identities $domain --region us-east-2 --profile attestra
   ```

   In the first JSON response, find `VerificationAttributes[$domain].VerificationToken`; in the second, find `DkimAttributes[$domain].DkimTokens`. Replace the region/profile if yours differ. The same domain appears under **Amazon SES → Configuration → Verified identities** in that region. A subsequent `pulumi preview` can also display the proposed outputs after the SES resources have been created. If SES returns no entry for the domain, confirm the AWS account, region and configured `senderDomain` before using any token.

2. In Cloudflare, open the domain → **DNS** → **Records** → **Add record**. Create the following records. Cloudflare's Name field is relative to the `attestrabond.com` zone in this example; do not append `attestrabond.com` twice.

   | Type | Cloudflare Name (for `attestrabond.com`) | Content / Target |
   | --- | --- | --- |
   | TXT | `_amazonses.info` | The token after `TXT` in `sesVerificationRecord` (paste the token alone). |
   | CNAME, three records | `<token1>._domainkey.info`, `<token2>._domainkey.info`, `<token3>._domainkey.info` | Respectively `<token1>.dkim.amazonses.com`, `<token2>.dkim.amazonses.com`, `<token3>.dkim.amazonses.com`. |

   Replace each `<tokenN>` with one complete string from `sesDkimTokens` (or from SES `DkimTokens` if the stack outputs are missing). Set each CNAME to **DNS only** (gray cloud), not Proxied. Leave TTL on Auto or choose 300 seconds. If the SES console displays a different full target for a CNAME, use the target shown there. These records authenticate sending from the subdomain; they do not move the website or incoming mail to AWS. Do not replace any existing MX records for your mailbox.
3. Check that public DNS returns the records, for example in PowerShell: `Resolve-DnsName -Type TXT _amazonses.info.attestrabond.com` and `Resolve-DnsName -Type CNAME <token1>._domainkey.info.attestrabond.com`. Check all three CNAMEs. Then check SES in the same AWS account and region:

   ```powershell
   aws ses get-identity-verification-attributes --identities info.attestrabond.com --region us-east-2 --profile attestra
   aws ses get-identity-dkim-attributes --identities info.attestrabond.com --region us-east-2 --profile attestra
   ```

   Wait until both `VerificationStatus` and `DkimVerificationStatus` report `Success`. DNS propagation and SES checks can take time. If the initial deployment failed while the identity was unverified, run `pulumi up` again; Cognito's `DEVELOPER` email configuration needs the verified SES identity in its region. If you use a different region or profile, substitute those values in the commands.

The stack does **not** configure a custom MAIL FROM domain or email receiving. Its SES verification and DKIM records do not require you to add an MX or SPF record. If you later configure a custom MAIL FROM domain, follow SES's separate MX and SPF instructions for that domain.

### Enable SES sending for test recipients

SES configuration is required before signup emails can arrive. First, verify the **sender** domain using the DNS records above in the same AWS account and region as this stack. Pulumi creates the SES identity, but DNS hosted by Cloudflare must be configured there manually. After a destroy and redeploy, compare Cloudflare records with the new `sesVerificationRecord` and `sesDkimTokens` outputs; update records if they changed, and confirm the new SES identity is verified.

New SES accounts start in a **regional sandbox**. In the sandbox, SES can send only to verified recipients (or the SES mailbox simulator). Choose one of these paths:

1. **Test with addresses on a domain you control:** in **Amazon SES → Verified identities** in the stack region, add the recipient domain as a **Domain** identity and publish the DKIM CNAME records SES gives you in that domain's DNS. Once verified, addresses on that domain can receive sandbox messages. For a single test address, you can instead add an **Email address** identity and click the AWS verification link sent to that inbox; this needs no recipient-domain DNS changes. Verify the sender domain separately as described above.
2. **Send to arbitrary recipient addresses:** in the same region, open **SES → Account dashboard → View Get set up page → Request production access**. Choose **Transactional**, supply the website URL and contact details, describe user-requested email verification, and submit the request. AWS must approve it; this does not happen automatically with `pulumi up`. Sender identity verification is still required after approval.

`POST /signup` deliberately returns the same `202` for eligible, already registered, and rate-limited addresses; it does **not** prove that SES accepted or delivered an email. The current service also suppresses SES send errors to avoid account enumeration. If a verified test recipient receives nothing, check CloudWatch for the signup Lambda and inspect sandbox status, eligibility and sending diagnostics without logging addresses, links, codes, or proof values. See [AWS SES production access](https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html) for the current regional requirements.

References: [SES domain identities and DKIM](https://docs.aws.amazon.com/ses/latest/dg/creating-identities.html), [Cloudflare DNS record creation](https://developers.cloudflare.com/dns/manage-dns-records/how-to/create-dns-records/), and [SES custom MAIL FROM](https://docs.aws.amazon.com/ses/latest/dg/mail-from.html).

### Configure the Android API URL

In PowerShell, read these values from the deployed `dev` stack in `infra`:

```powershell
cd D:\dev\go.attestra.aws.auth\infra
pulumi stack select dev
pulumi stack output apiUrl
pulumi config get appOrigin
```

Set Android's Gradle property `attestraApiBaseUrl` to the **exact HTTPS origin** returned by `apiUrl`, such as `https://kop22wur83.execute-api.us-east-2.amazonaws.com`. Do not append `/signup`, `/confirm`, `/verify-email`, or a trailing slash: the Android `:auth` module adds API routes itself. Set `attestraLinkHost` to the **hostname** of `appOrigin` (for example, `attestrabond.com` from `https://attestrabond.com`); this is the email verification link host, not the API Gateway host. See the [Android deployment instructions](https://github.com/lambdawalker/android.attestra.auth#configure-the-deployment) for Gradle commands and a local property example. If `pulumi stack output apiUrl` is missing, select the correct stack and finish `pulumi up`; a previewed URL is not a deployed stack output.

Configure the app's Android/iOS HTTPS associations and web route separately. Use the outputs `apiUrl`, `userPoolId`, and `clientId` to configure clients; the custom authentication flow is internal to the Lambda and must never receive client-supplied grants. The Pulumi secret is delivered to the three API Lambda environments; restrict access to Lambda configuration and Pulumi state. IAM roles have scoped DynamoDB, Cognito and SES actions per route.

The Cognito pool permits `EMAIL_OTP` and `PASSWORD` as first factors. Cognito rejects this pool's creation when `PASSWORD` is absent; AWS's CDK also requires it in its allowed-first-factors configuration. This app still creates users **without passwords** (`AdminCreateUser` omits `TemporaryPassword`), and `admin_only` disables self-service password resets. Account recovery in this app means a fresh email OTP sign-in, not Cognito's `ForgotPassword` operation. Do not add password sign-in to the app UI or create passwords for these users. **Security limitation:** Cognito's `ALLOW_USER_AUTH` client also permits password sign-in if a user ever acquires a password, and a signed-in passwordless user can call `ChangePassword` without a previous password. If the product requires password authentication to be impossible at the identity-provider level, this Cognito configuration does not provide that guarantee; choose a different session-issuance architecture before production.

API Gateway sends `POST /signup`, `/resend`, and `/confirm` to separate `email-signup`, `email-resend`, and `email-confirm` Lambdas. They share the same Go service and DynamoDB table, but have separate CloudWatch log groups and IAM roles. A fourth Lambda, `cognito-grant-challenge`, handles Cognito's custom challenge. `pulumi up` replaces the old shared `email-api` Lambda with these three route-specific functions; the `apiUrl` output remains the client-facing base URL.

SES can evaluate `ses:SendEmail` against a verified recipient identity as well as the sender identity while the account is in the sandbox. The signup and resend roles allow SES identity resources in **this AWS account and region**, with a `ses:FromAddress` condition restricting the sender to `senderAddress`. The confirm role has no SES send permission. SES still enforces its separate sandbox recipient verification requirement.

### Diagnosing email delivery

For the current confirmation/session investigation, rebuild the Lambda archives from the repository root (`./build.ps1` on Windows or `./build.sh` elsewhere), then run `pulumi up` in `infra`. In CloudWatch, inspect both the `email-confirm` and `cognito-grant-challenge` Lambda log groups. The temporary `confirm_*`, `cognito_session_*`, and `challenge_*` events show whether the proof was claimed, Cognito created a user, the grant challenge ran, and tokens were issued. Match the Android `EmailDebugX` `trace_id` with the `email-confirm` endpoint log. Test with a **fresh signup and link**, because an already confirmed proof is one-use. These temporary logs include test email addresses, identifiers, and raw provider errors; remove them after diagnosing the failure.

**Custom challenge response gotcha:** Cognito expects its complete trigger event returned with the `response` section updated. Dropping common fields such as `version`, `region`, `userPoolId`, `userName`, or `callerContext` can make `AdminInitiateAuth` fail with `InvalidLambdaResponseException: Unrecognizable lambda output`. The challenge handler preserves the original event, including fields added by Cognito in the future, while changing only its response. A confirmation that already created its Cognito user cannot be replayed for tokens; after deploying this fix, test with a new email signup or use the planned email OTP recovery.

All three endpoints log their route and HTTP status in CloudWatch. Backend failures log a short stage and AWS provider error code; signup and resend also log when work was skipped (for example, a rate limit, existing account, or resend cooldown). Never add raw email addresses, links, one-time codes, or proof tokens to log statements. A successful `signup_send_accepted` or `resend_send_accepted` means SES accepted the API request; it does not prove inbox delivery.

PowerShell checks in [`debug/`](debug) query the deployed SES account and identities without changing AWS resources:

```powershell
./debug/check-ses-account.ps1
./debug/check-ses-identity.ps1
./debug/check-ses-identity.ps1 -Identity isdavid.com
```

Both scripts default to profile `attestra` and region `us-east-2`; override with `-Profile` and `-Region`. The account check reports whether sending is enabled and whether production access is enabled. The identity check reports verification and DKIM status for the sender domain by default; in the SES sandbox, run it for the recipient domain too (or pass a verified recipient email address). A verified sender does not make unverified recipients eligible in the sandbox. These checks require AWS CLI v2 and credentials authorized to read SES state.

To expose dependency failures in a **restricted development stack**, build from the repository root, then configure `diagnosticMode` and redeploy from `infra`:

```sh
./build.sh # use ./build.ps1 on Windows
cd infra
pulumi config set diagnosticMode true
pulumi up
```

With this option, SES send failures and hidden Cognito lookup failures return HTTP 503 with `{"error":"dependency_failure","stage":"ses_send","provider_code":"SESIdentityNotVerified","trace_id":"..."}` (the code varies by failure). Every API response carries the API Gateway request ID in the `x-request-id` header, which you can match with `trace_id` in CloudWatch. Provider messages are never sent to the client. If the log says `signup_skipped reason=account_exists` or `signup_skipped reason=email_rate_limit`, signup intentionally did not send mail. Resend also observes a 60-second cooldown. When finished debugging, run `pulumi config set diagnosticMode false` and `pulumi up`; diagnostic mode reveals whether an address is eligible and must not remain enabled on a public production stack.

The state store is a single DynamoDB table with `id` as its key, conditional writes for transaction claims and failed-attempt counts, budget items keyed by HMAC of email/source, and one-use custom-auth grants. A confirmed or failed transaction expires via TTL. Account creation happens **only after** a proof is accepted, avoiding a public Cognito signup confirmation shortcut. If Cognito account creation succeeds but grant exchange fails, email OTP sign-in is the recovery path. API requests are bounded to 4096 bytes; proofs and token sets must not be logged by clients or infrastructure.

## Checks and rollout

Run `go test ./...` and `go vet ./...` in the root, then `go build ./...` in `infra`. The included GitHub Actions workflow performs those checks plus ARM64 Lambda builds. Before production, test both proof paths, concurrent confirmation, SES delivery, Cognito custom auth, a failed session exchange, account enumeration timing, hourly bucket boundaries, and resend races against a deployed nonproduction stack. This repository is not deployed by CI.

### Passkey registration

After email confirmation issues an access token, the Android app calls `POST /passkeys/options` with `Authorization: Bearer <access_token>`. The response is `{"creation_options": {...}}`. Android Credential Manager creates the credential, then the app sends `POST /passkeys/complete` with the same bearer token and `{"credential": <registration response object>}`. Only `{"registered": true}` confirms completion. A `401 sign_in_required` means the user must obtain a new signed-in session; a `400 invalid_credential` allows a fresh attempt. Cognito stores and verifies the credential. These Lambdas do not log access tokens, challenges, or credentials.

The Pulumi stack enables `WEB_AUTHN` as an allowed first factor, configures Cognito's relying party ID from the hostname of `appOrigin`, and creates one Lambda for each passkey endpoint. Run `build.ps1` (Windows) or `./build.sh` (Unix) before `pulumi up`. The existing pool updates in place; inspect the preview. The user's access token must include `aws.cognito.signin.user.admin`; if it does not, use a compatible Cognito sign-in flow to issue it. Passkey sign-in itself is a later feature; email sign-in remains the recovery path.

For Android, the HTTPS host must serve `/.well-known/assetlinks.json` with `delegate_permission/common.get_login_creds` for the exact application ID and signing fingerprint. Verify the installed build's association with that host before testing Credential Manager on Android 9 or later. The registration endpoints use the same `apiUrl` output as email verification.
