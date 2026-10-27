package main

import (
	"fmt"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"path/filepath"
)

func deployRuntime(ctx *pulumi.Context, cfg *config.Config, api *apigatewayv2.Api) error {
	role, e := iam.NewRole(ctx, "app-config-role", &iam.RoleArgs{AssumeRolePolicy: pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)})
	if e != nil {
		return e
	}
	_, e = iam.NewRolePolicyAttachment(ctx, "app-config-logs", &iam.RolePolicyAttachmentArgs{Role: role.Name, PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole")})
	if e != nil {
		return e
	}
	grant, e := iam.NewRolePolicy(ctx, "app-config-data", &iam.RolePolicyArgs{Role: role.Name, Policy: pulumi.String(fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:GetItem","Resource":%q,"Condition":{"ForAllValues:StringEquals":{"dynamodb:LeadingKeys":[%q]}}}]}`, cfg.Require("runtimeTableArn"), "ENV#"+ctx.Stack()))})
	if e != nil {
		return e
	}
	archive, _ := filepath.Abs("../dist/app-config.zip")
	fn, e := lambda.NewFunction(ctx, "app-config", &lambda.FunctionArgs{Role: role.Arn, Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"), Architectures: pulumi.StringArray{pulumi.String("arm64")}, Code: pulumi.NewFileArchive(archive), Timeout: pulumi.Int(10), MemorySize: pulumi.Int(128), Environment: &lambda.FunctionEnvironmentArgs{Variables: pulumi.StringMap{"RUNTIME_TABLE": pulumi.String(cfg.Require("runtimeTableName")), "ENVIRONMENT": pulumi.String(ctx.Stack())}}}, pulumi.DependsOn([]pulumi.Resource{grant}))
	if e != nil {
		return e
	}
	integration, e := apigatewayv2.NewIntegration(ctx, "app-config-integration", &apigatewayv2.IntegrationArgs{ApiId: api.ID(), IntegrationType: pulumi.String("AWS_PROXY"), IntegrationUri: fn.InvokeArn, PayloadFormatVersion: pulumi.String("2.0")})
	if e != nil {
		return e
	}
	_, e = apigatewayv2.NewRoute(ctx, "app-config-route", &apigatewayv2.RouteArgs{ApiId: api.ID(), RouteKey: pulumi.String("GET /v1/app-config"), Target: pulumi.Sprintf("integrations/%s", integration.ID())})
	if e != nil {
		return e
	}
	_, e = lambda.NewPermission(ctx, "app-config-invoke", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: fn.Name, Principal: pulumi.String("apigateway.amazonaws.com"), SourceArn: pulumi.Sprintf("%s/*/GET/v1/app-config", api.ExecutionArn)})
	return e
}
