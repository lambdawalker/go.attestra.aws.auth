package passkey

import (
 "context"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
)

type starterFake struct { token string }
func (f *starterFake) StartWebAuthnRegistration(_ context.Context, v *cognitoidentityprovider.StartWebAuthnRegistrationInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.StartWebAuthnRegistrationOutput,error) {
 f.token=*v.AccessToken
 return nil,errors.New("unavailable")
}
func TestBearerRejectsMissingAndMalformedTokens(t *testing.T) {
 for _,header:=range []string{"", "Basic abc", "Bearer", "Bearer a b", "Bearer a/b"} {
  if _,err:=Bearer(header); !errors.Is(err,ErrUnauthorized) { t.Fatalf("%q: expected unauthorized, got %v",header,err) }
 }
 if token,err:=Bearer("Bearer abc.def_123"); err!=nil || token!="abc.def_123" { t.Fatalf("valid token: %q, %v",token,err) }
}
func TestCompleteForwardsRawCredentialAndUserToken(t *testing.T) {
 credential:=json.RawMessage(`{"id":"key","type":"public-key","response":{"clientDataJSON":"data","attestationObject":"attestation"}}`)
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Header.Get("X-Amz-Target")!="AWSCognitoIdentityProviderService.CompleteWebAuthnRegistration" { t.Error("wrong Cognito operation") }
  var input struct { AccessToken string; Credential json.RawMessage }
  if json.NewDecoder(r.Body).Decode(&input)!=nil || input.AccessToken!="user-access-token" || string(input.Credential)!=string(credential) { t.Errorf("invalid forwarded payload: token=%q credential=%s",input.AccessToken,input.Credential) }
  w.WriteHeader(http.StatusOK)
 }))
 defer server.Close()
 if err:=(Service{Endpoint:server.URL}).Complete(context.Background(),"user-access-token",credential); err!=nil { t.Fatal(err) }
}
func TestCompleteDoesNotAcceptProviderFailure(t *testing.T) {
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,_ *http.Request){ w.WriteHeader(400); w.Write([]byte(`{"__type":"NotAuthorizedException"}`)) }))
 defer server.Close()
 credential:=json.RawMessage(`{"id":"key","type":"public-key","response":{"clientDataJSON":"data","attestationObject":"attestation"}}`)
 if err:=(Service{Endpoint:server.URL}).Complete(context.Background(),"token",credential); !errors.Is(err,ErrUnauthorized) { t.Fatalf("expected unauthorized, got %v",err) }
}
