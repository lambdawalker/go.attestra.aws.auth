# Attestra AWS authentication

The first implemented subfeature is [email confirmation](https://github.com/lambdawalker/design.attestra/tree/main/auth/onboarding/email-confirmation). This repository contains a Go Lambda API, a one-use Cognito custom-challenge trigger, a DynamoDB proof store, SES delivery, and a Pulumi Go stack. It does not contain passkey registration or ID capture yet.

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

Requires Go 1.26.6+, Pulumi, AWS credentials with creation rights, an HTTPS app origin, and an SES sender domain. The app origin hosts the client verification route; the Pulumi stack only provisions the API. Build both `provided.al2023` ARM64 Lambdas **from the repository root before each `pulumi preview` or `pulumi up`**. Pulumi reads the local `dist/api.zip` and `dist/challenge.zip` archives; it does not run the build.

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

The stack does **not** configure a custom MAIL FROM domain or email receiving. Its SES verification and DKIM records do not require you to add an MX or SPF record. If you later configure a custom MAIL FROM domain, follow SES's separate MX and SPF instructions for that domain. SES sandbox restrictions still apply until AWS grants production access; verification alone does not permit sending to arbitrary recipients.

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

Configure the app's Android/iOS HTTPS associations and web route separately. Use the outputs `apiUrl`, `userPoolId`, and `clientId` to configure clients; the custom authentication flow is internal to the Lambda and must never receive client-supplied grants. The Pulumi secret is delivered only to the API Lambda environment; restrict access to Lambda configuration and Pulumi state. IAM roles have scoped DynamoDB, Cognito and SES actions.

The Cognito pool permits `EMAIL_OTP` and `PASSWORD` as first factors. Cognito rejects this pool's creation when `PASSWORD` is absent; AWS's CDK also requires it in its allowed-first-factors configuration. This app still creates users **without passwords** (`AdminCreateUser` omits `TemporaryPassword`), and `admin_only` disables self-service password resets. Account recovery in this app means a fresh email OTP sign-in, not Cognito's `ForgotPassword` operation. Do not add password sign-in to the app UI or create passwords for these users. **Security limitation:** Cognito's `ALLOW_USER_AUTH` client also permits password sign-in if a user ever acquires a password, and a signed-in passwordless user can call `ChangePassword` without a previous password. If the product requires password authentication to be impossible at the identity-provider level, this Cognito configuration does not provide that guarantee; choose a different session-issuance architecture before production.

The state store is a single DynamoDB table with `id` as its key, conditional writes for transaction claims and failed-attempt counts, budget items keyed by HMAC of email/source, and one-use custom-auth grants. A confirmed or failed transaction expires via TTL. Account creation happens **only after** a proof is accepted, avoiding a public Cognito signup confirmation shortcut. If Cognito account creation succeeds but grant exchange fails, email OTP sign-in is the recovery path. API requests are bounded to 4096 bytes; proofs and token sets must not be logged by clients or infrastructure.

## Checks and rollout

Run `go test ./...` and `go vet ./...` in the root, then `go build ./...` in `infra`. The included GitHub Actions workflow performs those checks plus ARM64 Lambda builds. Before production, test both proof paths, concurrent confirmation, SES delivery, Cognito custom auth, a failed session exchange, account enumeration timing, hourly bucket boundaries, and resend races against a deployed nonproduction stack. This repository is not deployed by CI.
