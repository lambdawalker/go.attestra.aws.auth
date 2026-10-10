package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	awsenv "github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/aws"

	"github.com/lambdawalker/go.attestra.aws.auth/tools/deploy/internal/dns"
)

// Only identifiers required for read-back are persisted, never resource inputs,
// Lambda environment variables, secrets, or full Pulumi state.
type teardownProbe struct {
	Name, Kind, ID, Zone, RecordType string
	Retained                         bool
}

func teardownResourceProbe(kind, urn, id string, out map[string]json.RawMessage) teardownProbe {
	p := teardownProbe{Name: urn, Kind: "unsupported", ID: id}
	value := func(key string) string { var s string; _ = json.Unmarshal(out[key], &s); return s }
	parent := func(k, key string) { p.Kind = k; p.ID = value(key) }
	switch kind {
	case "aws:lambda/function:Function":
		p.Kind = "lambda"
	case "aws:lambda/eventSourceMapping:EventSourceMapping":
		p.Kind = "mapping"
	case "aws:lambda/permission:Permission":
		parent("lambda", "function")
	case "aws:iam/role:Role":
		p.Kind = "role"
	case "aws:iam/rolePolicy:RolePolicy", "aws:iam/rolePolicyAttachment:RolePolicyAttachment":
		parent("role", "role")
	case "aws:dynamodb/table:Table":
		p.Kind = "table"
	case "aws:s3/bucketV2:BucketV2":
		p.Kind = "bucket"
	case "aws:s3/bucketVersioningV2:BucketVersioningV2", "aws:s3/bucketLifecycleConfigurationV2:BucketLifecycleConfigurationV2", "aws:s3/bucketOwnershipControls:BucketOwnershipControls", "aws:s3/bucketPublicAccessBlock:BucketPublicAccessBlock", "aws:s3/bucketServerSideEncryptionConfigurationV2:BucketServerSideEncryptionConfigurationV2", "aws:s3/bucketPolicy:BucketPolicy":
		parent("bucket", "bucket")
	case "aws:sqs/queue:Queue":
		p.Kind = "queue"
	case "aws:cognito/userPool:UserPool":
		p.Kind = "pool"
	case "aws:cognito/userPoolClient:UserPoolClient":
		parent("pool", "userPoolId")
	case "aws:ses/domainIdentity:DomainIdentity":
		p.Kind = "ses"
	case "aws:ses/domainDkim:DomainDkim":
		parent("ses", "domain")
	case "aws:apigatewayv2/api:Api":
		p.Kind = "api"
	case "aws:apigatewayv2/integration:Integration", "aws:apigatewayv2/route:Route", "aws:apigatewayv2/stage:Stage", "aws:apigatewayv2/authorizer:Authorizer":
		parent("api", "apiId")
	case "aws:apigatewayv2/domainName:DomainName":
		p.Kind = "domain"
	case "aws:apigatewayv2/apiMapping:ApiMapping":
		parent("domain", "domainName")
	case "aws:acm/certificate:Certificate":
		p.Kind = "certificate"
	case "aws:acm/certificateValidation:CertificateValidation":
		parent("certificate", "certificateArn")
	case "aws:cloudwatch/eventRule:EventRule":
		p.Kind = "rule"
		if n := value("name"); n != "" {
			p.ID = n
		}
	case "aws:cloudwatch/eventTarget:EventTarget":
		parent("rule", "rule")
	case "aws:cloudwatch/metricAlarm:MetricAlarm":
		p.Kind = "alarm"
	case "aws:route53/record:Record":
		p.Kind = "record"
		p.ID = value("name")
		p.Zone = value("zoneId")
		p.RecordType = value("type")
	}
	return p
}

var errTeardownResourceRemains = errors.New("resource still exists")

