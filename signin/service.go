package signin

import (
 "context"
 "errors"
 "strings"

 "github.com/aws/aws-sdk-go-v2/aws"
 cognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
 "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

var ErrUnauthenticated=errors.New("sign in required")
var ErrRejected=errors.New("authentication failed")
var ErrUnavailable=errors.New("authentication service unavailable")

type Cognito interface {
 InitiateAuth(context.Context,*cognito.InitiateAuthInput,...func(*cognito.Options))(*cognito.InitiateAuthOutput,error)
 RespondToAuthChallenge(context.Context,*cognito.RespondToAuthChallengeInput,...func(*cognito.Options))(*cognito.RespondToAuthChallengeOutput,error)
 ListWebAuthnCredentials(context.Context,*cognito.ListWebAuthnCredentialsInput,...func(*cognito.Options))(*cognito.ListWebAuthnCredentialsOutput,error)
}
type Service struct { Client Cognito; ClientID string }
type Session struct {
 AccessToken string `json:"access_token"`
 IDToken string `json:"id_token"`
 RefreshToken string `json:"refresh_token"`
 ExpiresIn int32 `json:"expires_in"`
 TokenType string `json:"token_type"`
}
type Challenge struct { Session string `json:"session"`; Options string `json:"options,omitempty"` }
func tokens(a *types.AuthenticationResultType) (Session,error) {
 if a==nil || aws.ToString(a.AccessToken)=="" { return Session{},ErrRejected }
 return Session{aws.ToString(a.AccessToken),aws.ToString(a.IdToken),aws.ToString(a.RefreshToken),a.ExpiresIn,aws.ToString(a.TokenType)},nil
}
func providerError(err error) error {
 var auth *types.NotAuthorizedException
 var code *types.CodeMismatchException
 var expired *types.ExpiredCodeException
 var missing *types.UserNotFoundException
 if errors.As(err,&auth)||errors.As(err,&code)||errors.As(err,&expired)||errors.As(err,&missing) { return ErrRejected }
 return ErrUnavailable
}
func (s Service) Start(ctx context.Context,email,method string)(Challenge,error){
 if len(email)>254 || !strings.Contains(email,"@") || (method!="EMAIL_OTP" && method!="WEB_AUTHN") {return Challenge{},ErrRejected}
 out,err:=s.Client.InitiateAuth(ctx,&cognito.InitiateAuthInput{ClientId:aws.String(s.ClientID),AuthFlow:types.AuthFlowTypeUserAuth,AuthParameters:map[string]string{"USERNAME":email,"PREFERRED_CHALLENGE":method}})
 if err!=nil{return Challenge{},providerError(err)}
 if string(out.ChallengeName)!=method || aws.ToString(out.Session)=="" { return Challenge{},ErrRejected }
 options:=""
 if method=="WEB_AUTHN" {options=out.ChallengeParameters["CREDENTIAL_REQUEST_OPTIONS"];if options==""{return Challenge{},ErrUnavailable}}
 return Challenge{aws.ToString(out.Session),options},nil
}
func (s Service) Finish(ctx context.Context,email,method,challengeSession,answer string)(Session,error){
 if email==""||len(email)>254||challengeSession==""||len(challengeSession)>8192||answer==""||len(answer)>32768{return Session{},ErrRejected}
 field:="EMAIL_OTP_CODE"
 if method=="EMAIL_OTP" {if len(answer)!=6||strings.Trim(answer,"0123456789")!="" {return Session{},ErrRejected}} else if method=="WEB_AUTHN" {field="CREDENTIAL"} else {return Session{},ErrRejected}
 out,err:=s.Client.RespondToAuthChallenge(ctx,&cognito.RespondToAuthChallengeInput{ClientId:aws.String(s.ClientID),ChallengeName:types.ChallengeNameType(method),Session:aws.String(challengeSession),ChallengeResponses:map[string]string{"USERNAME":email,field:answer}})
 if err!=nil{return Session{},providerError(err)}
 return tokens(out.AuthenticationResult)
}
func (s Service) Refresh(ctx context.Context,refresh string)(Session,error){
 if refresh==""||len(refresh)>8192{return Session{},ErrUnauthenticated}
 out,err:=s.Client.InitiateAuth(ctx,&cognito.InitiateAuthInput{ClientId:aws.String(s.ClientID),AuthFlow:types.AuthFlowTypeRefreshTokenAuth,AuthParameters:map[string]string{"REFRESH_TOKEN":refresh}})
 if err!=nil{return Session{},ErrUnauthenticated}
 return tokens(out.AuthenticationResult)
}
func (s Service) HasPasskey(ctx context.Context,access string)(bool,error){
 if access==""||len(access)>8192{return false,ErrUnauthenticated}
 out,err:=s.Client.ListWebAuthnCredentials(ctx,&cognito.ListWebAuthnCredentialsInput{AccessToken:aws.String(access)})
 if err!=nil {var auth *types.NotAuthorizedException;if errors.As(err,&auth){return false,ErrUnauthenticated};return false,ErrUnavailable}
 return len(out.Credentials)>0,nil
}
