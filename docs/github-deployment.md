# GitHub Actions deployment

[Deploy AWS (S3 state)](../.github/workflows/deploy.yml) runs manually from `main`. Choose `preview` (the default) or `deploy`. Both test the Go code, authenticate to AWS, check the existing S3 stack, build all Lambda archives and run Pulumi preview. `deploy` then runs `pulumi up --yes --non-interactive`. It uses the same Go deployment tool as the local scripts, in explicit CI mode.

AWS authentication uses GitHub OIDC to assume an IAM role and obtain temporary credentials. No AWS access-key secret, interactive AWS login/SSO, or Pulumi Cloud API token is used in the workflow. The only application secret required in GitHub is the Pulumi state passphrase.

## Guided environment setup

After pulling `main`, run from the repository root:

```powershell
.\setup-github.bat
# Or:
.\setup-github.ps1
```

On Linux/macOS:

```bash
go -C tools/deploy run . -setup-github
```

This Go terminal form asks for a GitHub personal access token using hidden input. Create a **fine-grained token** limited to this repository with **Administration: read/write**, **Environments: read/write**, **Actions: read**, and the automatically included **Metadata: read** permissions. Authorize the token for the organization if your repository policy requires it. The token is only used in memory for this setup and is never stored in GitHub or a local file. No GitHub CLI or Python installation is required.

Defaults are `lambdawalker/go.attestra.aws.auth`, environment `dev`, region `us-east-2`, state backend `s3://pulumi-state-1p8322nx`, and stack `dev`. Existing environment variable values take precedence. The AWS account ID is derived from an existing role ARN when possible; otherwise enter it. The suggested role name is `attestra-github-deploy`, but you must enter the ARN of an **actual AWS OIDC role** you created using the instructions below.

The script shows the proposed variables before saving. For a new environment it creates a deployment branch restriction for `main`. For an existing environment it preserves protection rules and pre-fills existing values; press Enter to keep them. Existing passphrase secrets cannot be read back: leave the hidden passphrase prompt blank to retain one, or enter and confirm a replacement. Use the **same passphrase used for S3 migration**, not a newly invented password.

The passphrase is encrypted with the environment's GitHub public key before upload. The script verifies saved variables and secret presence. If a later API call fails, earlier successful changes remain and are listed by name; rerun after fixing the issue. It does not delete or roll back existing configuration.

This configures **GitHub only**. It does not create an AWS role, migrate Pulumi state, or start a deployment. Existing environment protections should be reviewed at the settings link printed on completion.

## 1. Finish migration locally

Complete the [S3 migration](deployment.md#one-time-migration-from-pulumi-cloud) and commit the migrated `infra/Pulumi.dev.yaml`, including its passphrase encryption metadata and encrypted configuration. Remove the old `aws:profile` setting as the migration tool does. Keep the passphrase in your password manager.

The workflow does not migrate state, create a missing stack, or fall back to Pulumi Cloud. The checked-in configuration currently needs your local migration result before deployment can succeed. A missing stack, Cloud-encrypted state, profile override, wrong region, or unavailable passphrase stops the run.

## 2. Create the GitHub environment

Open this repository's **Settings → Environments → New environment**, named **dev**. Restrict deployment branches to **main**. Add required reviewers if you want approval before a run receives credentials. That approval happens before the job, not between preview and update.

Add these environment **variables**:

| Name | Value |
| --- | --- |
| `AWS_ACCOUNT_ID` | Your 12-digit AWS account ID |
| `AWS_REGION` | `us-east-2` |
| `AWS_ROLE_ARN` | ARN of the deployment IAM role created below |
| `PULUMI_BACKEND_URL` | `s3://pulumi-state-1p8322nx` |
| `PULUMI_STACK` | `dev` |

Add this environment **secret**:

| Name | Value |
| --- | --- |
| `PULUMI_CONFIG_PASSPHRASE` | The exact passphrase used for the migrated S3 stack |

Use the S3 URL/prefix you actually migrated to. Do not use the old Cloud-qualified stack name `isdavid/attestra-auth-email/dev`; the workflow selects the S3 stack `dev` in this project.

## 3. Configure AWS trust once

In AWS IAM, create an OpenID Connect identity provider if it does not already exist:

- Provider URL: `https://token.actions.githubusercontent.com`
- Audience: `sts.amazonaws.com`

Create an IAM role for GitHub deployment with the following trust policy, replacing `AWS_ACCOUNT_ID`. It must match this repository's exact OIDC subject and the `dev` environment:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::AWS_ACCOUNT_ID:oidc-provider/token.actions.githubusercontent.com"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
        "token.actions.githubusercontent.com:sub": "repo:lambdawalker@100535891/go.attestra.aws.auth@1387537507:environment:dev"
      }
    }
  }]
}
```

GitHub documents immutable owner/repository IDs in subjects for repositories created after July 15, 2026; this repository was created after that date. If your repository uses the legacy subject format instead, use the exact subject `repo:lambdawalker/go.attestra.aws.auth:environment:dev`. Do not use a wildcard repository or environment. The environment branch restriction is important because an environment subject does not itself restrict the branch.

The role needs permissions for the resources managed by `infra/`, including Lambda, IAM role/policy management and restricted `iam:PassRole`, API Gateway, Cognito, DynamoDB, SES, and Route 53 when configured. Include S3, SQS, EventBridge and CloudWatch permissions for the ID-capture resources. Scope permissions to this deployment's resources and account; the trust policy above alone does not grant those permissions. Use a deployment role, not a Lambda runtime role.

Add the following **state-bucket permissions** to the role as well (adjust bucket/prefix if different):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket", "s3:GetBucketVersioning", "s3:GetBucketPublicAccessBlock"],
      "Resource": "arn:aws:s3:::pulumi-state-1p8322nx"
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::pulumi-state-1p8322nx/.pulumi/*"
    }
  ]
}
```

An SSE-KMS bucket additionally needs appropriate KMS key access. The state bucket must belong to this AWS account, have versioning enabled, and have all four bucket-level public access block settings enabled. Even preview mode needs state lock write/delete permissions. The workflow requests a one-hour AWS session and checks the returned account ID.

## 4. Run

After merging the workflow and migration configuration into `main`:

1. Open **Actions → Deploy AWS (S3 state) → Run workflow**.
2. Select branch **main** and operation **preview**.
3. Inspect the build and Pulumi preview logs.
4. Run again with operation **deploy** when ready.

A deployment run previews again against its own checked-out commit and the current state. It does not apply a saved plan from an earlier preview run. Use environment approval to review the selected commit before deployment. The workflow never runs automatically on push or pull request, and rejects non-main refs.

All dev runs share a concurrency group and running updates are not automatically cancelled. Local deployments still rely on Pulumi's S3 state lock; coordinate local work and CI. Do not manually cancel an active update unless necessary. If a run is interrupted, inspect the stack/lock before retrying rather than deleting state.

No cloud credentials or decrypted state are uploaded as artifacts. The Go CI mode uses environment credentials only, prohibits interactive login/migration/git pull, and removes credential settings from build subprocesses. Failures stop the sequence; a failed preview never proceeds to update.

## References

- [GitHub OIDC with AWS](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-aws)
- [AWS credentials action](https://github.com/aws-actions/configure-aws-credentials)
- [Pulumi S3 backend](https://www.pulumi.com/docs/iac/operations/stack-management/using-a-diy-backend/)
