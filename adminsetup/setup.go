// Package adminsetup configures the Cognito feature that older IaC providers cannot express.
package adminsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"net/url"
)

type Cognito interface {
	DescribeUserPool(context.Context, *cognitoidentityprovider.DescribeUserPoolInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolOutput, error)
	UpdateUserPool(context.Context, *cognitoidentityprovider.UpdateUserPoolInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.UpdateUserPoolOutput, error)
	SetUserPoolMfaConfig(context.Context, *cognitoidentityprovider.SetUserPoolMfaConfigInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.SetUserPoolMfaConfigOutput, error)
	GetUserPoolMfaConfig(context.Context, *cognitoidentityprovider.GetUserPoolMfaConfigInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.GetUserPoolMfaConfigOutput, error)
}

func Configure(ctx context.Context, client Cognito, pool, login string) error {
	u, e := url.Parse(login)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" {
		return errors.New("invalid managed login domain")
	}
	described, e := client.DescribeUserPool(ctx, &cognitoidentityprovider.DescribeUserPoolInput{UserPoolId: aws.String(pool)})
	if e != nil {
		return e
	}
	if described.UserPool == nil || described.UserPool.AdminCreateUserConfig == nil || !described.UserPool.AdminCreateUserConfig.AllowAdminCreateUserOnly {
		return errors.New("refusing to configure a pool that permits self-signup")
	}
	desired := &cognitoidentityprovider.SetUserPoolMfaConfigInput{UserPoolId: aws.String(pool), MfaConfiguration: types.UserPoolMfaTypeOn, SoftwareTokenMfaConfiguration: &types.SoftwareTokenMfaConfigType{Enabled: true}, WebAuthnConfiguration: &types.WebAuthnConfigurationType{RelyingPartyId: aws.String(u.Hostname()), UserVerification: types.UserVerificationTypeRequired, FactorConfiguration: types.WebAuthnFactorConfigurationTypeMultiFactorWithUserVerification}}
	if _, e = client.SetUserPoolMfaConfig(ctx, desired); e != nil {
		return fmt.Errorf("admin pool requires Cognito MFA passkey factor MULTI_FACTOR_WITH_USER_VERIFICATION; region or account may not support it: %w", e)
	}
	// Copy every matching updateable property from DescribeUserPool: UpdateUserPool resets omitted settings.
	data, e := json.Marshal(described.UserPool)
	if e != nil {
		return e
	}
	var update cognitoidentityprovider.UpdateUserPoolInput
	if e = json.Unmarshal(data, &update); e != nil {
		return e
	}
	update.UserPoolId = aws.String(pool)
	update.MfaConfiguration = types.UserPoolMfaTypeOn
	if update.Policies == nil {
		update.Policies = &types.UserPoolPolicyType{}
	}
	update.Policies.SignInPolicy = &types.SignInPolicyType{AllowedFirstAuthFactors: []types.AuthFactorType{types.AuthFactorTypePassword, types.AuthFactorTypeWebAuthn}}
	if _, e = client.UpdateUserPool(ctx, &update); e != nil {
		return fmt.Errorf("enable admin password and passkey choices: %w", e)
	}
	if _, e = client.SetUserPoolMfaConfig(ctx, desired); e != nil {
		return e
	}
	actual, e := client.GetUserPoolMfaConfig(ctx, &cognitoidentityprovider.GetUserPoolMfaConfigInput{UserPoolId: aws.String(pool)})
	if e != nil {
		return e
	}
	if actual.MfaConfiguration != types.UserPoolMfaTypeOn || actual.SoftwareTokenMfaConfiguration == nil || !actual.SoftwareTokenMfaConfiguration.Enabled || actual.WebAuthnConfiguration == nil || actual.WebAuthnConfiguration.FactorConfiguration != types.WebAuthnFactorConfigurationTypeMultiFactorWithUserVerification || actual.WebAuthnConfiguration.UserVerification != types.UserVerificationTypeRequired || aws.ToString(actual.WebAuthnConfiguration.RelyingPartyId) != u.Hostname() {
		return errors.New("admin MFA/passkey configuration verification failed; setup incomplete")
	}
	return nil
}
