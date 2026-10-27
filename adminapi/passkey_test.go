package adminapi

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"testing"
)

type factorFake struct {
	totp, credential bool
	calls            int
	set              *cognitoidentityprovider.SetUserMFAPreferenceInput
}

func (f *factorFake) GetUser(context.Context, *cognitoidentityprovider.GetUserInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.GetUserOutput, error) {
	out := &cognitoidentityprovider.GetUserOutput{PreferredMfaSetting: aws.String("SOFTWARE_TOKEN_MFA")}
	if f.totp {
		out.UserMFASettingList = []string{"SOFTWARE_TOKEN_MFA"}
	}
	return out, nil
}
func (f *factorFake) ListWebAuthnCredentials(context.Context, *cognitoidentityprovider.ListWebAuthnCredentialsInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListWebAuthnCredentialsOutput, error) {
	f.calls++
	out := &cognitoidentityprovider.ListWebAuthnCredentialsOutput{}
	if f.credential {
		out.Credentials = []types.WebAuthnCredentialDescription{{CredentialId: aws.String("credential"), RelyingPartyId: aws.String("login.auth.us-east-2.amazoncognito.com")}}
	}
	return out, nil
}
func (f *factorFake) SetUserMFAPreference(_ context.Context, in *cognitoidentityprovider.SetUserMFAPreferenceInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.SetUserMFAPreferenceOutput, error) {
	f.set = in
	return &cognitoidentityprovider.SetUserMFAPreferenceOutput{}, nil
}
func TestPasskeyMFARequiresTOTPAndRegistrationAndPreservesFallback(t *testing.T) {
	for _, tt := range []struct{ totp, credential, want bool }{{false, false, false}, {false, true, false}, {true, false, false}, {true, true, true}} {
		provider := &factorFake{totp: tt.totp, credential: tt.credential}
		service := PasskeyMFA{Provider: provider, LoginDomain: "https://login.auth.us-east-2.amazoncognito.com"}
		e := service.Activate(context.Background(), "signed-token")
		if tt.want {
			if e != nil || provider.set == nil || !provider.set.WebAuthnMfaSettings.Enabled || !provider.set.SoftwareTokenMfaSettings.Enabled || !provider.set.SoftwareTokenMfaSettings.PreferredMfa || aws.ToString(provider.set.AccessToken) != "signed-token" {
				t.Fatal(provider.set, e)
			}
		} else if e != ErrPasskeyNotReady || provider.set != nil {
			t.Fatal("activated incomplete factor", tt, e)
		}
	}
}
func TestPasskeyActivationSameOriginAndJWTProtected(t *testing.T) {
	h, _ := setup()
	provider := &factorFake{totp: true, credential: true}
	h.Passkeys = &PasskeyMFA{Provider: provider, LoginDomain: h.LoginDomain}
	req := request(h, "POST", "/api/passkey/activate")
	req.Body = "{}"
	req.Headers["authorization"] = "Bearer signed-token"
	out, _ := h.Handle(context.Background(), req)
	if out.StatusCode != 200 || provider.set == nil {
		t.Fatal(out)
	}
	provider.set = nil
	req.Headers["origin"] = "https://evil.example"
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 403 || provider.set != nil {
		t.Fatal("cross-origin factor activation", out)
	}
	req.Headers["origin"] = h.Origin
	req.RequestContext.Authorizer.JWT.Claims["client_id"] = "end-user-client"
	out, _ = h.Handle(context.Background(), req)
	if out.StatusCode != 401 || provider.set != nil {
		t.Fatal("end-user token activated MFA", out)
	}
}
