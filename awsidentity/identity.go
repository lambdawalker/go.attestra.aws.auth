package awsidentity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.attestra.aws.auth/email"
)

type Identity struct {
	Cognito                 *cognitoidentityprovider.Client
	DB                      *dynamodb.Client
	Table, PoolID, ClientID string
}

func (i Identity) user(ctx context.Context, address string) (*cognitoidentityprovider.AdminGetUserOutput, error) {
	return i.Cognito.AdminGetUser(ctx, &cognitoidentityprovider.AdminGetUserInput{UserPoolId: aws.String(i.PoolID), Username: aws.String(address)})
}
func (i Identity) Eligible(ctx context.Context, address string) (bool, error) {
	_, err := i.user(ctx, address)
	if err == nil {
		return false, nil
	}
	var missing *types.UserNotFoundException
	if errors.As(err, &missing) {
		return true, nil
	}
	return false, err
}
func sub(attrs []types.AttributeType) string {
	for _, a := range attrs {
		if aws.ToString(a.Name) == "sub" {
			return aws.ToString(a.Value)
		}
	}
	return ""
}
func (i Identity) Confirm(ctx context.Context, address string) (string, error) {
	log.Printf("cognito_create_user_start email=%s pool=%s", address, i.PoolID)
	out, err := i.Cognito.AdminCreateUser(ctx, &cognitoidentityprovider.AdminCreateUserInput{UserPoolId: aws.String(i.PoolID), Username: aws.String(address), MessageAction: types.MessageActionTypeSuppress, UserAttributes: []types.AttributeType{{Name: aws.String("email"), Value: aws.String(address)}, {Name: aws.String("email_verified"), Value: aws.String("true")}}})
	if err != nil {
		log.Printf("cognito_create_user_failed email=%s err=%v", address, err)
		return "", err
	}
	id := sub(out.User.Attributes)
	if id == "" {
		log.Printf("cognito_create_user_missing_sub email=%s", address)
		return "", errors.New("Cognito returned no sub")
	}
	log.Printf("cognito_create_user_ok email=%s sub=%s", address, id)
	return id, nil
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (i Identity) Session(ctx context.Context, address string) (email.Session, error) {
	log.Printf("cognito_session_start email=%s pool=%s client=%s", address, i.PoolID, i.ClientID)
	u, err := i.user(ctx, address)
	if err != nil {
		log.Printf("cognito_session_get_user_failed err=%v", err)
		return email.Session{}, err
	}
	subject := sub(u.UserAttributes)
	if subject == "" {
		log.Print("cognito_session_get_user_missing_sub")
		return email.Session{}, errors.New("missing sub")
	}
	log.Printf("cognito_session_get_user_ok sub=%s", subject)
	id, secret := random(), random()
	digest := sha256.Sum256([]byte(secret))
	now := time.Now().UTC()
	_, err = i.DB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(i.Table), Item: map[string]dtypes.AttributeValue{"id": &dtypes.AttributeValueMemberS{Value: "grant#" + id}, "sub": &dtypes.AttributeValueMemberS{Value: subject}, "digest": &dtypes.AttributeValueMemberS{Value: hex.EncodeToString(digest[:])}, "expires": &dtypes.AttributeValueMemberN{Value: fmt.Sprint(now.Add(2 * time.Minute).Unix())}, "ttl": &dtypes.AttributeValueMemberN{Value: fmt.Sprint(now.Add(time.Hour).Unix())}}, ConditionExpression: aws.String("attribute_not_exists(id)")})
	if err != nil {
		log.Printf("cognito_session_grant_write_failed err=%v", err)
		return email.Session{}, err
	}
	log.Printf("cognito_session_grant_stored grant_id=%s", id)
	start, err := i.Cognito.AdminInitiateAuth(ctx, &cognitoidentityprovider.AdminInitiateAuthInput{UserPoolId: aws.String(i.PoolID), ClientId: aws.String(i.ClientID), AuthFlow: types.AuthFlowTypeCustomAuth, AuthParameters: map[string]string{"USERNAME": address}})
	if err != nil {
		log.Printf("cognito_session_initiate_failed err=%v", err)
		return email.Session{}, err
	}
	log.Printf("cognito_session_initiate_result challenge=%s has_session=%t", start.ChallengeName, start.Session != nil)
	if start.ChallengeName != types.ChallengeNameTypeCustomChallenge || start.Session == nil {
		return email.Session{}, errors.New("unexpected Cognito challenge")
	}
	finish, err := i.Cognito.AdminRespondToAuthChallenge(ctx, &cognitoidentityprovider.AdminRespondToAuthChallengeInput{UserPoolId: aws.String(i.PoolID), ClientId: aws.String(i.ClientID), ChallengeName: types.ChallengeNameTypeCustomChallenge, Session: start.Session, ChallengeResponses: map[string]string{"USERNAME": address, "ANSWER": id + "." + secret}})
	if err != nil {
		log.Printf("cognito_session_respond_failed err=%v", err)
		return email.Session{}, err
	}
	log.Printf("cognito_session_respond_result challenge=%s has_auth_result=%t", finish.ChallengeName, finish.AuthenticationResult != nil)
	if finish.AuthenticationResult == nil || finish.ChallengeName != "" {
		return email.Session{}, errors.New("Cognito did not issue a session")
	}
	r := finish.AuthenticationResult
	log.Printf("cognito_session_success access=%t id=%t refresh=%t", r.AccessToken != nil, r.IdToken != nil, r.RefreshToken != nil)
	return email.Session{AccessToken: aws.ToString(r.AccessToken), IDToken: aws.ToString(r.IdToken), RefreshToken: aws.ToString(r.RefreshToken), ExpiresIn: r.ExpiresIn, TokenType: aws.ToString(r.TokenType)}, nil
}
