package signin

import (
 "context"
 "errors"
 "testing"

 "github.com/aws/aws-sdk-go-v2/aws"
 cognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
 "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)
type fake struct { started *cognito.InitiateAuthInput; completed *cognito.RespondToAuthChallengeInput; listed *cognito.ListWebAuthnCredentialsInput }
func(f *fake)InitiateAuth(_ context.Context,in *cognito.InitiateAuthInput,_ ...func(*cognito.Options))(*cognito.InitiateAuthOutput,error){f.started=in;if in.AuthFlow==types.AuthFlowTypeRefreshTokenAuth{return &cognito.InitiateAuthOutput{AuthenticationResult:&types.AuthenticationResultType{AccessToken:aws.String("new-access"),IdToken:aws.String("id"),ExpiresIn:3600}},nil};return &cognito.InitiateAuthOutput{ChallengeName:types.ChallengeNameType(in.AuthParameters["PREFERRED_CHALLENGE"]),Session:aws.String("challenge-session"),ChallengeParameters:map[string]string{"CREDENTIAL_REQUEST_OPTIONS":`{"challenge":"test"}`}},nil}
func(f *fake)RespondToAuthChallenge(_ context.Context,in *cognito.RespondToAuthChallengeInput,_ ...func(*cognito.Options))(*cognito.RespondToAuthChallengeOutput,error){f.completed=in;return &cognito.RespondToAuthChallengeOutput{AuthenticationResult:&types.AuthenticationResultType{AccessToken:aws.String("access"),RefreshToken:aws.String("refresh")}},nil}
func(f *fake)ListWebAuthnCredentials(_ context.Context,in *cognito.ListWebAuthnCredentialsInput,_ ...func(*cognito.Options))(*cognito.ListWebAuthnCredentialsOutput,error){f.listed=in;return &cognito.ListWebAuthnCredentialsOutput{},nil}
func TestEmailOTPUsesChallengeSession(t *testing.T){f:=&fake{};s:=Service{Client:f,ClientID:"client"};challenge,e:=s.Start(context.Background(),"user@example.com","EMAIL_OTP");if e!=nil||challenge.Session==""{t.Fatal(e)};result,e:=s.Finish(context.Background(),"user@example.com","EMAIL_OTP",challenge.Session,"123456");if e!=nil||result.RefreshToken!="refresh"||f.completed.ChallengeResponses["EMAIL_OTP_CODE"]!="123456"{t.Fatalf("result=%+v err=%v",result,e)}}
func TestPasskeyAndRefresh(t *testing.T){f:=&fake{};s:=Service{Client:f,ClientID:"client"};challenge,e:=s.Start(context.Background(),"user@example.com","WEB_AUTHN");if e!=nil||challenge.Options==""{t.Fatal(e)};_,e=s.Finish(context.Background(),"user@example.com","WEB_AUTHN",challenge.Session,`{"id":"key"}`);if e!=nil||f.completed.ChallengeResponses["CREDENTIAL"]==""{t.Fatal(e)};session,e:=s.Refresh(context.Background(),"old-refresh");if e!=nil||session.AccessToken!="new-access"{t.Fatal(e)};if _,e=s.HasPasskey(context.Background(),"access");e!=nil||f.listed==nil{t.Fatal(e)}}
func TestInvalidCodeRejectedBeforeProvider(t *testing.T){f:=&fake{};_,e:=(Service{Client:f}).Finish(context.Background(),"user@example.com","EMAIL_OTP","session","invalid");if !errors.Is(e,ErrRejected)||f.completed!=nil{t.Fatal(e)}}
