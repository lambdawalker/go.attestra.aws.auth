param(
    [string]$Profile = 'attestra',
    [string]$Region = 'us-east-2'
)

aws sesv2 get-account --profile $Profile --region $Region --query '{SendingEnabled:SendingEnabled,ProductionAccessEnabled:ProductionAccessEnabled}' --output json
if ($LASTEXITCODE -ne 0) { throw "SES account check failed (exit code $LASTEXITCODE)." }