func verifyTeardownProbe(a awsenv.API, p teardownProbe) error {
	if p.ID == "" {
		return errors.New("resource identifier missing; cannot verify deletion")
	}
	var args []string
	missing := "ResourceNotFoundException"
	field := ""
	switch p.Kind {
	case "lambda":
		args = []string{"lambda", "get-function-configuration", "--function-name", p.ID}
	case "mapping":
		args = []string{"lambda", "get-event-source-mapping", "--uuid", p.ID}
	case "role":
		args = []string{"iam", "get-role", "--role-name", p.ID}
		missing = "NoSuchEntity"
	case "table":
		args = []string{"dynamodb", "describe-table", "--table-name", p.ID}
	case "bucket":
		args = []string{"s3api", "get-bucket-location", "--bucket", p.ID}
		missing = "NoSuchBucket"
	case "queue":
		args = []string{"sqs", "get-queue-attributes", "--queue-url", p.ID, "--attribute-names", "QueueArn"}
		missing = "AWS.SimpleQueueService.NonExistentQueue"
	case "pool":
		args = []string{"cognito-idp", "describe-user-pool", "--user-pool-id", p.ID}
	case "ses":
		args = []string{"ses", "get-identity-verification-attributes", "--identities", p.ID}
		field = "VerificationAttributes"
	case "api":
		args = []string{"apigatewayv2", "get-api", "--api-id", p.ID}
		missing = "NotFoundException"
	case "domain":
		args = []string{"apigatewayv2", "get-domain-name", "--domain-name", p.ID}
		missing = "NotFoundException"
	case "certificate":
		args = []string{"acm", "describe-certificate", "--certificate-arn", p.ID}
	case "rule":
		args = []string{"events", "describe-rule", "--name", p.ID}
	case "alarm":
		args = []string{"cloudwatch", "describe-alarms", "--alarm-names", p.ID, "--alarm-types", "MetricAlarm", "CompositeAlarm"}
		field = "MetricAlarms"
	case "record":
		if p.Zone == "" || p.RecordType == "" {
			return errors.New("DNS identifier missing")
		}
		recordInput, _ := json.Marshal(map[string]string{"HostedZoneId": p.Zone, "StartRecordName": p.ID, "StartRecordType": p.RecordType, "MaxItems": "1"})
		args = []string{"route53", "list-resource-record-sets", "--cli-input-json", string(recordInput), "--no-paginate"}
		field = "ResourceRecordSets"
		missing = "NoSuchHostedZone"
	default:
		return errors.New("unsupported resource type; deletion requires manual verification")
	}
	var body map[string]json.RawMessage
	found, err := a.Call(&body, args...)
	if err != nil {
		var awsErr *awsenv.Error
		if errors.As(err, &awsErr) && (awsErr.Code == missing || (p.Kind == "queue" && awsErr.Code == "QueueDoesNotExist")) {
			return nil
		}
		return err
	}
	if !found {
		if p.Kind == "role" {
			return nil
		}
		return errors.New("AWS did not return an authoritative result")
	}
	if field == "" {
		return errTeardownResourceRemains
	}
	raw, ok := body[field]
	if !ok || string(raw) == "null" {
		return errors.New("incomplete AWS verification response")
	}
	if p.Kind == "ses" {
		var identities map[string]json.RawMessage
		if json.Unmarshal(raw, &identities) != nil {
			return errors.New("invalid SES verification response")
		}
		if len(identities) == 0 {
			return nil
		}
		return errTeardownResourceRemains
	}
	if p.Kind == "record" {
		var records []struct{ Name, Type string }
		if json.Unmarshal(raw, &records) != nil {
			return errors.New("invalid Route53 response")
		}
		for _, r := range records {
			if r.Name == "" || r.Type == "" {
				return errors.New("incomplete Route53 record")
			}
			if dns.Name(r.Name) == dns.Name(p.ID) && r.Type == p.RecordType {
				return errTeardownResourceRemains
			}
		}
		return nil
	}
	var alarms []json.RawMessage
	if json.Unmarshal(raw, &alarms) != nil {
		return errors.New("invalid alarm response")
	}
	if len(alarms) > 0 {
		return errTeardownResourceRemains
	}
	raw, ok = body["CompositeAlarms"]
	if !ok || string(raw) == "null" || json.Unmarshal(raw, &alarms) != nil {
		return errors.New("incomplete alarm response")
	}
	if len(alarms) > 0 {
		return errTeardownResourceRemains
	}
	return nil
}

func captureTeardownInventory(data []byte, p *teardownProgress) error {
	var state struct {
		Deployment *struct {
			Resources []struct {
				Type, URN, ID            string
				External, RetainOnDelete bool
				Outputs                  map[string]json.RawMessage
			}
		}
	}
	if json.Unmarshal(data, &state) != nil || state.Deployment == nil || state.Deployment.Resources == nil {
		return errors.New("invalid inventory export")
	}
	// Merge on resume: preserve evidence of resources already destroyed by a
	// partial attempt, while capturing any resources still in the stack.
	known := map[string]bool{}
	for _, probe := range p.Inventory {
		known[fmt.Sprintf("%s\x00%s", probe.Name, probe.ID)] = true
	}
	for _, r := range state.Deployment.Resources {
		if strings.HasPrefix(r.Type, "pulumi:") {
			continue
		}
		probe := teardownResourceProbe(r.Type, r.URN, r.ID, r.Outputs)
		probe.Retained = r.External || r.RetainOnDelete
		key := fmt.Sprintf("%s\x00%s", probe.Name, probe.ID)
		if !known[key] {
			p.Inventory = append(p.Inventory, probe)
			known[key] = true
		}
	}
	return nil
}
