// Package registry manages public environment configuration and deployment fencing.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Configuration struct {
	APIURL            string `json:"apiUrl"`
	AWSRegion         string `json:"awsRegion"`
	CognitoUserPoolID string `json:"cognitoUserPoolId"`
	CognitoClientID   string `json:"cognitoClientId"`
	Features          struct {
		IDCapture bool `json:"idCapture"`
	} `json:"features"`
}
type Entry struct {
	ID         string        `json:"id"`
	Config     Configuration `json:"configuration"`
	ConfigHash string        `json:"configHash"`
	Revision   int64         `json:"revision"`
	UpdatedAt  string        `json:"updatedAt"`
}
type Change struct {
	Operation string        `json:"operation"`
	Token     string        `json:"token,omitempty"`
	Revision  int64         `json:"revision,omitempty"`
	Config    Configuration `json:"configuration,omitempty"`
}
type Receipt struct {
	Token    string `json:"token"`
	Revision int64  `json:"revision"`
}
type Record struct {
	ID        string `dynamodbav:"id"`
	Revision  int64  `dynamodbav:"revision"`
	Lock      string `dynamodbav:"lock,omitempty"`
	Entry     *Entry `dynamodbav:"entry,omitempty"`
	Deleted   bool   `dynamodbav:"deleted"`
	LastToken string `dynamodbav:"lastToken,omitempty"`
}
type DB interface {
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}
type Service struct {
	DB    DB
	Table string
}

func str(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func key(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"id": str(id)}
}

