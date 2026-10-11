# Limitations and troubleshooting

| Symptom / boundary | Interpretation and next step |
| --- | --- |
| Missing `dist/capture.zip` during preview | Build application archives first, or use the wizard; Pulumi does not build them |
| SES identity unverified | Check the selected region and staged DNS verification; generic signup acceptance does not prove delivery |
| Cloudflare HTTP 400 authorization header | Enter only the API token, without `Bearer`; check zone-scoped permissions |
| Index readiness timeout | Read the direct/custom URL and last HTTP, DNS or TLS failure; resources remain for rerun |
| Expired AWS token | Renew the selected CLI profile or replace pasted temporary credentials; preserve stack state |
| Quota error for reserved concurrency | Review shared account/region quota and environment capacity policy; do not set zero to mean unreserved |
| Old teardown receipt after recreation | Setup archives completed receipts; unfinished teardown requires recovery first |
| Health passed but email/sign-in fails | Health probes do not create users, deliver mail, exercise platform passkeys or upload IDs |

ID parsing, vision extraction, user review persistence, independent ID validation and address assurance remain designed/deferred features. Capture file safety checks are implemented, but live capture is disabled by default and needs approved policy plus real-device/S3 smoke testing. It supports exactly front/back images under the current policy; do not promise arbitrary document support.

The shared index supports one AWS account/region per instance. Cross-account production isolation and a central publication role require further design. Direct Pulumi/AWS edits bypass synchronization. Public entries may temporarily show the last successful configuration after an interrupted operation. Cognito's provider configuration permits `PASSWORD` even though the app offers passkeys/email OTP; this is a recorded constraint, not a passwordless-provider guarantee.

Go tests and Pulumi mocks cannot certify deployed security or operational readiness. Operator-reported dev/QA setup completion on 2026-10-10 is recorded in the [central evidence ledger](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/docs/compatibility.md); it is not a release certification. Report actual tested source refs, device/region and scenario when adding evidence.

Migration: older root-level wizard receipts move into `.attestra/` when safe; conflicting files/locks stop migration. Do not commit local receipts, credentials, recovery tokens or exports containing private operational metadata. Public Android settings contain no credentials. Keep secret passphrases outside Git even though encrypted Pulumi YAML may be committed.
