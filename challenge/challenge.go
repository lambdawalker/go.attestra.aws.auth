package challenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// A single Lambda handles the three Cognito custom-challenge triggers. No challenge
// secret or grant identifier is placed in public challenge parameters.
type Event struct {
	TriggerSource string `json:"triggerSource"`
	Request       struct {
		UserAttributes  map[string]string `json:"userAttributes"`
		UserNotFound    bool              `json:"userNotFound"`
		ChallengeAnswer string            `json:"challengeAnswer"`
		Session         []struct {
			ChallengeName   string `json:"challengeName"`
			ChallengeResult bool   `json:"challengeResult"`
		} `json:"session"`
	} `json:"request"`
	Response map[string]any `json:"response"`
}
type Handler struct {
	DB    *dynamodb.Client
	Table string
}

func (h Handler) Handle(ctx context.Context, raw json.RawMessage) (Event, error) {
	var e Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, err
	}
	if e.Response == nil {
		e.Response = map[string]any{}
	}
	switch e.TriggerSource {
	case "DefineAuthChallenge_Authentication":
		if e.Request.UserNotFound {
			e.Response["failAuthentication"] = true
			return e, nil
		}
		n := len(e.Request.Session)
		if n == 0 {
			e.Response["challengeName"] = "CUSTOM_CHALLENGE"
			return e, nil
		}
		last := e.Request.Session[n-1]
		if last.ChallengeName == "CUSTOM_CHALLENGE" && last.ChallengeResult {
			e.Response["issueTokens"] = true
		} else {
			e.Response["failAuthentication"] = true
		}
	case "CreateAuthChallenge_Authentication":
		e.Response["publicChallengeParameters"] = map[string]string{"type": "onboarding-grant"}
		e.Response["privateChallengeParameters"] = map[string]string{"type": "onboarding-grant"}
		e.Response["challengeMetadata"] = "ONBOARDING_GRANT"
	case "VerifyAuthChallengeResponse_Authentication":
		e.Response["answerCorrect"] = h.verify(ctx, e.Request.UserAttributes["sub"], e.Request.ChallengeAnswer)
	default:
		return e, fmt.Errorf("unsupported trigger: %s", e.TriggerSource)
	}
	return e, nil
}
func (h Handler) verify(ctx context.Context, subject, answer string) bool {
	parts := strings.Split(answer, ".")
	if len(parts) != 2 || subject == "" || len(parts[0]) != 43 || len(parts[1]) != 43 {
		return false
	}
	digest := sha256.Sum256([]byte(parts[1]))
	_, err := h.DB.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(h.Table), Key: map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: "grant#" + parts[0]}}, UpdateExpression: aws.String("SET used = :yes"), ConditionExpression: aws.String("#sub = :sub AND digest = :digest AND expires > :now AND attribute_not_exists(used)"), ExpressionAttributeNames: map[string]string{"#sub": "sub"}, ExpressionAttributeValues: map[string]types.AttributeValue{":sub": &types.AttributeValueMemberS{Value: subject}, ":digest": &types.AttributeValueMemberS{Value: hex.EncodeToString(digest[:])}, ":now": &types.AttributeValueMemberN{Value: fmt.Sprint(time.Now().Unix())}, ":yes": &types.AttributeValueMemberBOOL{Value: true}}})
	return err == nil
}
