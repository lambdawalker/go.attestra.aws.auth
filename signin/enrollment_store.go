package signin

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type EnrollmentDB interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// Facts contain no credentials or document details and survive evidence retention.
// Environment reset clears this table alongside proof transactions.
type FactStore struct {
	DB    EnrollmentDB
	Table string
}

func (s FactStore) Get(ctx context.Context, subject string) (EnrollmentFacts, error) {
	out, err := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.Table), Key: map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: "enrollment#" + subject}}, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return EnrollmentFacts{}, err
	}
	var facts EnrollmentFacts
	err = attributevalue.UnmarshalMap(out.Item, &facts)
	return facts, err
}
func (s FactStore) Mark(ctx context.Context, subject string, password, captured bool) error {
	expression := ""
	names := map[string]string{}
	if password {
		expression = "SET #password = :yes"
		names["#password"] = "PasswordRegistered"
	}
	if captured {
		if expression == "" {
			expression = "SET "
		} else {
			expression += ", "
		}
		expression += "#captured = :yes"
		names["#captured"] = "IDCaptured"
	}
	if expression == "" {
		return nil
	}
	_, err := s.DB.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.Table), Key: map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: "enrollment#" + subject}}, UpdateExpression: aws.String(expression), ExpressionAttributeNames: names, ExpressionAttributeValues: map[string]types.AttributeValue{":yes": &types.AttributeValueMemberBOOL{Value: true}}})
	return err
}
