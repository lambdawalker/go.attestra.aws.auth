# Source quickstart

Install Go 1.26.6 or newer, Git, AWS CLI v2, and Pulumi. Use the existing administrative AWS profile for SSO or browser login. Prepare a GitHub token with the permissions listed in the [first-deployment guide](../first-deployment.md) and, when selected, a Cloudflare token restricted to the DNS zone (Zone Read and DNS Edit). There is no published client package to install.

From a checkout of this repository:

```sh
go test ./...
go -C tools/deploy test ./...
```

These use fixtures and do not create an account, send email or upload an ID. For a first real environment run `setup.bat` on Windows or `./setup.sh` on Linux/macOS. This command creates billable cloud resources after the wizard's prompts. Choose the environment first, then authenticate and choose Route 53 or Cloudflare. Optional saved credentials use an encrypted local vault; retain its passphrase independently.

Setup prepares application state, the shared index, IAM/GitHub, SES/ACM prerequisites, DNS and readiness waits, then builds and deploys the application, publishes its configuration, checks health and exports Android properties. Prerequisites must finish before dependent resources. See [the full sequence](../first-deployment.md) for prerequisites and recovery.

For a cloud-free API demonstration, run the [handler tests](../../api/handler_test.go):

```sh
go test ./api ./email ./passkey ./signin ./captureapi ./registry
```

They exercise actual handlers/services with fake dependencies. They are not a local HTTP server, SES simulation or Android emulator. Expected result: Go package tests pass. [Coverage](../coverage.md) links each scenario to source.

After real setup, review and commit the generated Pulumi YAML (encrypted secret values are not plaintext). Keep the passphrases and `.attestra/` receipts private. Load `android-config/<environment>.properties` using the Android app's `-PattestraConfigFile` option. Then test actual email/sign-in and deliberately enabled capture; a health check does not exercise those journeys.
