package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/acm"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func apiCertificate(ctx *pulumi.Context, domain, zone string) (*acm.Certificate, error) {
	if domain == "" {
		return nil, nil
	}
	if len(domain) > 253 || !strings.Contains(domain, ".") {
		return nil, fmt.Errorf("apiDomain must be a DNS hostname")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) > 63 || !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`).MatchString(label) {
			return nil, fmt.Errorf("apiDomain must be a lowercase DNS hostname")
		}
	}
	cert, err := acm.NewCertificate(ctx, "api-certificate", &acm.CertificateArgs{DomainName: pulumi.String(domain), ValidationMethod: pulumi.String("DNS")})
	if err != nil {
		return nil, err
	}
	ctx.Export("apiCertificateArn", cert.Arn)
	if zone != "" {
		record := cert.DomainValidationOptions.Index(pulumi.Int(0))
		_, err = route53.NewRecord(ctx, "api-certificate-dns", &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: record.ResourceRecordName().Elem(), Type: record.ResourceRecordType().Elem(), Records: pulumi.StringArray{record.ResourceRecordValue().Elem()}, Ttl: pulumi.Int(300)})
	}
	return cert, err
}

func apiDomainMapping(ctx *pulumi.Context, domain, zone string, cert *acm.Certificate, api *apigatewayv2.Api, stage *apigatewayv2.Stage) error {
	if cert == nil {
		ctx.Export("apiUrl", api.ApiEndpoint)
		return nil
	}
	validation, err := acm.NewCertificateValidation(ctx, "api-certificate-validation", &acm.CertificateValidationArgs{CertificateArn: cert.Arn})
	if err != nil {
		return err
	}
	custom, err := apigatewayv2.NewDomainName(ctx, "api-domain", &apigatewayv2.DomainNameArgs{DomainName: pulumi.String(domain), DomainNameConfiguration: &apigatewayv2.DomainNameDomainNameConfigurationArgs{CertificateArn: validation.CertificateArn, EndpointType: pulumi.String("REGIONAL"), SecurityPolicy: pulumi.String("TLS_1_2")}})
	if err != nil {
		return err
	}
	_, err = apigatewayv2.NewApiMapping(ctx, "api-domain-mapping", &apigatewayv2.ApiMappingArgs{ApiId: api.ID(), DomainName: custom.ID(), Stage: stage.Name})
	if err != nil {
		return err
	}
	target := custom.DomainNameConfiguration.TargetDomainName().Elem()
	ctx.Export("apiDomain", pulumi.String(domain))
	ctx.Export("apiDomainTarget", target)
	ctx.Export("apiUrl", pulumi.String("https://"+domain))
	if zone != "" {
		_, err = route53.NewRecord(ctx, "api-domain-dns", &route53.RecordArgs{ZoneId: pulumi.String(zone), Name: pulumi.String(domain), Type: pulumi.String("CNAME"), Records: pulumi.StringArray{target}, Ttl: pulumi.Int(300)})
	}
	return err
}
