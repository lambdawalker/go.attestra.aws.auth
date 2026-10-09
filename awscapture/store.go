// Package awscapture supplies persistence and private AWS transports for capture.
package awscapture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
)

type Store struct {
	DB    *dynamodb.Client
	Table string
}

func ownerKey(owner string) string {
	h := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(h[:])
}
func recordKey(owner, id string) string { return "CAP#" + ownerKey(owner) + "#" + id }
func key(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}}
}
func (s *Store) get(ctx context.Context, id string, out any) error {
	r, e := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.Table), Key: key(id), ConsistentRead: aws.Bool(true)})
	if e != nil {
		return e
	}
	if len(r.Item) == 0 {
		return capture.ErrNotFound
	}
	return attributevalue.UnmarshalMap(r.Item, out)
}
func (s *Store) Account(ctx context.Context, owner string) (capture.Account, error) {
	a := capture.Account{Owner: owner}
	e := s.get(ctx, "ACCOUNT#"+ownerKey(owner), &a)
	if e == capture.ErrNotFound {
		e = nil
	}
	return a, e
}
func (s *Store) Get(ctx context.Context, owner, id string) (capture.Record, error) {
	var r capture.Record
	e := s.get(ctx, recordKey(owner, id), &r)
	if e == nil && r.Owner != owner {
		return capture.Record{}, capture.ErrNotFound
	}
	return r, e
}
func (s *Store) FindCreate(ctx context.Context, owner, operation string) (capture.Record, error) {
	var mapping struct{ CaptureID string }
	e := s.get(ctx, "CREATE#"+ownerKey(owner)+"#"+operation, &mapping)
	if e != nil {
		return capture.Record{}, e
	}
	return s.Get(ctx, owner, mapping.CaptureID)
}
func item(id string, v any) (map[string]types.AttributeValue, error) {
	m, e := attributevalue.MarshalMap(v)
	if e != nil {
		return nil, e
	}
	m["id"] = &types.AttributeValueMemberS{Value: id}
	return m, nil
}
func recordItem(r capture.Record) (map[string]types.AttributeValue, error) {
	m, e := item(recordKey(r.Owner, r.ID), r)
	if e != nil {
		return nil, e
	}
	if r.Work != "" {
		m["work"] = &types.AttributeValueMemberS{Value: r.Work}
		m["due"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(r.Due, 10)}
	}
	if r.Work == "" {
		m["ttl"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(r.TTL, 10)}
	}
	return m, nil
}
func conditional(e error) error {
	var conflict *types.TransactionCanceledException
	var single *types.ConditionalCheckFailedException
	if errors.As(e, &single) {
		return capture.ErrConflict
	}
	if errors.As(e, &conflict) {
		for _, reason := range conflict.CancellationReasons {
			if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
				return capture.ErrConflict
			}
		}
	}
	return e
}
func (s *Store) Create(ctx context.Context, before, after capture.Account, r capture.Record) error {
	a, e := item("ACCOUNT#"+ownerKey(r.Owner), after)
	if e != nil {
		return e
	}
	c, e := recordItem(r)
	if e != nil {
		return e
	}
	mapping := key("CREATE#" + ownerKey(r.Owner) + "#" + r.CreateKey)
	mapping["CaptureID"] = &types.AttributeValueMemberS{Value: r.ID}
	mapping["ttl"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(r.TTL, 10)}
	condition := "Revision = :revision"
	values := map[string]types.AttributeValue{":revision": &types.AttributeValueMemberN{Value: strconv.FormatInt(before.Revision, 10)}}
	if before.Revision == 0 {
		condition = "attribute_not_exists(id)"
		values = nil
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.Table), Item: a, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}},
		{Put: &types.Put{TableName: aws.String(s.Table), Item: c, ConditionExpression: aws.String("attribute_not_exists(id)")}},
		{Put: &types.Put{TableName: aws.String(s.Table), Item: mapping, ConditionExpression: aws.String("attribute_not_exists(id)")}},
	}})
	return conditional(e)
}
func (s *Store) Swap(ctx context.Context, before, after capture.Record, ready bool) error {
	m, e := recordItem(after)
	if e != nil {
		return e
	}
	writes := []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.Table), Item: m, ConditionExpression: aws.String("Revision = :revision"), ExpressionAttributeValues: map[string]types.AttributeValue{":revision": &types.AttributeValueMemberN{Value: strconv.FormatInt(before.Revision, 10)}}}}}
	if ready {
		event, e := item("READY#"+ownerKey(after.Owner)+"#"+after.ID, struct {
			Kind     string
			Manifest capture.Record
		}{"capture.ready", after})
		if e != nil {
			return e
		}
		event["ttl"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(after.TTL, 10)}
		writes = append(writes, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.Table), Item: event, ConditionExpression: aws.String("attribute_not_exists(id)")}})
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	return conditional(e)
}

// Pending is the durable-intent sweep: the record and work index update atomically.
// A lost queue send/ack cannot lose intent. Duplicate deliveries are fenced by Work.
func (s *Store) Pending(ctx context.Context, now int64, send func(capture.Job) error) error {
	for _, work := range []string{"validate", "cleanup"} {
		var cursor map[string]types.AttributeValue
		for pages := 0; pages < 10; pages++ {
			out, e := s.DB.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(s.Table), IndexName: aws.String("work-due"), KeyConditionExpression: aws.String("#work = :work AND due <= :now"), ExpressionAttributeNames: map[string]string{"#work": "work"}, ExpressionAttributeValues: map[string]types.AttributeValue{":work": &types.AttributeValueMemberS{Value: work}, ":now": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)}}, ExclusiveStartKey: cursor, Limit: aws.Int32(100)})
			if e != nil {
				return e
			}
			for _, m := range out.Items {
				var r capture.Record
				if e = attributevalue.UnmarshalMap(m, &r); e != nil {
					return e
				}
				if e = send(capture.Job{Owner: r.Owner, ID: r.ID, Generation: r.Generation, Work: r.Work}); e != nil {
					return e
				}
			}
			cursor = out.LastEvaluatedKey
			if len(cursor) == 0 {
				break
			}
		}
	}
	return nil
}
