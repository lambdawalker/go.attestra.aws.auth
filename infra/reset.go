package main

import (
	"encoding/json"
	"fmt"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"path/filepath"
)

type resetInventory struct {
	CaptureTable    pulumi.StringInput
	CaptureTableARN pulumi.StringInput
	Control         *dynamodb.Table
	Functions       pulumi.StringArray
	FunctionARNs    pulumi.StringArray
	Tables          pulumi.StringArray
	TableARNs       pulumi.StringArray
	Pool            pulumi.StringInput
	PoolARN         pulumi.StringInput
	Bucket          pulumi.StringInput
	BucketARN       pulumi.StringInput
	Queues          pulumi.StringArray
	QueueARNs       pulumi.StringArray
}

func (i *resetInventory) addFunction(fn *lambda.Function) {
	i.Functions = append(i.Functions, fn.Name)
	i.FunctionARNs = append(i.FunctionARNs, fn.Arn)
}
func resetControl(ctx *pulumi.Context) (*dynamodb.Table, error) {
	return dynamodb.NewTable(ctx, "environment-reset-control", &dynamodb.TableArgs{BillingMode: pulumi.String("PAY_PER_REQUEST"), HashKey: pulumi.String("id"), Attributes: dynamodb.TableAttributeArray{&dynamodb.TableAttributeArgs{Name: pulumi.String("id"), Type: pulumi.String("S")}}, ServerSideEncryption: &dynamodb.TableServerSideEncryptionArgs{Enabled: pulumi.Bool(true)}, PointInTimeRecovery: &dynamodb.TablePointInTimeRecoveryArgs{Enabled: pulumi.Bool(true)}})
}
func deployReset(ctx *pulumi.Context, cfg *config.Config, i *resetInventory) error {
	enabled := ctx.Stack() != "prod" && ctx.Stack() != "production" && cfg.Get("environmentClass") != "production"
	if cfg.Get("environmentResetEnabled") != "" {
		enabled = cfg.GetBool("environmentResetEnabled")
	}
	role, e := iam.NewRole(ctx, "environment-reset-role", &iam.RoleArgs{AssumeRolePolicy: pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)})
	if e != nil {
		return e
	}
	_, e = iam.NewRolePolicyAttachment(ctx, "environment-reset-logs", &iam.RolePolicyAttachmentArgs{Role: role.Name, PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole")})
	if e != nil {
		return e
	}
	policy := pulumi.All(i.FunctionARNs, i.TableARNs, i.PoolARN, i.BucketARN, i.QueueARNs, i.Control.Arn).ApplyT(func(v []any) (string, error) {
		statement := func(actions any, resources any) map[string]any {
			return map[string]any{"Effect": "Allow", "Action": actions, "Resource": resources}
		}
		out := map[string]any{"Version": "2012-10-17", "Statement": []any{
			statement([]string{"lambda:GetFunctionConcurrency", "lambda:PutFunctionConcurrency", "lambda:DeleteFunctionConcurrency"}, v[0]),
			statement([]string{"dynamodb:Scan", "dynamodb:BatchWriteItem"}, v[1]),
			statement([]string{"cognito-idp:DescribeUserPool", "cognito-idp:ListUsers", "cognito-idp:AdminDeleteUser"}, v[2]),
			statement("s3:ListBucketVersions", v[3]), statement("s3:DeleteObjectVersion", fmt.Sprint(v[3])+"/*"),
			statement("sqs:PurgeQueue", v[4]), statement([]string{"dynamodb:GetItem", "dynamodb:PutItem"}, v[5]),
		}}
		b, e := json.Marshal(out)
		return string(b), e
	}).(pulumi.StringOutput)
	grant, e := iam.NewRolePolicy(ctx, "environment-reset-data", &iam.RolePolicyArgs{Role: role.ID(), Policy: policy})
	if e != nil {
		return e
	}
	manifest := pulumi.All(i.Functions, i.Tables, i.Pool, i.Bucket, i.Queues).ApplyT(func(v []any) (string, error) {
		b, e := json.Marshal(map[string]any{"Functions": v[0], "Tables": v[1], "Pool": v[2], "Bucket": v[3], "Queues": v[4]})
		return string(b), e
	}).(pulumi.StringOutput)
	archive, _ := filepath.Abs("../dist/environment-reset.zip")
	fn, e := lambda.NewFunction(ctx, "environment-reset", &lambda.FunctionArgs{Name: pulumi.String("attestra-reset-" + ctx.Stack()), Role: role.Arn, Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"), Architectures: pulumi.StringArray{pulumi.String("arm64")}, Timeout: pulumi.Int(60), MemorySize: pulumi.Int(256), Code: pulumi.NewFileArchive(archive), Environment: &lambda.FunctionEnvironmentArgs{Variables: pulumi.StringMap{"RESET_MANIFEST": manifest, "RESET_TABLE": i.Control.Name, "ENVIRONMENT": pulumi.String(ctx.Stack()), "RESET_ENABLED": pulumi.String(fmt.Sprint(enabled))}}}, pulumi.DependsOn([]pulumi.Resource{grant}))
	if e != nil {
		return e
	}
	rule, e := cloudwatch.NewEventRule(ctx, "environment-reset-sweep", &cloudwatch.EventRuleArgs{ScheduleExpression: pulumi.String("rate(1 minute)")})
	if e != nil {
		return e
	}
	_, e = cloudwatch.NewEventTarget(ctx, "environment-reset-target", &cloudwatch.EventTargetArgs{Rule: rule.Name, Arn: fn.Arn, Input: pulumi.String(`{"action":"work"}`)})
	if e != nil {
		return e
	}
	_, e = lambda.NewPermission(ctx, "environment-reset-schedule", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: fn.Name, Principal: pulumi.String("events.amazonaws.com"), SourceArn: rule.Arn})
	ctx.Export("environmentResetEnabled", pulumi.Bool(enabled))
	return e
}
