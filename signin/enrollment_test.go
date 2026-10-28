package signin

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	cognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"testing"
)

type enrollmentIdentity struct {
	sets      int
	userError error
	setError  error
	passkey   bool
	last      *cognito.AdminSetUserPasswordInput
}

func (f *enrollmentIdentity) GetUser(context.Context, *cognito.GetUserInput, ...func(*cognito.Options)) (*cognito.GetUserOutput, error) {
	return &cognito.GetUserOutput{Username: aws.String("user"), UserAttributes: []types.AttributeType{{Name: aws.String("sub"), Value: aws.String("subject")}, {Name: aws.String("email_verified"), Value: aws.String("true")}}}, f.userError
}
func (f *enrollmentIdentity) AdminSetUserPassword(_ context.Context, in *cognito.AdminSetUserPasswordInput, _ ...func(*cognito.Options)) (*cognito.AdminSetUserPasswordOutput, error) {
	f.sets++
	f.last = in
	return &cognito.AdminSetUserPasswordOutput{}, f.setError
}
func (f *enrollmentIdentity) ListWebAuthnCredentials(context.Context, *cognito.ListWebAuthnCredentialsInput, ...func(*cognito.Options)) (*cognito.ListWebAuthnCredentialsOutput, error) {
	o := &cognito.ListWebAuthnCredentialsOutput{}
	if f.passkey {
		o.Credentials = []types.WebAuthnCredentialDescription{{}}
	}
	return o, nil
}

type configReader struct {
	c   runtimeconfig.Config
	err error
}

func (r *configReader) Get(context.Context, string) (runtimeconfig.Config, error) { return r.c, r.err }

type factMemory struct {
	facts EnrollmentFacts
	err   error
}

func (f *factMemory) Get(context.Context, string) (EnrollmentFacts, error) { return f.facts, f.err }
func (f *factMemory) Mark(_ context.Context, _ string, password, captured bool) error {
	if f.err != nil {
		return f.err
	}
	f.facts.PasswordRegistered = f.facts.PasswordRegistered || password
	f.facts.IDCaptured = f.facts.IDCaptured || captured
	return nil
}

type captureReader struct {
	ready     bool
	completed bool
}

func (c captureReader) Account(context.Context, string) (capture.Account, error) {
	return capture.Account{CurrentID: "capture", IDCaptured: c.completed}, nil
}
func (c captureReader) Get(context.Context, string, string) (capture.Record, error) {
	state := "uploading"
	if c.ready {
		state = "ready"
	}
	return capture.Record{State: state}, nil
}
func testAccess(claims string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".signature"
}

const claims = `{"iss":"https://cognito-idp.us-east-1.amazonaws.com/pool","client_id":"client","token_use":"access","sub":"subject"}`

func enrollmentFixture() (*Enrollment, *enrollmentIdentity, *configReader, *factMemory) {
	id := &enrollmentIdentity{}
	cfg := &configReader{c: runtimeconfig.Default("dev")}
	facts := &factMemory{}
	return &Enrollment{Identity: id, Config: cfg, Facts: facts, Captures: captureReader{}, Environment: "dev", PoolID: "pool", ClientID: "client", Issuer: "https://cognito-idp.us-east-1.amazonaws.com/pool"}, id, cfg, facts
}
func TestPasswordEnrollmentPolicyAndVerifiedAccountBinding(t *testing.T) {
	s, id, cfg, facts := enrollmentFixture()
	ctx := context.Background()
	token := testAccess(claims)
	if err := s.SetPassword(ctx, token, "ValidPassword123"); !errors.Is(err, ErrEnrollmentDisabled) {
		t.Fatal(err)
	}
	cfg.c.Onboarding.Password = runtimeconfig.Required
	cfg.c.Onboarding.PasswordPlacement = "after_verification"
	if err := s.SetPassword(ctx, token, "short"); !errors.Is(err, ErrPasswordPolicy) {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"iss":"wrong","client_id":"client","token_use":"access","sub":"subject"}`, `{"iss":"https://cognito-idp.us-east-1.amazonaws.com/pool","client_id":"other","token_use":"access","sub":"subject"}`, `{"iss":"https://cognito-idp.us-east-1.amazonaws.com/pool","client_id":"client","token_use":"id","sub":"subject"}`} {
		if err := s.SetPassword(ctx, testAccess(bad), "ValidPassword123"); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal(err)
		}
	}
	if id.sets != 0 {
		t.Fatal("provider modified before validation")
	}
	if err := s.SetPassword(ctx, token, "ValidPassword123"); err != nil {
		t.Fatal(err)
	}
	if !facts.facts.PasswordRegistered || aws.ToString(id.last.Username) != "user" || aws.ToString(id.last.UserPoolId) != "pool" || !id.last.Permanent {
		t.Fatal("wrong user or missing completion")
	}
}
func TestPasswordProviderFailureDoesNotMarkEnrolled(t *testing.T) {
	s, id, cfg, facts := enrollmentFixture()
	cfg.c.Onboarding.Password = runtimeconfig.Optional
	cfg.c.Onboarding.PasswordPlacement = "with_email"
	id.setError = errors.New("offline")
	if s.SetPassword(context.Background(), testAccess(claims), "ValidPassword123") == nil || facts.facts.PasswordRegistered {
		t.Fatal("false success")
	}
}
func TestRequiredCompletionUsesServerFacts(t *testing.T) {
	s, id, cfg, facts := enrollmentFixture()
	ctx := context.Background()
	token := testAccess(claims)
	status, err := s.Status(ctx, token)
	if err != nil || status.Complete {
		t.Fatal(status, err)
	}
	id.passkey = true
	status, err = s.Status(ctx, token)
	if err != nil || status.Complete || status.IDCaptured {
		t.Fatal("unfinished capture counted", status, err)
	}
	s.Captures = captureReader{ready: true}
	status, err = s.Status(ctx, token)
	if err != nil || !status.Complete || !facts.facts.IDCaptured {
		t.Fatal(status, err)
	}
	s.Captures = nil // retention/deletion does not revoke historical onboarding completion
	status, err = s.Status(ctx, token)
	if err != nil || !status.Complete {
		t.Fatal(status, err)
	}
	cfg.c.Onboarding.Password = runtimeconfig.Required
	cfg.c.Onboarding.PasswordPlacement = "after_verification"
	status, err = s.Status(ctx, token)
	if err != nil || status.Complete {
		t.Fatal("new required password bypassed", status, err)
	}
	cfg.err = errors.New("offline")
	if _, err = s.Status(ctx, token); err == nil {
		t.Fatal("config failure did not fail closed")
	}
}

func TestFirstStatusAfterCaptureRetentionUsesDurableAccountFact(t *testing.T) {
	s, id, _, _ := enrollmentFixture()
	id.passkey = true
	s.Captures = captureReader{completed: true}
	result, err := s.Status(context.Background(), testAccess(claims))
	if err != nil || !result.IDCaptured || !result.Complete {
		t.Fatal(result, err)
	}
}
