package runtimeconfig

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"testing"
)

type fakeDynamo struct {
	read         map[string]types.AttributeValue
	transactions []*dynamodb.TransactWriteItemsInput
	err          error
}

func (f *fakeDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if !aws.ToBool(in.ConsistentRead) {
		panic("runtime read must be strongly consistent")
	}
	return &dynamodb.GetItemOutput{Item: f.read}, nil
}
func (f *fakeDynamo) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}
func (f *fakeDynamo) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.transactions = append(f.transactions, in)
	return &dynamodb.TransactWriteItemsOutput{}, f.err
}
func TestPublishHasAtomicGrantRevisionAndImmutableAudit(t *testing.T) {
	db := &fakeDynamo{}
	s := Store{DB: db, Table: "runtime"}
	c := validConfig()
	c.Revision = 4
	g := Grant{Subject: "admin", Environment: "dev", Revision: 9, Active: true}
	if e := s.Publish(context.Background(), g, 3, Version{Config: c, Actor: "admin", Action: "publish"}); e != nil {
		t.Fatal(e)
	}
	writes := db.transactions[0].TransactItems
	if len(writes) != 3 {
		t.Fatal(writes)
	}
	check := writes[0].ConditionCheck
	if aws.ToString(check.ConditionExpression) != "Revision = :revision AND Active = :active" || check.ExpressionAttributeValues[":revision"].(*types.AttributeValueMemberN).Value != "9" || !check.ExpressionAttributeValues[":active"].(*types.AttributeValueMemberBOOL).Value {
		t.Fatal("grant revocation not fenced")
	}
	if aws.ToString(writes[1].Put.ConditionExpression) != "Revision = :revision" || writes[1].Put.ExpressionAttributeValues[":revision"].(*types.AttributeValueMemberN).Value != "3" {
		t.Fatal("config optimistic concurrency absent")
	}
	if !writes[1].Put.Item["Override"].(*types.AttributeValueMemberBOOL).Value || aws.ToString(writes[2].Put.ConditionExpression) != "attribute_not_exists(id)" {
		t.Fatal("override or immutable audit missing")
	}
	db.err = &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}
	if e := s.Publish(context.Background(), g, 3, Version{Config: c}); e != ErrConflict {
		t.Fatal(e)
	}
}
func TestDefaultsFencedAgainstStaleDeploymentAndAdminOverride(t *testing.T) {
	ctx := context.Background()
	db := &fakeDynamo{}
	s := Store{DB: db, Table: "runtime"}
	c := validConfig()
	f := Fence{Table: "registry", Environment: "dev", Token: "lease", Revision: 10}
	if e := s.SyncDefaults(ctx, c, f); e != nil {
		t.Fatal(e)
	}
	writes := db.transactions[0].TransactItems
	if len(writes) != 3 || aws.ToString(writes[0].ConditionCheck.TableName) != "registry" || aws.ToString(writes[0].ConditionCheck.ConditionExpression) != "revision = :revision AND #lock = :token" {
		t.Fatal("deployment lease not in same transaction")
	}
	var created storedConfig
	if e := attributevalue.UnmarshalMap(writes[1].Put.Item, &created); e != nil {
		t.Fatal(e)
	}
	if created.Revision != 0 || created.DefaultGeneration != 10 || created.Override {
		t.Fatal(created)
	}
	db.read = writes[1].Put.Item
	db.transactions = nil
	if e := s.SyncDefaults(ctx, c, f); e != nil || len(db.transactions) != 0 {
		t.Fatal("replay wrote extra revision", e)
	}
	f.Revision = 9
	if e := s.SyncDefaults(ctx, c, f); e != ErrConflict {
		t.Fatal("old deployment accepted", e)
	}
	f.Revision = 11
	c.Capture.Purpose = "New deployment default"
	if e := s.SyncDefaults(ctx, c, f); e != nil {
		t.Fatal(e)
	}
	writes = db.transactions[0].TransactItems
	if aws.ToString(writes[1].Put.ConditionExpression) != "Revision = :revision AND (attribute_not_exists(Override) OR Override = :false) AND (attribute_not_exists(DefaultGeneration) OR DefaultGeneration < :generation)" {
		t.Fatal("racing override unprotected")
	}
	var next storedConfig
	attributevalue.UnmarshalMap(writes[1].Put.Item, &next)
	if next.Revision != 1 {
		t.Fatal("changed defaults didn't append revision")
	}
	next.Override = true
	db.read, _ = attributevalue.MarshalMap(next)
	db.transactions = nil
	f.Revision = 12
	if e := s.SyncDefaults(ctx, validConfig(), f); e != nil || len(db.transactions) != 0 {
		t.Fatal("deployment overwrote administrator values", e)
	}
}
func TestGrantWritesPreserveRevocationsAndRequireManager(t *testing.T) {
	db := &fakeDynamo{}
	s := Store{DB: db, Table: "runtime"}
	actor := Grant{Subject: "owner", Environment: "dev", Revision: 3, Active: true, ManageGrants: true}
	target := Grant{Subject: "other", Environment: "dev", Revision: 8, Active: false}
	if e := s.SetGrant(context.Background(), actor, target); e != nil {
		t.Fatal(e)
	}
	writes := db.transactions[0].TransactItems
	if aws.ToString(writes[0].ConditionCheck.ConditionExpression) != "Revision = :revision AND Active = :active AND ManageGrants = :active" {
		t.Fatal("manager permission not transactional")
	}
	var stored Grant
	attributevalue.UnmarshalMap(writes[1].Put.Item, &stored)
	if stored.Active || stored.Revision != 9 {
		t.Fatal("revocation wasn't persisted")
	}
	actor.ManageGrants = false
	if e := s.SetGrant(context.Background(), actor, target); e != ErrForbidden {
		t.Fatal("non-manager changed grant")
	}
	if e := s.BootstrapGrant(context.Background(), "first", "dev"); e != nil {
		t.Fatal(e)
	}
	bootstrap := db.transactions[len(db.transactions)-1].TransactItems
	if aws.ToString(bootstrap[0].Put.ConditionExpression) != "attribute_not_exists(id)" || bootstrap[0].Put.Item["sk"].(*types.AttributeValueMemberS).Value != "BOOTSTRAP" {
		t.Fatal("setup could restore revoked grant")
	}
}
