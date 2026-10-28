package signin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	cognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
)

var ErrPasswordPolicy = errors.New("password_policy")
var ErrEnrollmentDisabled = errors.New("password_disabled")

type EnrollmentIdentity interface {
	GetUser(context.Context, *cognito.GetUserInput, ...func(*cognito.Options)) (*cognito.GetUserOutput, error)
	AdminSetUserPassword(context.Context, *cognito.AdminSetUserPasswordInput, ...func(*cognito.Options)) (*cognito.AdminSetUserPasswordOutput, error)
	ListWebAuthnCredentials(context.Context, *cognito.ListWebAuthnCredentialsInput, ...func(*cognito.Options)) (*cognito.ListWebAuthnCredentialsOutput, error)
}
type EnrollmentFacts struct {
	PasswordRegistered bool `json:"password_registered"`
	IDCaptured         bool `json:"id_captured"`
}
type EnrollmentStore interface {
	Get(context.Context, string) (EnrollmentFacts, error)
	Mark(context.Context, string, bool, bool) error
}
type CaptureReader interface {
	Account(context.Context, string) (capture.Account, error)
	Get(context.Context, string, string) (capture.Record, error)
}
type Enrollment struct {
	Identity                              EnrollmentIdentity
	Config                                runtimeconfig.Reader
	Facts                                 EnrollmentStore
	Captures                              CaptureReader
	Environment, PoolID, ClientID, Issuer string
}
type AccountStatus struct {
	PasskeyRegistered  bool `json:"passkey_registered"`
	PasswordRegistered bool `json:"password_registered"`
	IDCaptured         bool `json:"id_captured"`
	Complete           bool `json:"onboarding_complete"`
}

func (s Enrollment) policy(ctx context.Context) (runtimeconfig.Onboarding, error) {
	if s.Config == nil {
		return runtimeconfig.Onboarding{}, ErrUnavailable
	}
	c, err := s.Config.Get(ctx, s.Environment)
	if err != nil || c.Environment != s.Environment || c.Validate() != nil {
		return runtimeconfig.Onboarding{}, ErrUnavailable
	}
	return c.EffectiveOnboarding(), nil
}

// GetUser validates the access token with Cognito. Only then trust its claims,
// and additionally bind it to this environment's exact pool and app client.
func (s Enrollment) user(ctx context.Context, token string) (string, string, error) {
	if token == "" || len(token) > 8192 || s.Identity == nil || s.PoolID == "" || s.ClientID == "" || s.Issuer == "" {
		return "", "", ErrUnauthenticated
	}
	u, err := s.Identity.GetUser(ctx, &cognito.GetUserInput{AccessToken: aws.String(token)})
	if err != nil {
		if errors.Is(providerError(err), ErrRejected) {
			return "", "", ErrUnauthenticated
		}
		return "", "", ErrUnavailable
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", ErrUnauthenticated
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", ErrUnauthenticated
	}
	var claims struct {
		Issuer  string `json:"iss"`
		Client  string `json:"client_id"`
		Use     string `json:"token_use"`
		Subject string `json:"sub"`
	}
	if json.Unmarshal(data, &claims) != nil || claims.Issuer != s.Issuer || claims.Client != s.ClientID || claims.Use != "access" {
		return "", "", ErrUnauthenticated
	}
	if u == nil {
		return "", "", ErrUnauthenticated
	}
	subject, verified := "", false
	for _, a := range u.UserAttributes {
		switch aws.ToString(a.Name) {
		case "sub":
			subject = aws.ToString(a.Value)
		case "email_verified":
			verified = aws.ToString(a.Value) == "true"
		}
	}
	if !verified || subject == "" || subject != claims.Subject || aws.ToString(u.Username) == "" {
		return "", "", ErrUnauthenticated
	}
	return aws.ToString(u.Username), subject, nil
}
func (s Enrollment) SetPassword(ctx context.Context, token, password string) error {
	username, subject, err := s.user(ctx, token)
	if err != nil {
		return err
	}
	p, err := s.policy(ctx)
	if err != nil {
		return err
	}
	if p.Password == runtimeconfig.Disabled {
		return ErrEnrollmentDisabled
	}
	if p.PasswordPolicy.Check(password) != nil {
		return ErrPasswordPolicy
	}
	if s.Facts == nil {
		return ErrUnavailable
	}
	_, err = s.Identity.AdminSetUserPassword(ctx, &cognito.AdminSetUserPasswordInput{UserPoolId: aws.String(s.PoolID), Username: aws.String(username), Password: aws.String(password), Permanent: true})
	if err != nil {
		return ErrUnavailable
	}
	if s.Facts.Mark(ctx, subject, true, false) != nil {
		return ErrUnavailable
	}
	return nil
}
func (s Enrollment) Status(ctx context.Context, token string) (AccountStatus, error) {
	_, subject, err := s.user(ctx, token)
	if err != nil {
		return AccountStatus{}, err
	}
	p, err := s.policy(ctx)
	if err != nil {
		return AccountStatus{}, err
	}
	if s.Facts == nil {
		return AccountStatus{}, ErrUnavailable
	}
	facts, err := s.Facts.Get(ctx, subject)
	if err != nil {
		return AccountStatus{}, ErrUnavailable
	}
	credentials, err := s.Identity.ListWebAuthnCredentials(ctx, &cognito.ListWebAuthnCredentialsInput{AccessToken: aws.String(token), MaxResults: aws.Int32(1)})
	if err != nil || credentials == nil {
		return AccountStatus{}, ErrUnavailable
	}
	if !facts.IDCaptured && s.Captures != nil {
		account, e := s.Captures.Account(ctx, subject)
		if e != nil && !errors.Is(e, capture.ErrNotFound) {
			return AccountStatus{}, ErrUnavailable
		}
		if e == nil && account.IDCaptured {
			if s.Facts.Mark(ctx, subject, false, true) != nil {
				return AccountStatus{}, ErrUnavailable
			}
			facts.IDCaptured = true
		}
		if e == nil && !facts.IDCaptured && account.CurrentID != "" {
			record, e := s.Captures.Get(ctx, subject, account.CurrentID)
			if e != nil && !errors.Is(e, capture.ErrNotFound) {
				return AccountStatus{}, ErrUnavailable
			}
			if e == nil && record.State == "ready" {
				if s.Facts.Mark(ctx, subject, false, true) != nil {
					return AccountStatus{}, ErrUnavailable
				}
				facts.IDCaptured = true
			}
		}
	}
	out := AccountStatus{PasskeyRegistered: len(credentials.Credentials) > 0, PasswordRegistered: facts.PasswordRegistered, IDCaptured: facts.IDCaptured}
	out.Complete = p.Complete(out.PasskeyRegistered, out.PasswordRegistered, out.IDCaptured)
	return out, nil
}
