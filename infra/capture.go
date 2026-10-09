package main

import (
	"fmt"
	"path/filepath"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cognito"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lambda"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/sqs"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func deployCapture(ctx *pulumi.Context, cfg *config.Config, api *apigatewayv2.Api, pool *cognito.UserPool, client *cognito.UserPoolClient, region pulumi.StringOutput) error {
	enabled := cfg.GetBool("captureEnabled")
	kind := cfg.Get("captureDocumentType")
	if kind == "" {
		kind = "sample_card"
	}
	if enabled && (kind == "sample_card" || cfg.Get("capturePurpose") == "" || cfg.Get("captureJurisdiction") == "") {
		return fmt.Errorf("capture requires document type, purpose and jurisdiction before enabling")
	}
	bucket, e := s3.NewBucketV2(ctx, "id-evidence", &s3.BucketV2Args{ForceDestroy: pulumi.Bool(false)})
	if e != nil {
		return e
	}
	versioning, e := s3.NewBucketVersioningV2(ctx, "id-evidence-versions", &s3.BucketVersioningV2Args{Bucket: bucket.ID(), VersioningConfiguration: &s3.BucketVersioningV2VersioningConfigurationArgs{Status: pulumi.String("Enabled")}})
	if e != nil {
		return e
	}
	_, e = s3.NewBucketLifecycleConfigurationV2(ctx, "id-evidence-orphans", &s3.BucketLifecycleConfigurationV2Args{Bucket: bucket.ID(), Rules: s3.BucketLifecycleConfigurationV2RuleArray{&s3.BucketLifecycleConfigurationV2RuleArgs{Id: pulumi.String("orphan-safety"), Status: pulumi.String("Enabled"), Filter: &s3.BucketLifecycleConfigurationV2RuleFilterArgs{Prefix: pulumi.String("")}, Expiration: &s3.BucketLifecycleConfigurationV2RuleExpirationArgs{Days: pulumi.Int(10)}, NoncurrentVersionExpiration: &s3.BucketLifecycleConfigurationV2RuleNoncurrentVersionExpirationArgs{NoncurrentDays: pulumi.Int(10)}, AbortIncompleteMultipartUpload: &s3.BucketLifecycleConfigurationV2RuleAbortIncompleteMultipartUploadArgs{DaysAfterInitiation: pulumi.Int(1)}}}}, pulumi.DependsOn([]pulumi.Resource{versioning}))
	if e != nil {
		return e
	}
	_, e = s3.NewBucketOwnershipControls(ctx, "id-evidence-owner", &s3.BucketOwnershipControlsArgs{Bucket: bucket.ID(), Rule: &s3.BucketOwnershipControlsRuleArgs{ObjectOwnership: pulumi.String("BucketOwnerEnforced")}})
	if e != nil {
		return e
	}
	_, e = s3.NewBucketPublicAccessBlock(ctx, "id-evidence-private", &s3.BucketPublicAccessBlockArgs{Bucket: bucket.ID(), BlockPublicAcls: pulumi.Bool(true), BlockPublicPolicy: pulumi.Bool(true), IgnorePublicAcls: pulumi.Bool(true), RestrictPublicBuckets: pulumi.Bool(true)})
	if e != nil {
		return e
	}
	_, e = s3.NewBucketServerSideEncryptionConfigurationV2(ctx, "id-evidence-encryption", &s3.BucketServerSideEncryptionConfigurationV2Args{Bucket: bucket.ID(), Rules: s3.BucketServerSideEncryptionConfigurationV2RuleArray{&s3.BucketServerSideEncryptionConfigurationV2RuleArgs{ApplyServerSideEncryptionByDefault: &s3.BucketServerSideEncryptionConfigurationV2RuleApplyServerSideEncryptionByDefaultArgs{SseAlgorithm: pulumi.String("AES256")}}}})
	if e != nil {
		return e
	}
	_, e = s3.NewBucketPolicy(ctx, "id-evidence-tls", &s3.BucketPolicyArgs{Bucket: bucket.ID(), Policy: pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":[%q,%q],"Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`, bucket.Arn, pulumi.Sprintf("%s/*", bucket.Arn))})
	if e != nil {
		return e
	}
	table, e := dynamodb.NewTable(ctx, "id-captures", &dynamodb.TableArgs{BillingMode: pulumi.String("PAY_PER_REQUEST"), HashKey: pulumi.String("id"), Attributes: dynamodb.TableAttributeArray{&dynamodb.TableAttributeArgs{Name: pulumi.String("id"), Type: pulumi.String("S")}, &dynamodb.TableAttributeArgs{Name: pulumi.String("work"), Type: pulumi.String("S")}, &dynamodb.TableAttributeArgs{Name: pulumi.String("due"), Type: pulumi.String("N")}}, GlobalSecondaryIndexes: dynamodb.TableGlobalSecondaryIndexArray{&dynamodb.TableGlobalSecondaryIndexArgs{Name: pulumi.String("work-due"), HashKey: pulumi.String("work"), RangeKey: pulumi.String("due"), ProjectionType: pulumi.String("ALL")}}, PointInTimeRecovery: &dynamodb.TablePointInTimeRecoveryArgs{Enabled: pulumi.Bool(true)}, ServerSideEncryption: &dynamodb.TableServerSideEncryptionArgs{Enabled: pulumi.Bool(true)}, Ttl: &dynamodb.TableTtlArgs{AttributeName: pulumi.String("ttl"), Enabled: pulumi.Bool(true)}})
	if e != nil {
		return e
	}
	dlq, e := sqs.NewQueue(ctx, "capture-validation-dlq", &sqs.QueueArgs{MessageRetentionSeconds: pulumi.Int(1209600), SqsManagedSseEnabled: pulumi.Bool(true)})
	if e != nil {
		return e
	}
	queue, e := sqs.NewQueue(ctx, "capture-validation", &sqs.QueueArgs{VisibilityTimeoutSeconds: pulumi.Int(720), SqsManagedSseEnabled: pulumi.Bool(true), RedrivePolicy: pulumi.Sprintf(`{"deadLetterTargetArn":%q,"maxReceiveCount":5}`, dlq.Arn)})
	if e != nil {
		return e
	}
	_, e = cloudwatch.NewMetricAlarm(ctx, "capture-dlq-alarm", &cloudwatch.MetricAlarmArgs{Namespace: pulumi.String("AWS/SQS"), MetricName: pulumi.String("ApproximateNumberOfMessagesVisible"), Dimensions: pulumi.StringMap{"QueueName": dlq.Name}, Statistic: pulumi.String("Maximum"), Period: pulumi.Int(60), EvaluationPeriods: pulumi.Int(1), Threshold: pulumi.Float64(0), ComparisonOperator: pulumi.String("GreaterThanThreshold"), TreatMissingData: pulumi.String("notBreaching")})
	if e != nil {
		return e
	}
	trust := pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)
	dbStatement := pulumi.Sprintf(`{"Effect":"Allow","Action":["dynamodb:GetItem","dynamodb:PutItem"],"Resource":%q}`, table.Arn)
	policies := map[string]pulumi.StringOutput{
		"capture":            pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[%s,{"Effect":"Allow","Action":["s3:PutObject","s3:GetObject"],"Resource":%q}]}`, dbStatement, pulumi.Sprintf("%s/uploads/*", bucket.Arn)),
		"capture-worker":     pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[%s,{"Effect":"Allow","Action":["s3:GetObjectVersion","s3:DeleteObjectVersion"],"Resource":%q},{"Effect":"Allow","Action":["s3:PutObject","s3:DeleteObjectVersion"],"Resource":%q},{"Effect":"Allow","Action":"s3:ListBucketVersions","Resource":%q,"Condition":{"StringLike":{"s3:prefix":["uploads/*","processed/*"]}}},{"Effect":"Allow","Action":["sqs:ReceiveMessage","sqs:DeleteMessage","sqs:GetQueueAttributes"],"Resource":%q}]}`, dbStatement, pulumi.Sprintf("%s/uploads/*", bucket.Arn), pulumi.Sprintf("%s/processed/*", bucket.Arn), bucket.Arn, queue.Arn),
		"capture-dispatcher": pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:Query","Resource":%q},{"Effect":"Allow","Action":"sqs:SendMessage","Resource":%q}]}`, pulumi.Sprintf("%s/index/work-due", table.Arn), queue.Arn),
	}
	functions := map[string]*lambda.Function{}
	for _, name := range []string{"capture", "capture-worker", "capture-dispatcher"} {
		role, e := iam.NewRole(ctx, name+"-role", &iam.RoleArgs{AssumeRolePolicy: trust})
		if e != nil {
			return e
		}
		_, e = iam.NewRolePolicyAttachment(ctx, name+"-logs", &iam.RolePolicyAttachmentArgs{Role: role.Name, PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole")})
		if e != nil {
			return e
		}
		grant, e := iam.NewRolePolicy(ctx, name+"-grant", &iam.RolePolicyArgs{Role: role.ID(), Policy: policies[name]})
		if e != nil {
			return e
		}
		archive, _ := filepath.Abs("../dist/" + name + ".zip")
		timeout, memory := 25, 256
		if name != "capture" {
			timeout, memory = 90, 1024
		}
		fn, e := lambda.NewFunction(ctx, name, &lambda.FunctionArgs{Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"), Architectures: pulumi.StringArray{pulumi.String("arm64")}, Role: role.Arn, Code: pulumi.NewFileArchive(archive), Timeout: pulumi.Int(timeout), MemorySize: pulumi.Int(memory), ReservedConcurrentExecutions: pulumi.Int(5), Environment: &lambda.FunctionEnvironmentArgs{Variables: pulumi.StringMap{"CAPTURE_TABLE": table.Name, "CAPTURE_BUCKET": bucket.ID(), "CAPTURE_QUEUE_URL": queue.Url, "CAPTURE_ENABLED": pulumi.String(fmt.Sprint(enabled)), "CAPTURE_DOCUMENT_TYPE": pulumi.String(kind), "CAPTURE_PURPOSE": pulumi.String(cfg.Get("capturePurpose")), "CAPTURE_JURISDICTION": pulumi.String(cfg.Get("captureJurisdiction"))}}}, pulumi.DependsOn([]pulumi.Resource{grant, versioning}))
		if e != nil {
			return e
		}
		functions[name] = fn
	}
	_, e = lambda.NewEventSourceMapping(ctx, "capture-queue-worker", &lambda.EventSourceMappingArgs{EventSourceArn: queue.Arn, FunctionName: functions["capture-worker"].Arn, BatchSize: pulumi.Int(1), FunctionResponseTypes: pulumi.StringArray{pulumi.String("ReportBatchItemFailures")}})
	if e != nil {
		return e
	}
	rule, e := cloudwatch.NewEventRule(ctx, "capture-sweep", &cloudwatch.EventRuleArgs{ScheduleExpression: pulumi.String("rate(1 minute)")})
	if e != nil {
		return e
	}
	_, e = cloudwatch.NewEventTarget(ctx, "capture-sweep-target", &cloudwatch.EventTargetArgs{Rule: rule.Name, Arn: functions["capture-dispatcher"].Arn})
	if e != nil {
		return e
	}
	_, e = lambda.NewPermission(ctx, "capture-sweep-invoke", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: functions["capture-dispatcher"].Name, Principal: pulumi.String("events.amazonaws.com"), SourceArn: rule.Arn})
	if e != nil {
		return e
	}
	authorizer, e := apigatewayv2.NewAuthorizer(ctx, "capture-jwt", &apigatewayv2.AuthorizerArgs{ApiId: api.ID(), AuthorizerType: pulumi.String("JWT"), IdentitySources: pulumi.StringArray{pulumi.String("$request.header.Authorization")}, JwtConfiguration: &apigatewayv2.AuthorizerJwtConfigurationArgs{Audiences: pulumi.StringArray{client.ID()}, Issuer: pulumi.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", region, pool.ID())}})
	if e != nil {
		return e
	}
	integration, e := apigatewayv2.NewIntegration(ctx, "capture-integration", &apigatewayv2.IntegrationArgs{ApiId: api.ID(), IntegrationType: pulumi.String("AWS_PROXY"), IntegrationUri: functions["capture"].InvokeArn, PayloadFormatVersion: pulumi.String("2.0")})
	if e != nil {
		return e
	}
	for i, route := range []string{"GET /onboarding/id/document-policy", "GET /onboarding/id/status", "POST /onboarding/id/captures", "GET /onboarding/id/captures/{capture_id}", "POST /onboarding/id/captures/{capture_id}/uploads", "POST /onboarding/id/captures/{capture_id}/finalize", "POST /onboarding/id/captures/{capture_id}/retry-finalization", "POST /onboarding/id/captures/{capture_id}/cancel"} {
		_, e = apigatewayv2.NewRoute(ctx, fmt.Sprintf("capture-route-%d", i), &apigatewayv2.RouteArgs{ApiId: api.ID(), RouteKey: pulumi.String(route), Target: pulumi.Sprintf("integrations/%s", integration.ID()), AuthorizationType: pulumi.String("JWT"), AuthorizerId: authorizer.ID()})
		if e != nil {
			return e
		}
	}
	_, e = lambda.NewPermission(ctx, "capture-api-invoke", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: functions["capture"].Name, Principal: pulumi.String("apigateway.amazonaws.com"), SourceArn: pulumi.Sprintf("%s/*/*/onboarding/id/*", api.ExecutionArn)})
	if e != nil {
		return e
	}
	ctx.Export("captureEnabled", pulumi.Bool(enabled))
	ctx.Export("captureBucket", bucket.ID())
	ctx.Export("captureDeadLetterQueue", dlq.Url)
	return nil
}
