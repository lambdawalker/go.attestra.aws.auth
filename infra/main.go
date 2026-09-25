package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cognito"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ses"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() { pulumi.Run(deploy) }
func deploy(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	origin := cfg.Require("appOrigin")
	senderDomain := cfg.Require("senderDomain")
	senderAddress := cfg.Require("senderAddress")
	zone := cfg.Get("route53ZoneId")
	proofKey := cfg.RequireSecret("proofKey")
	if err := validate(origin, senderDomain, senderAddress); err != nil {
		return err
	}
	// Pulumi marks the value secret; the API Lambda alone receives the decoded-key source.
	if v := cfg.Get("proofKey"); v != "" {
		b, e := base64.StdEncoding.DecodeString(v)
		if e != nil || len(b) < 32 {
			return errors.New("proofKey must be base64 of at least 32 random bytes")
		}
	}
	apiArchive, _ := filepath.Abs("../dist/api.zip")
	challengeArchive, _ := filepath.Abs("../dist/challenge.zip")
	for _, path := range []string{apiArchive, challengeArchive} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("build Lambda archives with build.sh or build.ps1 first: %s: %w", path, err)
		}
	}
	region := aws.GetRegionOutput(ctx, aws.GetRegionOutputArgs{}).Name()
	account := aws.GetCallerIdentityOutput(ctx, aws.GetCallerIdentityOutputArgs{}).AccountId()
	table, err := dynamodb.NewTable(ctx, "email-proofs", &dynamodb.TableArgs{BillingMode: pulumi.String("PAY_PER_REQUEST"), HashKey: pulumi.String("id"), Attributes: dynamodb.TableAttributeArray{&dynamodb.TableAttributeArgs{Name: pulumi.String("id"), Type: pulumi.String("S")}}, Ttl: &dynamodb.TableTtlArgs{AttributeName: pulumi.String("ttl"), Enabled: pulumi.Bool(true)}, PointInTimeRecovery: &dynamodb.TablePointInTimeRecoveryArgs{Enabled: pulumi.Bool(true)}, ServerSideEncryption: &dynamodb.TableServerSideEncryptionArgs{Enabled: pulumi.Bool(true)}})
	if err != nil {
		return err
	}
	sender, err := ses.NewDomainIdentity(ctx, "email-sender", &ses.DomainIdentityArgs{Domain: pulumi.String(senderDomain)})
	if err != nil {
		return err
	}
	dkim, err := ses.NewDomainDkim(ctx, "email-sender-dkim", &ses.DomainDkimArgs{Domain: sender.Domain})
	if err != nil {
		return err
	}
	if zone != "" {
		_, err = route53.NewRecord(ctx, "ses-verify", &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: pulumi.String("_amazonses." + senderDomain), Type: pulumi.String("TXT"), Ttl: pulumi.Int(300), Records: pulumi.StringArray{sender.VerificationToken}})
		if err != nil {
			return err
		}
		for n := 0; n < 3; n++ {
			idx := n
			token := dkim.DkimTokens.ApplyT(func(v []string) string { return v[idx] }).(pulumi.StringOutput)
			_, err = route53.NewRecord(ctx, fmt.Sprintf("ses-dkim-%d", n), &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: pulumi.Sprintf("%s._domainkey.%s", token, senderDomain), Type: pulumi.String("CNAME"), Ttl: pulumi.Int(300), Records: pulumi.StringArray{pulumi.Sprintf("%s.dkim.amazonses.com", token)}})
			if err != nil {
				return err
			}
		}
	}
	trust := pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)
	apiRole, err := iam.NewRole(ctx, "email-api-role", &iam.RoleArgs{AssumeRolePolicy: trust})
	if err != nil {
		return err
	}
	challengeRole, err := iam.NewRole(ctx, "challenge-role", &iam.RoleArgs{AssumeRolePolicy: trust})
	if err != nil {
		return err
	}
	for _, v := range []struct {
		name string
		role pulumi.StringOutput
	}{{"email-api-logs", apiRole.Name}, {"challenge-logs", challengeRole.Name}} {
		_, err = iam.NewRolePolicyAttachment(ctx, v.name, &iam.RolePolicyAttachmentArgs{Role: v.role, PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole")})
		if err != nil {
			return err
		}
	}
	trigger, err := lambda.NewFunction(ctx, "cognito-grant-challenge", &lambda.FunctionArgs{Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"), Architectures: pulumi.StringArray{pulumi.String("arm64")}, Role: challengeRole.Arn, Code: pulumi.NewFileArchive(challengeArchive), Timeout: pulumi.Int(10), Environment: &lambda.FunctionEnvironmentArgs{Variables: pulumi.StringMap{"TABLE_NAME": table.Name}}})
	if err != nil {
		return err
	}
	permission, err := lambda.NewPermission(ctx, "allow-cognito-challenge", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: trigger.Name, Principal: pulumi.String("cognito-idp.amazonaws.com"), SourceAccount: account, SourceArn: pulumi.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/*", region, account)})
	if err != nil {
		return err
	}
	challengePolicy := pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:UpdateItem","Resource":%q}]}`, table.Arn)
	challengeGrant, err := iam.NewRolePolicy(ctx, "challenge-permissions", &iam.RolePolicyArgs{Role: challengeRole.ID(), Policy: challengePolicy})
	if err != nil {
		return err
	}
	pool, err := cognito.NewUserPool(ctx, "attesta-users", &cognito.UserPoolArgs{
		UserPoolTier: pulumi.String("ESSENTIALS"), UsernameAttributes: pulumi.StringArray{pulumi.String("email")}, AutoVerifiedAttributes: pulumi.StringArray{pulumi.String("email")}, MfaConfiguration: pulumi.String("OFF"),
		SignInPolicy:          &cognito.UserPoolSignInPolicyArgs{AllowedFirstAuthFactors: pulumi.StringArray{pulumi.String("EMAIL_OTP")}},
		AdminCreateUserConfig: &cognito.UserPoolAdminCreateUserConfigArgs{AllowAdminCreateUserOnly: pulumi.Bool(true)},
		EmailConfiguration:    &cognito.UserPoolEmailConfigurationArgs{EmailSendingAccount: pulumi.String("DEVELOPER"), SourceArn: sender.Arn, FromEmailAddress: pulumi.String(senderAddress)},
		LambdaConfig:          &cognito.UserPoolLambdaConfigArgs{DefineAuthChallenge: trigger.Arn, CreateAuthChallenge: trigger.Arn, VerifyAuthChallengeResponse: trigger.Arn},
	}, pulumi.DependsOn([]pulumi.Resource{permission, challengeGrant}))
	if err != nil {
		return err
	}
	client, err := cognito.NewUserPoolClient(ctx, "public-auth-client", &cognito.UserPoolClientArgs{UserPoolId: pool.ID(), GenerateSecret: pulumi.Bool(false), ExplicitAuthFlows: pulumi.StringArray{pulumi.String("ALLOW_CUSTOM_AUTH"), pulumi.String("ALLOW_USER_AUTH"), pulumi.String("ALLOW_REFRESH_TOKEN_AUTH")}, PreventUserExistenceErrors: pulumi.String("ENABLED"), AuthSessionValidity: pulumi.Int(3)})
	if err != nil {
		return err
	}
	apiPolicy := pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["dynamodb:GetItem","dynamodb:PutItem","dynamodb:UpdateItem"],"Resource":%q},{"Effect":"Allow","Action":["cognito-idp:AdminGetUser","cognito-idp:AdminCreateUser","cognito-idp:AdminInitiateAuth","cognito-idp:AdminRespondToAuthChallenge"],"Resource":%q},{"Effect":"Allow","Action":"ses:SendEmail","Resource":%q}]}`, table.Arn, pool.Arn, sender.Arn)
	apiGrant, err := iam.NewRolePolicy(ctx, "email-api-permissions", &iam.RolePolicyArgs{Role: apiRole.ID(), Policy: apiPolicy})
	if err != nil {
		return err
	}
	apiFn, err := lambda.NewFunction(ctx, "email-api", &lambda.FunctionArgs{Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"), Architectures: pulumi.StringArray{pulumi.String("arm64")}, Role: apiRole.Arn, Code: pulumi.NewFileArchive(apiArchive), Timeout: pulumi.Int(25), MemorySize: pulumi.Int(256), Environment: &lambda.FunctionEnvironmentArgs{Variables: pulumi.StringMap{"TABLE_NAME": table.Name, "POOL_ID": pool.ID(), "CLIENT_ID": client.ID(), "APP_ORIGIN": pulumi.String(origin), "SENDER_ADDRESS": pulumi.String(senderAddress), "PROOF_KEY": proofKey}}}, pulumi.DependsOn([]pulumi.Resource{apiGrant}))
	if err != nil {
		return err
	}
	httpAPI, err := apigatewayv2.NewApi(ctx, "email-http-api", &apigatewayv2.ApiArgs{ProtocolType: pulumi.String("HTTP"), CorsConfiguration: &apigatewayv2.ApiCorsConfigurationArgs{AllowOrigins: pulumi.StringArray{pulumi.String(origin)}, AllowMethods: pulumi.StringArray{pulumi.String("POST")}, AllowHeaders: pulumi.StringArray{pulumi.String("content-type")}, MaxAge: pulumi.Int(300)}})
	if err != nil {
		return err
	}
	integration, err := apigatewayv2.NewIntegration(ctx, "email-integration", &apigatewayv2.IntegrationArgs{ApiId: httpAPI.ID(), IntegrationType: pulumi.String("AWS_PROXY"), IntegrationUri: apiFn.InvokeArn, PayloadFormatVersion: pulumi.String("2.0")})
	if err != nil {
		return err
	}
	for _, path := range []string{"signup", "resend", "confirm"} {
		_, err = apigatewayv2.NewRoute(ctx, "route-"+path, &apigatewayv2.RouteArgs{ApiId: httpAPI.ID(), RouteKey: pulumi.String("POST /" + path), Target: pulumi.Sprintf("integrations/%s", integration.ID())})
		if err != nil {
			return err
		}
	}
	_, err = apigatewayv2.NewStage(ctx, "email-stage", &apigatewayv2.StageArgs{ApiId: httpAPI.ID(), Name: pulumi.String("$default"), AutoDeploy: pulumi.Bool(true), DefaultRouteSettings: &apigatewayv2.StageDefaultRouteSettingsArgs{ThrottlingBurstLimit: pulumi.Int(20), ThrottlingRateLimit: pulumi.Float64(10)}})
	if err != nil {
		return err
	}
	_, err = lambda.NewPermission(ctx, "allow-api", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: apiFn.Name, Principal: pulumi.String("apigateway.amazonaws.com"), SourceArn: pulumi.Sprintf("%s/*/POST/*", httpAPI.ExecutionArn)})
	if err != nil {
		return err
	}
	ctx.Export("apiUrl", httpAPI.ApiEndpoint)
	ctx.Export("userPoolId", pool.ID())
	ctx.Export("clientId", client.ID())
	ctx.Export("sesVerificationRecord", pulumi.Sprintf("_amazonses.%s TXT %s", senderDomain, sender.VerificationToken))
	ctx.Export("sesDkimTokens", dkim.DkimTokens)
	return nil
}
func validate(origin, domain, address string) error {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("appOrigin must be an HTTPS origin")
	}
	if domain == "" || strings.ToLower(address) != address || !strings.HasSuffix(address, "@"+domain) {
		return errors.New("senderAddress must belong to senderDomain")
	}
	return nil
}
