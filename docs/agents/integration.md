# Android and system integration

The central [repository catalog](https://raw.githubusercontent.com/lambdawalker/design.attestra/main/docs/repositories.md) identifies component owners. Backend source and central requirements have independent revisions; there is no certified cross-repository release matrix yet.

Setup exports `android-config/<environment>.properties`. [Export source](../../tools/deploy/internal/android/export.go) writes these public keys:

| Key | Meaning |
| --- | --- |
| `attestraEnvironment` | Environment name |
| `attestraApiBaseUrl` | Deployed HTTPS API origin |
| `attestraLinkHost` | Application/deep-link hostname |
| `attestraAwsRegion` | AWS region |
| `attestraCognitoUserPoolId` | Public Cognito pool identifier |
| `attestraCognitoClientId` | Public app client identifier |
| `attestraCaptureEnabled` | Deployment default only; runtime configuration and server policy govern |
| `attestraCaptureDocumentType` | Selected document policy type |

In the Android checkout, use the [documented Gradle file option](https://github.com/lambdawalker/android.attestra.auth/blob/main/README.md):

```sh
./gradlew :app:assembleDebug -PattestraConfigFile=../go.attestra.aws.auth/android-config/qa.properties
```

This is a cross-repository source-build example. It requires the Android SDK/toolchain and an existing generated file; it was not executed by the documentation build. Explicit Gradle properties override file values. Missing or malformed settings fail the Android build. Do not interpret a client flag as permission to enable server-side capture.

The backend does not host `/verify-email`, the web application, `assetlinks.json`, or iOS association files. Provision app-domain hosting separately. API and app origins have different purposes. Standard names are `<env>.api.<base-domain>`, `<env>.<base-domain>`, and `<env>.info.<base-domain>` for the sender domain. The shared index uses `index.<base-domain>`; the invitation-only console uses `admin.<base-domain>`.

Fetch [runtime configuration](../admin-runtime-config.md) from the configured environment API at `/v1/app-config` on launch and foreground. Its validated cache permits at most 15 minutes of transient-failure fallback; malformed documents fail closed. This updates behavior without changing routing, Cognito settings, or the authentication session. Capture creation sends `expected_policy_version` for the reviewed policy; `409 policy_changed` requires a refreshed disclosure and a new explicit start.

Automatic Android discovery from the index is not implemented by this backend change. Use the generated properties until the Android owner implements runtime discovery. Camera permissions, bitmap ownership, cancellation on lifecycle changes and secure local storage belong to the Android implementation, whose [capture guide](https://github.com/lambdawalker/android.attestra.auth/blob/main/docs/identity-capture.md) links the concrete client contract.
