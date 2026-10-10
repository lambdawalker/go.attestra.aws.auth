package main

import (
	"net/url"
	"strings"
)

// Policies intentionally cover deployment control planes, not application data
// access. Pulumi-generated names have a random suffix; Cognito/API Gateway IDs
// cannot be known until creation, so their regional control planes are broader.
func deploymentPolicies(account string, o options) map[string]any {
	statement := func(actions, resources []string) map[string]any {
		return map[string]any{"Effect": "Allow", "Action": actions, "Resource": resources}
	}
	document := func(statements ...map[string]any) any {
		return map[string]any{"Version": "2012-10-17", "Statement": statements}
	}
	arn := func(service, resource string) string {
		return "arn:aws:" + service + ":" + o.Region + ":" + account + ":" + resource
	}
	regional := statement([]string{"apigateway:*", "cognito-idp:*", "lambda:ListEventSourceMappings", "lambda:CreateEventSourceMapping", "lambda:GetEventSourceMapping", "lambda:UpdateEventSourceMapping", "lambda:DeleteEventSourceMapping", "lambda:ListTags", "lambda:TagResource", "lambda:UntagResource"}, []string{"arn:aws:apigateway:" + o.Region + "::/*", arn("cognito-idp", "userpool/*"), arn("lambda", "event-source-mapping:*")})
	regional["Condition"] = map[string]any{"StringEquals": map[string]string{"aws:RequestedRegion": o.Region}}
	roles := []string{}
	for _, name := range []string{"signup", "resend", "confirm", "challenge", "passkeyoptions", "passkeycomplete", "auth-email-start", "auth-email-complete", "auth-passkey-start", "auth-passkey-complete", "auth-refresh", "auth-status", "capture", "capture-dispatcher", "capture-worker"} {
		roles = append(roles, "arn:aws:iam::"+account+":role/"+name+"-role-*")
	}
	iam := statement([]string{"iam:CreateRole", "iam:GetRole", "iam:UpdateRole", "iam:DeleteRole", "iam:UpdateAssumeRolePolicy", "iam:ListRolePolicies", "iam:GetRolePolicy", "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:ListAttachedRolePolicies", "iam:ListInstanceProfilesForRole", "iam:TagRole", "iam:UntagRole", "iam:ListRoleTags"}, roles)
	attach := statement([]string{"iam:AttachRolePolicy", "iam:DetachRolePolicy"}, roles)
	attach["Condition"] = map[string]any{"ArnEquals": map[string]string{"iam:PolicyARN": "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"}}
	pass := statement([]string{"iam:PassRole"}, roles)
	pass["Condition"] = map[string]any{"StringEquals": map[string]string{"iam:PassedToService": "lambda.amazonaws.com"}}
	functions := []string{}
	for _, prefix := range []string{"email-", "cognito-grant-challenge-", "passkeyoptions-", "passkeycomplete-", "auth-", "capture-"} {
		functions = append(functions, arn("lambda", "function:"+prefix+"*"))
	}
	u, _ := url.Parse(o.Backend)
	bucket := "arn:aws:s3:::" + u.Host
	prefix := strings.Trim(u.Path, "/")
	if prefix != "" {
		prefix += "/"
	}
	state := document(
		statement([]string{"s3:ListBucket", "s3:GetBucketVersioning", "s3:GetBucketPublicAccessBlock", "s3:GetBucketLocation"}, []string{bucket}),
		statement([]string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"}, []string{bucket + "/" + prefix + ".pulumi/*"}),
	)
	// Route53/SES cover infrastructure configured in infra/main.go. Regional SES
	// identity management does not grant mail sending. DNS changes are optional.
	ses := statement([]string{"ses:GetIdentity*", "ses:ListIdentities", "ses:VerifyDomainIdentity", "ses:VerifyDomainDkim", "ses:DeleteIdentity", "ses:SetIdentityDkimEnabled", "ses:GetAccount", "acm:RequestCertificate", "acm:DescribeCertificate", "acm:DeleteCertificate", "acm:AddTagsToCertificate", "acm:RemoveTagsFromCertificate", "acm:ListTagsForCertificate"}, []string{"*"})
	ses["Condition"] = map[string]any{"StringEquals": map[string]string{"aws:RequestedRegion": o.Region}}
	serviceRole := statement([]string{"iam:CreateServiceLinkedRole"}, []string{"arn:aws:iam::" + account + ":role/aws-service-role/ops.apigateway.amazonaws.com/AWSServiceRoleForAPIGateway"})
	serviceRole["Condition"] = map[string]any{"StringEquals": map[string]string{"iam:AWSServiceName": "ops.apigateway.amazonaws.com"}}
	return map[string]any{
		"attestra-state": state,
		"attestra-iam":   document(iam, attach, pass, serviceRole),
		"attestra-services": document(regional,
			statement([]string{"lambda:*"}, functions),
			statement([]string{"dynamodb:*"}, []string{arn("dynamodb", "table/email-proofs-*"), arn("dynamodb", "table/id-captures-*")}),
			statement([]string{"sqs:*"}, []string{arn("sqs", "capture-validation-*")}),
			statement([]string{"events:*"}, []string{arn("events", "rule/capture-sweep-*")}),
			statement([]string{"cloudwatch:PutMetricAlarm", "cloudwatch:DescribeAlarms", "cloudwatch:DeleteAlarms", "cloudwatch:ListTagsForResource", "cloudwatch:TagResource", "cloudwatch:UntagResource"}, []string{arn("cloudwatch", "alarm:capture-dlq-alarm-*")}),
			statement([]string{"s3:*"}, []string{"arn:aws:s3:::id-evidence-*", "arn:aws:s3:::id-evidence-*/*"}), ses,
			// Cognito creation and Lambda mapping creation do not support preexisting resource IDs.
			func() map[string]any {
				s := statement([]string{"cognito-idp:CreateUserPool", "lambda:CreateEventSourceMapping", "lambda:ListEventSourceMappings"}, []string{"*"})
				s["Condition"] = map[string]any{"StringEquals": map[string]string{"aws:RequestedRegion": o.Region}}
				return s
			}(),
		),
	}
}
