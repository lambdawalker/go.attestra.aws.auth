package adminsetup

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"testing"
)

type provider struct {
	set         []*cognitoidentityprovider.SetUserPoolMfaConfigInput
	update      *cognitoidentityprovider.UpdateUserPoolInput
	unsupported bool
	weaken      bool
}

func (p *provider) DescribeUserPool(context.Context, *cognitoidentityprovider.DescribeUserPoolInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolOutput, error) {
	return &cognitoidentityprovider.DescribeUserPoolOutput{UserPool: &types.UserPoolType{Id: aws.String("pool"), UserPoolTier: types.UserPoolTierTypeEssentials, AdminCreateUserConfig: &types.AdminCreateUserConfigType{AllowAdminCreateUserOnly: true}, Policies: &types.UserPoolPolicyType{PasswordPolicy: &types.PasswordPolicyType{MinimumLength: aws.Int32(14)}}, AccountRecoverySetting: &types.AccountRecoverySettingType{RecoveryMechanisms: []types.RecoveryOptionType{{Name: types.RecoveryOptionNameTypeAdminOnly, Priority: aws.Int32(1)}}}, UserPoolTags: map[string]string{"preserve": "yes"}}}, nil
}
func (p *provider) UpdateUserPool(_ context.Context, in *cognitoidentityprovider.UpdateUserPoolInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.UpdateUserPoolOutput, error) {
	p.update = in
	return &cognitoidentityprovider.UpdateUserPoolOutput{}, nil
}
func (p *provider) SetUserPoolMfaConfig(_ context.Context, in *cognitoidentityprovider.SetUserPoolMfaConfigInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.SetUserPoolMfaConfigOutput, error) {
	p.set = append(p.set, in)
	if p.unsupported {
		return nil, errors.New("unsupported factor")
	}
	return &cognitoidentityprovider.SetUserPoolMfaConfigOutput{}, nil
}
func (p *provider) GetUserPoolMfaConfig(context.Context, *cognitoidentityprovider.GetUserPoolMfaConfigInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.GetUserPoolMfaConfigOutput, error) {
	last := p.set[len(p.set)-1]
	w := *last.WebAuthnConfiguration
	if p.weaken {
		w.FactorConfiguration = types.WebAuthnFactorConfigurationTypeSingleFactor
	}
	return &cognitoidentityprovider.GetUserPoolMfaConfigOutput{MfaConfiguration: last.MfaConfiguration, SoftwareTokenMfaConfiguration: last.SoftwareTokenMfaConfiguration, WebAuthnConfiguration: &w}, nil
}
func TestRequiredMFAPasskeyConfigurationAndPreservedPool(t *testing.T) {
	p := &provider{}
	if e := Configure(context.Background(), p, "pool", "https://admin.auth.us-east-2.amazoncognito.com"); e != nil {
		t.Fatal(e)
	}
	if len(p.set) != 2 || p.set[0].MfaConfiguration != types.UserPoolMfaTypeOn || p.set[0].WebAuthnConfiguration.FactorConfiguration != types.WebAuthnFactorConfigurationTypeMultiFactorWithUserVerification || p.set[0].WebAuthnConfiguration.UserVerification != types.UserVerificationTypeRequired {
		t.Fatal("passkey did not satisfy required MFA")
	}
	if aws.ToInt32(p.update.Policies.PasswordPolicy.MinimumLength) != 14 || p.update.UserPoolTags["preserve"] != "yes" || p.update.AccountRecoverySetting.RecoveryMechanisms[0].Name != types.RecoveryOptionNameTypeAdminOnly {
		t.Fatal("UpdateUserPool reset existing configuration")
	}
	if len(p.update.Policies.SignInPolicy.AllowedFirstAuthFactors) != 2 {
		t.Fatal("missing password or passkey")
	}
}
func TestUnsupportedOrWeakenedMFAFailsSetup(t *testing.T) {
	for _, p := range []*provider{{unsupported: true}, {weaken: true}} {
		if e := Configure(context.Background(), p, "pool", "https://admin.auth.us-east-2.amazoncognito.com"); e == nil {
			t.Fatal("setup succeeded with unsupported or weakened MFA")
		}
	}
}
