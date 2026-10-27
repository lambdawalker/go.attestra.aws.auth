package main

import (
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/acm"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cognito"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"path/filepath"
	"strings"
)

// The shared stack owns the invitation-only identity boundary and runtime data.
func deployAdmin(ctx *pulumi.Context, cfg *config.Config) (*dynamodb.Table, error) {
	domain := cfg.Require("adminDomain")
	zone := cfg.Get("route53ZoneId")
	cert, e := acm.NewCertificate(ctx, "admin-certificate", &acm.CertificateArgs{DomainName: pulumi.String(domain), ValidationMethod: pulumi.String("DNS")})
	if e != nil {
		return nil, e
	}
	ctx.Export("adminCertificateArn", cert.Arn)
	if zone != "" {
		o := cert.DomainValidationOptions.Index(pulumi.Int(0))
		_, e = route53.NewRecord(ctx, "admin-certificate-dns", &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: o.ResourceRecordName().Elem(), Type: o.ResourceRecordType().Elem(), Records: pulumi.StringArray{o.ResourceRecordValue().Elem()}, Ttl: pulumi.Int(300)})
		if e != nil {
			return nil, e
		}
	}
	validation, e := acm.NewCertificateValidation(ctx, "admin-certificate-validation", &acm.CertificateValidationArgs{CertificateArn: cert.Arn})
	if e != nil {
		return nil, e
	}
	table, e := dynamodb.NewTable(ctx, "admin-runtime", &dynamodb.TableArgs{BillingMode: pulumi.String("PAY_PER_REQUEST"), HashKey: pulumi.String("id"), RangeKey: pulumi.String("sk"), Attributes: dynamodb.TableAttributeArray{&dynamodb.TableAttributeArgs{Name: pulumi.String("id"), Type: pulumi.String("S")}, &dynamodb.TableAttributeArgs{Name: pulumi.String("sk"), Type: pulumi.String("S")}}, DeletionProtectionEnabled: pulumi.Bool(true), PointInTimeRecovery: &dynamodb.TablePointInTimeRecoveryArgs{Enabled: pulumi.Bool(true)}, ServerSideEncryption: &dynamodb.TableServerSideEncryptionArgs{Enabled: pulumi.Bool(true)}}, pulumi.Protect(true))
	if e != nil {
		return nil, e
	}
	// Create password+TOTP first. admin-setup configures the current Cognito MFA passkey factor through the SDK,
	// then enables WEB_AUTHN. Older Pulumi providers cannot express FactorConfiguration.
	pool, e := cognito.NewUserPool(ctx, "attestra-admins", &cognito.UserPoolArgs{UserPoolTier: pulumi.String("ESSENTIALS"), UsernameAttributes: pulumi.StringArray{pulumi.String("email")}, AutoVerifiedAttributes: pulumi.StringArray{pulumi.String("email")}, AdminCreateUserConfig: &cognito.UserPoolAdminCreateUserConfigArgs{AllowAdminCreateUserOnly: pulumi.Bool(true)}, MfaConfiguration: pulumi.String("ON"), SoftwareTokenMfaConfiguration: &cognito.UserPoolSoftwareTokenMfaConfigurationArgs{Enabled: pulumi.Bool(true)}, PasswordPolicy: &cognito.UserPoolPasswordPolicyArgs{MinimumLength: pulumi.Int(14), RequireLowercase: pulumi.Bool(true), RequireUppercase: pulumi.Bool(true), RequireNumbers: pulumi.Bool(true), RequireSymbols: pulumi.Bool(true), TemporaryPasswordValidityDays: pulumi.Int(7)}, AccountRecoverySetting: &cognito.UserPoolAccountRecoverySettingArgs{RecoveryMechanisms: cognito.UserPoolAccountRecoverySettingRecoveryMechanismArray{&cognito.UserPoolAccountRecoverySettingRecoveryMechanismArgs{Name: pulumi.String("admin_only"), Priority: pulumi.Int(1)}}}}, pulumi.Protect(true), pulumi.IgnoreChanges([]string{"signInPolicy", "webAuthnConfiguration"}))
	if e != nil {
		return nil, e
	}
	prefix := pool.ID().ToStringOutput().ApplyT(func(id string) string { return "attestra-admin-" + strings.ReplaceAll(strings.ToLower(id), "_", "-") }).(pulumi.StringOutput)
	login, e := cognito.NewUserPoolDomain(ctx, "admin-login", &cognito.UserPoolDomainArgs{UserPoolId: pool.ID(), Domain: prefix, ManagedLoginVersion: pulumi.Int(2)})
	if e != nil {
		return nil, e
	}
	client, e := cognito.NewUserPoolClient(ctx, "admin-console-client", &cognito.UserPoolClientArgs{UserPoolId: pool.ID(), GenerateSecret: pulumi.Bool(false), AllowedOauthFlowsUserPoolClient: pulumi.Bool(true), AllowedOauthFlows: pulumi.StringArray{pulumi.String("code")}, AllowedOauthScopes: pulumi.StringArray{pulumi.String("openid"), pulumi.String("email"), pulumi.String("aws.cognito.signin.user.admin")}, CallbackUrls: pulumi.StringArray{pulumi.String("https://" + domain + "/callback")}, LogoutUrls: pulumi.StringArray{pulumi.String("https://" + domain + "/")}, SupportedIdentityProviders: pulumi.StringArray{pulumi.String("COGNITO")}, ExplicitAuthFlows: pulumi.StringArray{pulumi.String("ALLOW_USER_AUTH")}, AccessTokenValidity: pulumi.Int(15), IdTokenValidity: pulumi.Int(15), TokenValidityUnits: &cognito.UserPoolClientTokenValidityUnitsArgs{AccessToken: pulumi.String("minutes"), IdToken: pulumi.String("minutes")}, PreventUserExistenceErrors: pulumi.String("ENABLED")})
	if e != nil {
		return nil, e
	}
	_, e = cognito.NewManagedLoginBranding(ctx, "admin-branding", &cognito.ManagedLoginBrandingArgs{UserPoolId: pool.ID(), ClientId: client.ID(), UseCognitoProvidedValues: pulumi.Bool(true)})
	if e != nil {
		return nil, e
	}
	region := aws.GetRegionOutput(ctx, aws.GetRegionOutputArgs{}).Name()
	issuer := pulumi.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", region, pool.ID())
	loginURL := pulumi.Sprintf("https://%s.auth.%s.amazoncognito.com", login.Domain, region)
	role, e := iam.NewRole(ctx, "admin-role", &iam.RoleArgs{AssumeRolePolicy: pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)})
	if e != nil {
		return nil, e
	}
	_, e = iam.NewRolePolicyAttachment(ctx, "admin-logs", &iam.RolePolicyAttachmentArgs{Role: role.Name, PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole")})
	if e != nil {
		return nil, e
	}
	grant, e := iam.NewRolePolicy(ctx, "admin-data", &iam.RolePolicyArgs{Role: role.Name, Policy: pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["dynamodb:GetItem","dynamodb:Query","dynamodb:PutItem","dynamodb:ConditionCheckItem"],"Resource":%q}]}`, table.Arn)})
	if e != nil {
		return nil, e
	}
	archive, _ := filepath.Abs("../dist/admin.zip")
	fn, e := lambda.NewFunction(ctx, "admin-handler", &lambda.FunctionArgs{Role: role.Arn, Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"), Architectures: pulumi.StringArray{pulumi.String("arm64")}, Code: pulumi.NewFileArchive(archive), Timeout: pulumi.Int(20), MemorySize: pulumi.Int(256), Environment: &lambda.FunctionEnvironmentArgs{Variables: pulumi.StringMap{"RUNTIME_TABLE": table.Name, "ADMIN_ISSUER": issuer, "ADMIN_CLIENT_ID": client.ID(), "ADMIN_ORIGIN": pulumi.String("https://" + domain), "ADMIN_LOGIN_DOMAIN": loginURL}}}, pulumi.DependsOn([]pulumi.Resource{grant}))
	if e != nil {
		return nil, e
	}
	api, e := apigatewayv2.NewApi(ctx, "admin-api", &apigatewayv2.ApiArgs{ProtocolType: pulumi.String("HTTP")})
	if e != nil {
		return nil, e
	}
	integration, e := apigatewayv2.NewIntegration(ctx, "admin-integration", &apigatewayv2.IntegrationArgs{ApiId: api.ID(), IntegrationType: pulumi.String("AWS_PROXY"), IntegrationUri: fn.InvokeArn, PayloadFormatVersion: pulumi.String("2.0")})
	if e != nil {
		return nil, e
	}
	auth, e := apigatewayv2.NewAuthorizer(ctx, "admin-jwt", &apigatewayv2.AuthorizerArgs{ApiId: api.ID(), AuthorizerType: pulumi.String("JWT"), IdentitySources: pulumi.StringArray{pulumi.String("$request.header.Authorization")}, JwtConfiguration: &apigatewayv2.AuthorizerJwtConfigurationArgs{Audiences: pulumi.StringArray{client.ID()}, Issuer: issuer}})
	if e != nil {
		return nil, e
	}
	for _, r := range []struct {
		name, path string
		protected  bool
	}{{"home", "GET /", false}, {"callback", "GET /callback", false}, {"script", "GET /app.js", false}, {"style", "GET /style.css", false}, {"settings", "GET /settings", false}, {"passkey-activate", "POST /api/passkey/activate", true}, {"environments", "GET /api/environments", true}, {"read", "GET /api/environments/{environment}", true}, {"history", "GET /api/environments/{environment}/history", true}, {"publish", "POST /api/environments/{environment}", true}, {"grant-read", "GET /api/environments/{environment}/grants", true}, {"grants", "POST /api/environments/{environment}/grants", true}} {
		args := &apigatewayv2.RouteArgs{ApiId: api.ID(), RouteKey: pulumi.String(r.path), Target: pulumi.Sprintf("integrations/%s", integration.ID())}
		if r.protected {
			args.AuthorizationType = pulumi.String("JWT")
			args.AuthorizerId = auth.ID()
			args.AuthorizationScopes = pulumi.StringArray{pulumi.String("aws.cognito.signin.user.admin")}
		}
		if _, e = apigatewayv2.NewRoute(ctx, "admin-"+r.name, args); e != nil {
			return nil, e
		}
	}
	_, e = lambda.NewPermission(ctx, "admin-invoke", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: fn.Name, Principal: pulumi.String("apigateway.amazonaws.com"), SourceArn: pulumi.Sprintf("%s/*/*", api.ExecutionArn)})
	if e != nil {
		return nil, e
	}
	stage, e := apigatewayv2.NewStage(ctx, "admin-stage", &apigatewayv2.StageArgs{ApiId: api.ID(), Name: pulumi.String("$default"), AutoDeploy: pulumi.Bool(true), DefaultRouteSettings: &apigatewayv2.StageDefaultRouteSettingsArgs{ThrottlingBurstLimit: pulumi.Int(10), ThrottlingRateLimit: pulumi.Float64(5)}})
	if e != nil {
		return nil, e
	}
	custom, e := apigatewayv2.NewDomainName(ctx, "admin-domain", &apigatewayv2.DomainNameArgs{DomainName: pulumi.String(domain), DomainNameConfiguration: &apigatewayv2.DomainNameDomainNameConfigurationArgs{CertificateArn: validation.CertificateArn, EndpointType: pulumi.String("REGIONAL"), SecurityPolicy: pulumi.String("TLS_1_2")}})
	if e != nil {
		return nil, e
	}
	_, e = apigatewayv2.NewApiMapping(ctx, "admin-mapping", &apigatewayv2.ApiMappingArgs{ApiId: api.ID(), DomainName: custom.ID(), Stage: stage.Name})
	if e != nil {
		return nil, e
	}
	target := custom.DomainNameConfiguration.TargetDomainName().Elem()
	if zone != "" {
		_, e = route53.NewRecord(ctx, "admin-hostname", &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: pulumi.String(domain), Type: pulumi.String("CNAME"), Records: pulumi.StringArray{target}, Ttl: pulumi.Int(300)})
		if e != nil {
			return nil, e
		}
	}
	ctx.Export("runtimeTableName", table.Name)
	ctx.Export("runtimeTableArn", table.Arn)
	ctx.Export("adminPoolId", pool.ID())
	ctx.Export("adminClientId", client.ID())
	ctx.Export("adminLoginDomain", loginURL)
	ctx.Export("adminDomainTarget", target)
	ctx.Export("adminUrl", pulumi.String("https://"+domain))
	return table, nil
}
