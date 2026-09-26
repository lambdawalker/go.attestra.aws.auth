package passkey

import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "log"
 "net/http"
 "regexp"
 "strings"
 "time"

 "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
 "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_=.\-]+$`)
var ErrUnauthorized = errors.New("invalid or expired session")
var ErrInvalid = errors.New("invalid credential")
var ErrUnavailable = errors.New("passkey service unavailable")

type Starter interface {
 StartWebAuthnRegistration(context.Context, *cognitoidentityprovider.StartWebAuthnRegistrationInput, ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.StartWebAuthnRegistrationOutput, error)
}
type Service struct {
 Cognito Starter
 Endpoint string
 HTTP *http.Client
}

func Bearer(header string) (string, error) {
 parts := strings.Split(header, " ")
 if len(parts) != 2 || parts[0] != "Bearer" || len(parts[1]) > 8192 || !tokenPattern.MatchString(parts[1]) { return "", ErrUnauthorized }
 return parts[1], nil
}

func (s Service) Options(ctx context.Context, token string) (json.RawMessage, error) {
 out, err := s.Cognito.StartWebAuthnRegistration(ctx, &cognitoidentityprovider.StartWebAuthnRegistrationInput{AccessToken: &token})
 if err != nil { return nil, classify(err) }
 if out.CredentialCreationOptions == nil { return nil, ErrUnavailable }
 var options any
 if err := out.CredentialCreationOptions.UnmarshalSmithyDocument(&options); err != nil { return nil, ErrUnavailable }
 b, err := json.Marshal(options)
 if err != nil { return nil, ErrUnavailable }
 return b, nil
}

func (s Service) Complete(ctx context.Context, token string, credential json.RawMessage) error {
 if len(credential) == 0 || len(credential) > 32768 || !json.Valid(credential) || credential[0] != '{' { return ErrInvalid }
 var fields struct { ID string `json:"id"`; Type string `json:"type"`; Response struct { ClientData string `json:"clientDataJSON"`; Attestation string `json:"attestationObject"` } `json:"response"` }
 if json.Unmarshal(credential, &fields) != nil || fields.ID == "" || fields.Type != "public-key" || fields.Response.ClientData == "" || fields.Response.Attestation == "" { return ErrInvalid }
 body, _ := json.Marshal(struct { AccessToken string `json:"AccessToken"`; Credential json.RawMessage `json:"Credential"` }{token, credential})
 req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
 if err != nil { return ErrUnavailable }
 req.Header.Set("Content-Type", "application/x-amz-json-1.1")
 req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.CompleteWebAuthnRegistration")
 client := s.HTTP
 if client == nil { client = &http.Client{Timeout: 15*time.Second} }
 resp, err := client.Do(req)
 if err != nil { log.Printf("passkey_complete_provider_error err=%v", err); return ErrUnavailable }
 defer resp.Body.Close()
 if resp.StatusCode == http.StatusOK { io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); return nil }
 data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
 var provider struct { Type string `json:"__type"` }
 _ = json.Unmarshal(data, &provider)
 log.Printf("passkey_complete_provider_failed status=%d type=%s", resp.StatusCode, provider.Type)
 if strings.Contains(provider.Type, "NotAuthorized") || strings.Contains(provider.Type, "Expired") { return ErrUnauthorized }
 if strings.Contains(provider.Type, "InvalidParameter") || strings.Contains(provider.Type, "WebAuthn") { return ErrInvalid }
 return fmt.Errorf("%w: provider status %d", ErrUnavailable, resp.StatusCode)
}

func classify(err error) error {
 var unauthorized *types.NotAuthorizedException
 if errors.As(err, &unauthorized) { return ErrUnauthorized }
 log.Printf("passkey_options_provider_failed err=%v", err)
 return ErrUnavailable
}
