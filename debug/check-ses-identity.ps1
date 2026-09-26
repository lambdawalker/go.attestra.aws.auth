param(
    [string]$Profile = 'attestra',
    [string]$Region = 'us-east-2',
    [string]$Identity = 'info.attestrabond.com'
)

aws sesv2 get-email-identity --profile $Profile --region $Region --email-identity $Identity --query '{VerifiedForSending:VerifiedForSendingStatus,DKIM:DkimAttributes.Status,IdentityType:IdentityType}' --output json
if ($LASTEXITCODE -ne 0) { throw "SES identity check failed (exit code $LASTEXITCODE)." }
