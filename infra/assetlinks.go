package main

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/lambdawalker/go.attestra.aws.auth/assetlinks"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/acm"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudfront"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func deployAssetLinks(ctx *pulumi.Context, origin, zone string, apps []assetlinks.Application) error {
	content, err := assetlinks.Render(apps)
	if err != nil {
		return err
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("asset links require an HTTPS appOrigin hostname")
	}
	domain := u.Hostname()
	// CloudFront viewer certificates must be in us-east-1 regardless of stack region.
	east, err := aws.NewProvider(ctx, "app-edge", &aws.ProviderArgs{Region: pulumi.String("us-east-1")})
	if err != nil {
		return err
	}
	cert, err := acm.NewCertificate(ctx, "app-certificate", &acm.CertificateArgs{DomainName: pulumi.String(domain), ValidationMethod: pulumi.String("DNS")}, pulumi.Provider(east))
	if err != nil {
		return err
	}
	ctx.Export("appCertificateArn", cert.Arn)
	validationNames := pulumi.StringArray{}
	if zone != "" {
		option := cert.DomainValidationOptions.Index(pulumi.Int(0))
		record, e := route53.NewRecord(ctx, "app-certificate-dns", &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: option.ResourceRecordName().Elem(), Type: option.ResourceRecordType().Elem(), Records: pulumi.StringArray{option.ResourceRecordValue().Elem()}, Ttl: pulumi.Int(300)})
		if e != nil {
			return e
		}
		validationNames = append(validationNames, record.Fqdn)
	}
	valid, err := acm.NewCertificateValidation(ctx, "app-certificate-validation", &acm.CertificateValidationArgs{CertificateArn: cert.Arn, ValidationRecordFqdns: validationNames}, pulumi.Provider(east))
	if err != nil {
		return err
	}
	bucket, err := s3.NewBucketV2(ctx, "app-associations", &s3.BucketV2Args{ForceDestroy: pulumi.Bool(false)})
	if err != nil {
		return err
	}
	private, err := s3.NewBucketPublicAccessBlock(ctx, "app-associations-private", &s3.BucketPublicAccessBlockArgs{Bucket: bucket.ID(), BlockPublicAcls: pulumi.Bool(true), BlockPublicPolicy: pulumi.Bool(true), IgnorePublicAcls: pulumi.Bool(true), RestrictPublicBuckets: pulumi.Bool(true)})
	if err != nil {
		return err
	}
	_, err = s3.NewBucketOwnershipControls(ctx, "app-associations-owner", &s3.BucketOwnershipControlsArgs{Bucket: bucket.ID(), Rule: &s3.BucketOwnershipControlsRuleArgs{ObjectOwnership: pulumi.String("BucketOwnerEnforced")}})
	if err != nil {
		return err
	}
	_, err = s3.NewBucketServerSideEncryptionConfigurationV2(ctx, "app-associations-encryption", &s3.BucketServerSideEncryptionConfigurationV2Args{Bucket: bucket.ID(), Rules: s3.BucketServerSideEncryptionConfigurationV2RuleArray{&s3.BucketServerSideEncryptionConfigurationV2RuleArgs{ApplyServerSideEncryptionByDefault: &s3.BucketServerSideEncryptionConfigurationV2RuleApplyServerSideEncryptionByDefaultArgs{SseAlgorithm: pulumi.String("AES256")}}}})
	if err != nil {
		return err
	}
	_, err = s3.NewBucketObjectv2(ctx, "android-assetlinks", &s3.BucketObjectv2Args{Bucket: bucket.ID(), Key: pulumi.String(".well-known/assetlinks.json"), Content: pulumi.String(content), ContentType: pulumi.String("application/json"), CacheControl: pulumi.String("no-store"), ServerSideEncryption: pulumi.String("AES256")})
	if err != nil {
		return err
	}
	access, err := cloudfront.NewOriginAccessControl(ctx, "app-associations-access", &cloudfront.OriginAccessControlArgs{Description: pulumi.String("Attestra public Android associations"), OriginAccessControlOriginType: pulumi.String("s3"), SigningBehavior: pulumi.String("always"), SigningProtocol: pulumi.String("sigv4")})
	if err != nil {
		return err
	}
	distribution, err := cloudfront.NewDistribution(ctx, "app-associations-cdn", &cloudfront.DistributionArgs{
		Enabled: pulumi.Bool(true), WaitForDeployment: pulumi.Bool(true), Aliases: pulumi.StringArray{pulumi.String(domain)}, PriceClass: pulumi.String("PriceClass_100"),
		Origins:              cloudfront.DistributionOriginArray{&cloudfront.DistributionOriginArgs{DomainName: bucket.BucketRegionalDomainName, OriginId: pulumi.String("associations"), OriginAccessControlId: access.ID(), S3OriginConfig: &cloudfront.DistributionOriginS3OriginConfigArgs{OriginAccessIdentity: pulumi.String("")}}},
		DefaultCacheBehavior: &cloudfront.DistributionDefaultCacheBehaviorArgs{TargetOriginId: pulumi.String("associations"), ViewerProtocolPolicy: pulumi.String("https-only"), AllowedMethods: pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")}, CachedMethods: pulumi.StringArray{pulumi.String("GET"), pulumi.String("HEAD")}, CachePolicyId: pulumi.String("4135ea2d-6df8-44a3-9df3-4b5a84be39ad")},
		Restrictions:         &cloudfront.DistributionRestrictionsArgs{GeoRestriction: &cloudfront.DistributionRestrictionsGeoRestrictionArgs{RestrictionType: pulumi.String("none")}},
		ViewerCertificate:    &cloudfront.DistributionViewerCertificateArgs{AcmCertificateArn: valid.CertificateArn, SslSupportMethod: pulumi.String("sni-only"), MinimumProtocolVersion: pulumi.String("TLSv1.2_2021")},
	})
	if err != nil {
		return err
	}
	policy := pulumi.All(bucket.Arn, distribution.Arn).ApplyT(func(v []interface{}) (string, error) {
		b, e := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []any{
			map[string]any{"Effect": "Allow", "Principal": map[string]string{"Service": "cloudfront.amazonaws.com"}, "Action": "s3:GetObject", "Resource": v[0].(string) + "/.well-known/assetlinks.json", "Condition": map[string]any{"StringEquals": map[string]any{"AWS:SourceArn": v[1]}}},
			map[string]any{"Effect": "Deny", "Principal": "*", "Action": "s3:*", "Resource": []string{v[0].(string), v[0].(string) + "/*"}, "Condition": map[string]any{"Bool": map[string]string{"aws:SecureTransport": "false"}}},
		}})
		return string(b), e
	}).(pulumi.StringOutput)
	_, err = s3.NewBucketPolicy(ctx, "app-associations-policy", &s3.BucketPolicyArgs{Bucket: bucket.ID(), Policy: policy}, pulumi.DependsOn([]pulumi.Resource{private}))
	if err != nil {
		return err
	}
	if zone != "" {
		_, err = route53.NewRecord(ctx, "app-associations-dns", &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: pulumi.String(domain), Type: pulumi.String("A"), Aliases: route53.RecordAliasArray{&route53.RecordAliasArgs{Name: distribution.DomainName, ZoneId: distribution.HostedZoneId, EvaluateTargetHealth: pulumi.Bool(false)}}})
		if err != nil {
			return err
		}
	}
	ctx.Export("assetLinksUrl", pulumi.String("https://"+domain+"/.well-known/assetlinks.json"))
	ctx.Export("assetLinksContent", pulumi.String(content))
	ctx.Export("appDomainTarget", distribution.DomainName)
	ctx.Export("appDistributionId", distribution.ID())
	return nil
}
