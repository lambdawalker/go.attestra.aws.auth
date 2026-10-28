package envreset

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"strconv"
)

type DynamoStore struct {
	DB    *dynamodb.Client
	Table string
}

func (s DynamoStore) Load(ctx context.Context) (State, error) {
	out, e := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.Table), Key: map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: "CONTROL"}}, ConsistentRead: aws.Bool(true)})
	if e != nil {
		return State{}, e
	}
	var r State
	e = attributevalue.UnmarshalMap(out.Item, &r)
	return r, e
}
func (s DynamoStore) Save(ctx context.Context, old, next State) error {
	m, e := attributevalue.MarshalMap(next)
	if e != nil {
		return e
	}
	m["id"] = &types.AttributeValueMemberS{Value: "CONTROL"}
	condition := "Revision = :revision"
	values := map[string]types.AttributeValue{":revision": &types.AttributeValueMemberN{Value: strconv.FormatInt(old.Revision, 10)}}
	if old.Revision == 0 {
		condition = "attribute_not_exists(id)"
		values = nil
	}
	// Keep an audit copy per operation without putting application data in this table.
	audit := map[string]types.AttributeValue{}
	for k, v := range m {
		audit[k] = v
	}
	audit["id"] = &types.AttributeValueMemberS{Value: "RESET#" + next.Operation}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.Table), Item: m, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}},
		{Put: &types.Put{TableName: aws.String(s.Table), Item: audit}},
	}})
	var conflict *types.TransactionCanceledException
	if errors.As(e, &conflict) {
		for _, r := range conflict.CancellationReasons {
			if aws.ToString(r.Code) == "ConditionalCheckFailed" {
				return ErrConflict
			}
		}
	}
	return e
}

// CheckToken also blocks the small window before the reset worker pauses Lambdas.
func (s DynamoStore) CheckToken(ctx context.Context, iat int64) (bool, error) {
	r, e := s.Load(ctx)
	if e != nil {
		return false, e
	}
	return (r.Phase == "" || r.Phase == "complete") && (r.ValidAfter == 0 || iat > r.ValidAfter), nil
}
