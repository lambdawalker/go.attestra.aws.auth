# Android domain associations implementation plan

Goal: deploy reviewed public Android certificate associations before distributing signed builds.

Architecture: each environment opts in with an `androidAssociations` JSON array in Pulumi configuration. A shared Go validator produces deterministic Digital Asset Links. Pulumi owns a private S3 bucket, public JSON object, CloudFront OAC/distribution and a us-east-1 certificate. Setup handles certificate DNS before full deployment, then hostname DNS and HTTP verification. Android tooling exports certificates and verifies APK identity without uploading private keys or automatically granting trust.

Constraints: branch `feat/android-domain-associations` in both repositories; no live deployment; no invented fingerprints; no password authentication feature; existing website hosting must not be overwritten silently. Production requires explicit certificate approval. Empty association arrays revoke all published entries without destroying hosting.

Tasks:
- [x] Validate/render associations with tests for malformed packages/fingerprints, duplicates and empty revocation.
- [x] Add opt-in hosting, staged certificate handling, IAM, readiness and teardown support; test infrastructure with Pulumi mocks and workflow fixtures.
- [x] Add Android certificate export, optional persistent signing and a manually triggered signed-build workflow with public-file verification.
- [x] Document setup, external-hosting conflict handling, revocation/caching limits and test both repositories; publish matching branches.

Review focus: ACM must use us-east-1; absence of config differs from an empty list; no private key reaches public assets; DNS conflicts stop setup; signed APK identity must match both the declared package and approved fingerprint; missing cloud permissions never mean readiness.

Validation: renderer/unit tests, Pulumi mock tests, deploy-tool suites and documentation build passed locally. Android CI additionally exercises a temporary signed APK. Live AWS/DNS deployment remains an operator action after approving fingerprints.
