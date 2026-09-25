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
pulumi config set aws:region us-east-1
pulumi config set appOrigin https://app.example.com
pulumi config set senderDomain example.com
pulumi config set senderAddress verify@example.com
pulumi config set --secret proofKey "$(openssl rand -base64 32)"
# Optional when the DNS zone is managed by Route 53:
pulumi config set route53ZoneId Z123456EXAMPLE
pulumi preview
pulumi up
```

SES identity/DKIM DNS records are exported. For DNS outside Route 53, publish them manually and wait for verified status; move SES out of its sandbox before sending to arbitrary recipients. Configure the app's Android/iOS HTTPS associations and web route separately. Use the outputs `apiUrl`, `userPoolId`, and `clientId` to configure clients; the custom authentication flow is internal to the Lambda and must never receive client-supplied grants. The Pulumi secret is delivered only to the API Lambda environment; restrict access to Lambda configuration and Pulumi state. IAM roles have scoped DynamoDB, Cognito and SES actions.

The Cognito pool permits email OTP sign-in and uses `admin_only` for **password reset**. Account recovery in this app means a fresh email OTP sign-in, not Cognito's `ForgotPassword` operation. Keep `PASSWORD` out of `AllowedFirstAuthFactors`: enabling it introduces an additional sign-in path. The explicit account recovery setting prevents Cognito from applying its default self-service password recovery behavior to this passwordless pool.

The state store is a single DynamoDB table with `id` as its key, conditional writes for transaction claims and failed-attempt counts, budget items keyed by HMAC of email/source, and one-use custom-auth grants. A confirmed or failed transaction expires via TTL. Account creation happens **only after** a proof is accepted, avoiding a public Cognito signup confirmation shortcut. If Cognito account creation succeeds but grant exchange fails, email OTP sign-in is the recovery path. API requests are bounded to 4096 bytes; proofs and token sets must not be logged by clients or infrastructure.

## Checks and rollout

Run `go test ./...` and `go vet ./...` in the root, then `go build ./...` in `infra`. The included GitHub Actions workflow performs those checks plus ARM64 Lambda builds. Before production, test both proof paths, concurrent confirmation, SES delivery, Cognito custom auth, a failed session exchange, account enumeration timing, hourly bucket boundaries, and resend races against a deployed nonproduction stack. This repository is not deployed by CI.
