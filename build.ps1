$ErrorActionPreference = 'Stop'

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
Push-Location $PSScriptRoot
try {
    New-Item -ItemType Directory -Path 'dist/signup', 'dist/resend', 'dist/confirm', 'dist/challenge', 'dist/passkeyoptions', 'dist/passkeycomplete' -Force | Out-Null

    # Build the packaging tool for the host before cross-compiling the Lambdas.
    $zipTool = Join-Path $PSScriptRoot 'dist/build-lambda-zip.exe'
    & go build -o $zipTool github.com/aws/aws-lambda-go/cmd/build-lambda-zip
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the Lambda ZIP packaging tool.' }

    $env:GOOS = 'linux'
    $env:GOARCH = 'arm64'
    $env:CGO_ENABLED = '0'
    foreach ($name in @('signup', 'resend', 'confirm', 'challenge', 'passkeyoptions', 'passkeycomplete')) {
        $binary = "dist/$name/bootstrap"
        $archive = "dist/$name.zip"
        & go build -trimpath -tags lambda.norpc -o $binary "./cmd/$name"
        if ($LASTEXITCODE -ne 0) { throw "Could not build the $name Lambda." }
        Remove-Item $archive -ErrorAction SilentlyContinue
        & $zipTool -o $archive $binary
        if ($LASTEXITCODE -ne 0) { throw "Could not package the $name Lambda." }
    }
} finally {
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
