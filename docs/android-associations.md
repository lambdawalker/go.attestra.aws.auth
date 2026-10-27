# Android domain associations

Opt-in, environment-owned hosting for `https://<appOrigin host>/.well-known/assetlinks.json`. Certificate fingerprints are public allowlist entries, not private signing keys. Each passkey developer keeps their own key; QA uses a persistent CI identity. Generate/export its certificate before building an APK using the [Android signing tools](https://github.com/lambdawalker/android.attestra.auth/blob/feat/android-domain-associations/docs/android-signing.md).

## First setup

1. Obtain the developer/CI public association JSON (`packageName`, `sha256CertFingerprints`). Independently review the certificate and permitted environment. Combine fingerprints for the same application into one entry. Do not authorize development identities in production.
2. The Android signing wizard saves a public copy to `~/.attestra/associations/<environment>/developer-associations.json` (Windows: `%USERPROFILE%\.attestra\associations\<environment>\developer-associations.json`). Setup discovers it, displays its source, package names and fingerprints, and asks for approval. Approved entries are merged without removing existing fingerprints; unchanged entries need no new approval. Private keys/passwords are never read from this handoff. Rerun an already-completed setup with `setup.bat -force-setup` to review newly exported data. If no copy is found, use the manual file prompt.

   Run the setup wizard for the selected environment. At **Android association JSON file**, enter that file's path. Blank leaves hosting unchanged. The wizard saves the approved array in `attestra-auth-email:androidAssociations` in the environment's Pulumi YAML. Commit that public configuration for CI deployment.
3. The wizard partially deploys the app certificate using an explicit **us-east-1** provider (required by CloudFront), publishes its validation CNAME through the chosen Cloudflare/Route 53 provider, and waits for ACM.
4. Full deployment creates a private S3 bucket, deterministic JSON object, CloudFront OAC and distribution, and app-host DNS. Cloudflare records are DNS-only; Route 53 uses an A alias. No private signing key is uploaded to AWS.
5. Setup fetches the exact public URL and verifies HTTP 200, `application/json`, no redirects and exact approved content. It waits up to 30 minutes for HTTP readiness, retrying every 30 seconds, then reports a resumable failure. ACM waits remain interruptible. Device verification can take longer.

**Hostname ownership:** this feature owns the app hostname. Do not enable it on a hostname already serving a website without planning a migration. Existing Cloudflare conflicts stop setup; Route 53 does not opt into record overwrites. For an existing website, keep this feature disabled and publish the same approved Digital Asset Links there. This distribution serves only the association object; it does not implement the `/verify-email` browser fallback page. Do not replace a functioning website with it merely to add one file.

Existing wizard runs remember a blank answer. To enable hosting later, set `androidAssociations` directly in the environment YAML to your reviewed public array and rerun setup with `-force-setup` (no key is generated automatically). This also works for updating previously imported entries; an import does not remain linked to the original JSON file.

## Configuration and updates

`androidAssociations` is an array of objects with:

- `packageName`: the installed Android application ID.
- `sha256CertFingerprints`: one or more 32-byte, colon-separated hexadecimal certificate fingerprints. Export the actual certificate; do not enter a placeholder.

The renderer rejects invalid packages, malformed fingerprints and duplicates. It normalizes fingerprints to uppercase and sorts packages/fingerprints for stable output. Each statement includes `delegate_permission/common.handle_all_urls` and `delegate_permission/common.get_login_creds`.

Absence of the configuration means unmanaged hosting. An explicit **empty array `[]`** keeps hosting deployed and publishes an empty association list. To remove one developer, remove only that fingerprint; if it was the last for a package, remove the package entry. Do not delete the entire config key unless you intend Pulumi to remove hosting.

Both setup and **Deploy AWS [Pulumi S3]** run the same Pulumi renderer/object update. GitHub deployment requires the initial certificate/DNS setup to be complete; otherwise it fails with instructions to run setup. Rerun setup after adding this feature so the deployment role receives its CloudFront, association-bucket and us-east-1 ACM permissions. CloudFront permissions cover only the necessary distribution/OAC/tag operations, but are not environment-ID scoped; use separate AWS accounts for stronger isolation.

CloudFront uses managed **CachingDisabled**, and S3 sends `Cache-Control: no-store`. Content updates need no CDN invalidation. Android/credential-provider caches are independent: deleting a fingerprint is not instant signing revocation, does not stop APK signing, and does not revoke backend sessions. Reverify App Links and test actual passkey ceremonies after a change. Certificates, application IDs, relying-party ID and backend verification must remain compatible.

## Lifecycle and checks

Outputs: `appCertificateArn`, `appDomainTarget`, `appDistributionId`, `assetLinksUrl`, `assetLinksContent`. The final environment health check validates the public file. Setup and CI report readiness rather than assuming that an object upload means every device has refreshed.

Environment teardown removes the Pulumi-managed object, private bucket, CloudFront distribution/OAC and certificate, plus approved app DNS evidence through the existing DNS cleanup. Certificate verification uses the ARN's region. The bucket is not force-emptied: unrelated objects stop deletion for manual review. Shared index infrastructure and developer/CI signing keys are not removed. CloudFront disable/delete may take several minutes.

Checks: `go test ./assetlinks`; `go -C infra test ./...` (Pulumi mocks); `go -C tools/deploy test ./...` (workflow/HTTP/teardown fixtures). No live cloud deployment is required for these tests. Signing identities and GitHub secrets must be supplied by the operator; this feature does not create production keys or auto-register certificates discovered during builds.

## Manage approvals without a full deployment

Run the separate root launcher after association hosting has been set up:

```powershell
.\scripts\associations.bat -sso -profile attestra -stack dev -backend s3://YOUR-DEV-STATE-BUCKET
```

```sh
./scripts/associations.sh -sso -profile attestra -stack qa -backend s3://YOUR-QA-STATE-BUCKET
```

The tool uses the existing Go deployment authentication flow (SSO, browser login with `-login`, or prompted AWS credentials) and requests the Pulumi passphrase. Confirm the environment, AWS identity and state backend. No GitHub or Cloudflare token is needed.

The menu lists approvals, adds a public JSON file (defaulting to the local environment handoff), removes one selected fingerprint, or publishes. Add/remove changes remain in memory until Publish; exiting warns about unpublished changes. Publishing displays the exact list for approval, writes the Pulumi config, updates only the existing `android-assetlinks` S3 object using `--target --skip-preview`, then checks the public URL. Removing the final fingerprint retains an empty array so hosting remains deployed.

This skips Lambda builds, full-stack updates and preview. Pulumi still executes/compiles the infrastructure program, which can require the existing local deployment artifacts and configuration. Initial hosting creation remains a setup operation. This tool does not write directly to S3 outside Pulumi ownership.

Commit the changed `infra/Pulumi.<environment>.yaml` so later CI deployments preserve approvals/removals. Do not edit the same stack configuration concurrently from another checkout. If publishing fails, the edited YAML is retained for retry; exiting does not revert it. The local handoff may still contain a removed fingerprint and setup may offer it again—do not reapprove it. Device caches and sessions remain separate from public-file publication.