var environmentName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func ValidEnvironment(id string) bool {
	return len(id) > 0 && len(id) <= 32 && environmentName.MatchString(id)
}
func Hash(c Configuration) string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func Validate(c Configuration) error {
	u, e := url.Parse(c.APIURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid HTTPS API URL")
	}
	if !regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]+$`).MatchString(c.AWSRegion) || !strings.HasPrefix(c.CognitoUserPoolID, c.AWSRegion+"_") || len(c.CognitoClientID) < 5 {
		return errors.New("invalid public Cognito configuration")
	}
	return nil
}
func (s Service) get(ctx context.Context, id string) (Record, error) {
	out, e := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.Table), Key: key(id), ConsistentRead: aws.Bool(true)})
	var r Record
	if e != nil {
		return r, e
	}
	e = attributevalue.UnmarshalMap(out.Item, &r)
	return r, e
}
func (s Service) change(ctx context.Context, id string, c Change) (any, error) {
	if !ValidEnvironment(id) {
		return nil, errors.New("invalid environment")
	}
	if c.Operation == "begin" {
		if !regexp.MustCompile(`^[a-f0-9]{48}$`).MatchString(c.Token) {
			return nil, errors.New("invalid deployment token")
		}
		value := c.Token
		current, e := s.get(ctx, id)
		if e != nil {
			return nil, e
		}
		if current.Lock == value {
			return Receipt{Token: value, Revision: current.Revision}, nil
		}

		if current.LastToken == value {
			return nil, &types.ConditionalCheckFailedException{}
		}
		out, e := s.DB.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.Table), Key: key(id), ConditionExpression: aws.String("attribute_not_exists(#lock) AND (attribute_not_exists(lastToken) OR lastToken <> :token)"), UpdateExpression: aws.String("SET #lock = :token ADD revision :one"), ExpressionAttributeNames: map[string]string{"#lock": "lock"}, ExpressionAttributeValues: map[string]types.AttributeValue{":token": str(value), ":one": &types.AttributeValueMemberN{Value: "1"}}, ReturnValues: types.ReturnValueAllNew})
		if e != nil {
			var conflict *types.ConditionalCheckFailedException
			if errors.As(e, &conflict) {
				latest, readErr := s.get(ctx, id)
				if readErr != nil {
					return nil, readErr
				}
				if latest.Lock == value {
					return Receipt{Token: value, Revision: latest.Revision}, nil
				}
			}
			return nil, e
		}
		var r Record
		if e = attributevalue.UnmarshalMap(out.Attributes, &r); e != nil {
			return nil, e
		}
		return Receipt{Token: value, Revision: r.Revision}, nil
	}
	if c.Token == "" || c.Revision < 1 {
		return nil, errors.New("deployment receipt required")
	}
	current, e := s.get(ctx, id)
	if e != nil {
		return nil, e
	}
	if current.Revision != c.Revision {
		return nil, &types.ConditionalCheckFailedException{}
	}
	if current.Lock == "" && current.LastToken == c.Token {
		if c.Operation == "abandon" || (c.Operation == "delete" && current.Deleted) || (c.Operation == "publish" && !current.Deleted && current.Entry != nil && current.Entry.ConfigHash == Hash(c.Config)) {
			return map[string]bool{"ok": true}, nil
		}
	}
	values := map[string]types.AttributeValue{":token": str(c.Token), ":revision": &types.AttributeValueMemberN{Value: jsonNumber(c.Revision)}}
	update := "SET lastToken = :token REMOVE #lock"
	switch c.Operation {
	case "publish":
		if e = Validate(c.Config); e != nil {
			return nil, e
		}
		entry := Entry{ID: id, Config: c.Config, ConfigHash: Hash(c.Config), Revision: c.Revision, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		if current.Entry != nil && !current.Deleted && current.Entry.ConfigHash == entry.ConfigHash {
			entry = *current.Entry
		}
		attr, e := attributevalue.Marshal(entry)
		if e != nil {
			return nil, e
		}
		values[":entry"] = attr
		values[":deleted"] = &types.AttributeValueMemberBOOL{Value: false}
		update = "SET lastToken = :token, entry = :entry, deleted = :deleted REMOVE #lock"
	case "retire":
		values[":deleted"] = &types.AttributeValueMemberBOOL{Value: true}
		update = "SET deleted = :deleted REMOVE entry"
	case "delete":
		values[":deleted"] = &types.AttributeValueMemberBOOL{Value: true}
		update = "SET lastToken = :token, deleted = :deleted REMOVE #lock, entry"
	case "abandon":
	default:
		return nil, errors.New("unknown operation")
	}
	_, e = s.DB.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.Table), Key: key(id), ConditionExpression: aws.String("#lock = :token AND revision = :revision"), UpdateExpression: aws.String(update), ExpressionAttributeNames: map[string]string{"#lock": "lock"}, ExpressionAttributeValues: values})
	return map[string]bool{"ok": true}, e
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
func response(status int, v any) events.APIGatewayV2HTTPResponse {
	b, _ := json.Marshal(v)
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store"}, Body: string(b)}
}
func (s Service) Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.HTTP.Method == "GET" && req.RawPath == "/v1/environments" {
		entries := []Entry{}
		var start map[string]types.AttributeValue
		for {
			out, e := s.DB.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(s.Table), ExclusiveStartKey: start, ConsistentRead: aws.Bool(true), ProjectionExpression: aws.String("entry, deleted")})
			if e != nil {
				return response(503, map[string]string{"error": "registry unavailable"}), nil
			}
			for _, item := range out.Items {
				var r Record
				if e = attributevalue.UnmarshalMap(item, &r); e != nil {
					return response(503, map[string]string{"error": "invalid registry data"}), nil
				}
				if !r.Deleted && r.Entry != nil {
					entries = append(entries, *r.Entry)
				}
			}
			start = out.LastEvaluatedKey
			if len(start) == 0 {
				break
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
		return response(200, map[string]any{"schemaVersion": 1, "environments": entries}), nil
	}
	if req.RequestContext.HTTP.Method != "POST" {
		return response(404, map[string]string{"error": "not found"}), nil
	}
	parts := strings.Split(strings.Trim(req.RawPath, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "environments" || parts[3] != "changes" {
		return response(404, map[string]string{"error": "not found"}), nil
	}
	if len(req.Body) > 16384 || req.IsBase64Encoded {
		return response(400, map[string]string{"error": "invalid request"}), nil
	}
	var c Change
	decoder := json.NewDecoder(strings.NewReader(req.Body))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&c); e != nil {
		return response(400, map[string]string{"error": "invalid request"}), nil
	}
	if decoder.Decode(new(any)) != io.EOF || !ValidEnvironment(parts[2]) || !regexp.MustCompile(`^[a-f0-9]{48}$`).MatchString(c.Token) {
		return response(400, map[string]string{"error": "invalid request"}), nil
	}
	switch c.Operation {
	case "begin":
	case "publish", "retire", "delete", "abandon":
		if c.Revision < 1 {
			return response(400, map[string]string{"error": "deployment receipt required"}), nil
		}
		if c.Operation == "publish" {
			if e := Validate(c.Config); e != nil {
				return response(400, map[string]string{"error": "invalid configuration"}), nil
			}
		}
	default:
		return response(400, map[string]string{"error": "unknown operation"}), nil
	}
	result, e := s.change(ctx, parts[2], c)
	if e != nil {
		var conflict *types.ConditionalCheckFailedException
		if errors.As(e, &conflict) {
			return response(409, map[string]string{"error": "environment busy or deployment revision superseded"}), nil
		}
		return response(503, map[string]string{"error": "registry temporarily unavailable"}), nil
	}
	return response(200, result), nil
}
