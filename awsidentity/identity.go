package awsidentity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.attestra.aws.auth/email"
)

type Identity struct{ Cognito *cognitoidentityprovider.Client;DB *dynamodb.Client;Table,PoolID,ClientID string }
func(i Identity) user(ctx context.Context,address string)(*cognitoidentityprovider.AdminGetUserOutput,error){return i.Cognito.AdminGetUser(ctx,&cognitoidentityprovider.AdminGetUserInput{UserPoolId:aws.String(i.PoolID),Username:aws.String(address)})}
func(i Identity) Eligible(ctx context.Context,address string)(bool,error){
	_,err:=i.user(ctx,address);if err==nil{return false,nil};var missing *types.UserNotFoundException;if errors.As(err,&missing){return true,nil};return false,err
}
func sub(attrs []types.AttributeType)string{for _,a:=range attrs{if aws.ToString(a.Name)=="sub"{return aws.ToString(a.Value)}};return ""}
func(i Identity) Confirm(ctx context.Context,address string)(string,error){
	out,err:=i.Cognito.AdminCreateUser(ctx,&cognitoidentityprovider.AdminCreateUserInput{UserPoolId:aws.String(i.PoolID),Username:aws.String(address),MessageAction:types.MessageActionTypeSuppress,UserAttributes:[]types.AttributeType{{Name:aws.String("email"),Value:aws.String(address)},{Name:aws.String("email_verified"),Value:aws.String("true")}}})
	if err!=nil{return "",err};id:=sub(out.User.Attributes);if id==""{return "",errors.New("Cognito returned no sub")};return id,nil
}
func random()string{b:=make([]byte,32);if _,err:=rand.Read(b);err!=nil{panic(err)};return base64.RawURLEncoding.EncodeToString(b)}
func(i Identity) Session(ctx context.Context,address string)(email.Session,error){
	u,err:=i.user(ctx,address);if err!=nil{return email.Session{},err};subject:=sub(u.UserAttributes);if subject==""{return email.Session{},errors.New("missing sub")}
	id,secret:=random(),random();digest:=sha256.Sum256([]byte(secret));now:=time.Now().UTC()
	_,err=i.DB.PutItem(ctx,&dynamodb.PutItemInput{TableName:aws.String(i.Table),Item:map[string]dtypes.AttributeValue{"id":&dtypes.AttributeValueMemberS{Value:"grant#"+id},"sub":&dtypes.AttributeValueMemberS{Value:subject},"digest":&dtypes.AttributeValueMemberS{Value:hex.EncodeToString(digest[:])},"expires":&dtypes.AttributeValueMemberN{Value:fmt.Sprint(now.Add(2*time.Minute).Unix())},"ttl":&dtypes.AttributeValueMemberN{Value:fmt.Sprint(now.Add(time.Hour).Unix())}},ConditionExpression:aws.String("attribute_not_exists(id)")});if err!=nil{return email.Session{},err}
	start,err:=i.Cognito.AdminInitiateAuth(ctx,&cognitoidentityprovider.AdminInitiateAuthInput{UserPoolId:aws.String(i.PoolID),ClientId:aws.String(i.ClientID),AuthFlow:types.AuthFlowTypeCustomAuth,AuthParameters:map[string]string{"USERNAME":address}});if err!=nil{return email.Session{},err}
	if start.ChallengeName!=types.ChallengeNameTypeCustomChallenge||start.Session==nil{return email.Session{},errors.New("unexpected Cognito challenge")}
	finish,err:=i.Cognito.AdminRespondToAuthChallenge(ctx,&cognitoidentityprovider.AdminRespondToAuthChallengeInput{UserPoolId:aws.String(i.PoolID),ClientId:aws.String(i.ClientID),ChallengeName:types.ChallengeNameTypeCustomChallenge,Session:start.Session,ChallengeResponses:map[string]string{"USERNAME":address,"ANSWER":id+"."+secret}});if err!=nil{return email.Session{},err}
	if finish.AuthenticationResult==nil||finish.ChallengeName!=""{return email.Session{},errors.New("Cognito did not issue a session")}
	r:=finish.AuthenticationResult;return email.Session{AccessToken:aws.ToString(r.AccessToken),IDToken:aws.ToString(r.IdToken),RefreshToken:aws.ToString(r.RefreshToken),ExpiresIn:r.ExpiresIn,TokenType:aws.ToString(r.TokenType)},nil
}
