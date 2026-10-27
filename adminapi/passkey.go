package adminapi

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"net/url"
	"slices"
)

var ErrPasskeyNotReady = errors.New("complete_totp_and_passkey_registration_first")

type FactorProvider interface {
	GetUser(context.Context, *cognitoidentityprovider.GetUserInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.GetUserOutput, error)
	ListWebAuthnCredentials(context.Context, *cognitoidentityprovider.ListWebAuthnCredentialsInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListWebAuthnCredentialsOutput, error)
	SetUserMFAPreference(context.Context, *cognitoidentityprovider.SetUserMFAPreferenceInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.SetUserMFAPreferenceOutput, error)
}
type PasskeyMFA struct {
	Provider    FactorProvider
	LoginDomain string
}

func (s PasskeyMFA) Activate(ctx context.Context, token string) error {
	user, e := s.Provider.GetUser(ctx, &cognitoidentityprovider.GetUserInput{AccessToken: aws.String(token)})
	if e != nil {
		return e
	}
	if !slices.Contains(user.UserMFASettingList, "SOFTWARE_TOKEN_MFA") {
		return ErrPasskeyNotReady
	}
	u, e := url.Parse(s.LoginDomain)
	if e != nil || u.Hostname() == "" {
		return ErrPasskeyNotReady
	}
	found := false
	var next *string
	for {
		out, e := s.Provider.ListWebAuthnCredentials(ctx, &cognitoidentityprovider.ListWebAuthnCredentialsInput{AccessToken: aws.String(token), NextToken: next})
		if e != nil {
			return e
		}
		for _, credential := range out.Credentials {
			if aws.ToString(credential.RelyingPartyId) == u.Hostname() {
				found = true
				break
			}
		}
		if found || out.NextToken == nil || *out.NextToken == "" {
			break
		}
		next = out.NextToken
	}
	if !found {
		return ErrPasskeyNotReady
	}
	_, e = s.Provider.SetUserMFAPreference(ctx, &cognitoidentityprovider.SetUserMFAPreferenceInput{AccessToken: aws.String(token), SoftwareTokenMfaSettings: &types.SoftwareTokenMfaSettingsType{Enabled: true, PreferredMfa: aws.ToString(user.PreferredMfaSetting) == "SOFTWARE_TOKEN_MFA"}, WebAuthnMfaSettings: &types.WebAuthnMfaSettingsType{Enabled: true}})
	return e
}
