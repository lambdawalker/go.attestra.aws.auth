package runtimeconfig

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Dynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}
type Store struct {
	DB    Dynamo
	Table string
}

func key(id, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}, "sk": &types.AttributeValueMemberS{Value: sk}}
}
func item(id, sk string, v any) (map[string]types.AttributeValue, error) {
	m, e := attributevalue.MarshalMap(v)
	if e == nil {
		for k, v := range key(id, sk) {
			m[k] = v
		}
	}
	return m, e
}
func (s Store) read(ctx context.Context, id, sk string, v any) error {
	r, e := s.DB.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.Table), Key: key(id, sk), ConsistentRead: aws.Bool(true)})
	if e != nil {
		return e
	}
	if len(r.Item) == 0 {
		return ErrNotFound
	}
	return attributevalue.UnmarshalMap(r.Item, v)
}
func (s Store) Get(ctx context.Context, env string) (Config, error) {
	var c Config
	e := s.read(ctx, "ENV#"+env, "CONFIG", &c)
	if e == nil {
		e = c.Validate()
		if c.Environment != env {
			e = ErrInvalid
		}
	}
	return c, e
}
func (s Store) query(ctx context.Context, id, prefix string) ([]map[string]types.AttributeValue, error) {
	r, e := s.DB.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(s.Table), ConsistentRead: aws.Bool(true), KeyConditionExpression: aws.String("id = :id AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: id}, ":prefix": &types.AttributeValueMemberS{Value: prefix}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(100)})
	if e != nil {
		return nil, e
	}
	return r.Items, nil
}
func (s Store) Grants(ctx context.Context, sub string) ([]Grant, error) {
	out := []Grant{}
	var cursor map[string]types.AttributeValue
	for {
		r, e := s.DB.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(s.Table), ConsistentRead: aws.Bool(true), KeyConditionExpression: aws.String("id = :id AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: "USER#" + sub}, ":prefix": &types.AttributeValueMemberS{Value: "GRANT#"}}, ExclusiveStartKey: cursor, Limit: aws.Int32(100)})
		if e != nil {
			return nil, e
		}
		for _, m := range r.Items {
			var g Grant
			if e := attributevalue.UnmarshalMap(m, &g); e != nil {
				return nil, e
			}
			if g.Subject != sub || !ValidEnvironment(g.Environment) || g.Revision < 1 {
				return nil, ErrInvalid
			}
			out = append(out, g)
		}
		cursor = r.LastEvaluatedKey
		if len(cursor) == 0 {
			return out, nil
		}
	}
}

func (s Store) GetVersion(ctx context.Context, env string, rev int64) (Version, error) {
	var v Version
	e := s.read(ctx, "ENV#"+env, "VERSION#"+fmtRevision(rev), &v)
	return v, e
}
func (s Store) History(ctx context.Context, env string) ([]Version, error) {
	items, e := s.query(ctx, "ENV#"+env, "VERSION#")
	out := []Version{}
	for _, m := range items {
		var v Version
		if e := attributevalue.UnmarshalMap(m, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, e
}
func revision(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}
func (s Store) grantCheck(g Grant, manage bool) *types.ConditionCheck {
	condition := "Revision = :revision AND Active = :active"
	values := map[string]types.AttributeValue{":revision": revision(g.Revision), ":active": &types.AttributeValueMemberBOOL{Value: true}}
	if manage {
		condition += " AND ManageGrants = :active"
	}
	return &types.ConditionCheck{TableName: aws.String(s.Table), Key: key("USER#"+g.Subject, "GRANT#"+g.Environment), ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}
}
func conflict(e error) error {
	var t *types.TransactionCanceledException
	if errors.As(e, &t) {
		for _, r := range t.CancellationReasons {
			if aws.ToString(r.Code) == "ConditionalCheckFailed" {
				return ErrConflict
			}
		}
	}
	return e
}
func (s Store) Publish(ctx context.Context, g Grant, expected int64, v Version) error {
	m, e := item("ENV#"+v.Config.Environment, "CONFIG", v.Config)
	if e != nil {
		return e
	}
	m["Override"] = &types.AttributeValueMemberBOOL{Value: true}
	history, e := item("ENV#"+v.Config.Environment, "VERSION#"+fmtRevision(v.Config.Revision), v)
	if e != nil {
		return e
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{ConditionCheck: s.grantCheck(g, false)}, {Put: &types.Put{TableName: aws.String(s.Table), Item: m, ConditionExpression: aws.String("Revision = :revision"), ExpressionAttributeValues: map[string]types.AttributeValue{":revision": revision(expected)}}}, {Put: &types.Put{TableName: aws.String(s.Table), Item: history, ConditionExpression: aws.String("attribute_not_exists(id)")}}}})
	return conflict(e)
}
func fmtRevision(v int64) string {
	n := strconv.FormatInt(v, 10)
	for len(n) < 20 {
		n = "0" + n
	}
	return n
}
func (s Store) SetGrant(ctx context.Context, actor, target Grant) error {
	if !actor.Active || !actor.ManageGrants || actor.Environment != target.Environment || actor.Subject == target.Subject || target.Subject == "" || len(target.Subject) > 100 {
		return ErrForbidden
	}
	expected := target.Revision
	target.Revision++
	m, e := item("USER#"+target.Subject, "GRANT#"+target.Environment, target)
	if e != nil {
		return e
	}
	condition := "Revision = :revision"
	values := map[string]types.AttributeValue{":revision": revision(expected)}
	if expected == 0 {
		condition = "attribute_not_exists(id)"
		values = nil
	}
	audit, e := item("ENV#"+target.Environment, "GRANT-AUDIT#"+target.Subject+"#"+fmtRevision(target.Revision), struct {
		Actor     string
		Grant     Grant
		UpdatedAt string
	}{actor.Subject, target, time.Now().UTC().Format(time.RFC3339Nano)})
	if e != nil {
		return e
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{ConditionCheck: s.grantCheck(actor, true)}, {Put: &types.Put{TableName: aws.String(s.Table), Item: m, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}}, {Put: &types.Put{TableName: aws.String(s.Table), Item: audit, ConditionExpression: aws.String("attribute_not_exists(id)")}}}})
	return conflict(e)
}

// Seed inserts an environment exactly once. Neither deployments nor reruns can overwrite an override.
func (s Store) Seed(ctx context.Context, c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	m, e := item("ENV#"+c.Environment, "CONFIG", c)
	if e != nil {
		return e
	}
	v, e := item("ENV#"+c.Environment, "VERSION#"+fmtRevision(0), Version{Config: c, Actor: "setup", Action: "defaults"})
	if e != nil {
		return e
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.Table), Item: m, ConditionExpression: aws.String("attribute_not_exists(id)")}}, {Put: &types.Put{TableName: aws.String(s.Table), Item: v, ConditionExpression: aws.String("attribute_not_exists(id)")}}}})
	return conflict(e)
}

// BootstrapGrant uses a permanent per-environment marker, so setup never restores a revoked grant.
func (s Store) BootstrapGrant(ctx context.Context, sub, env string) error {
	g := Grant{Subject: sub, Environment: env, Revision: 1, Active: true, ManageGrants: true}
	m, e := item("USER#"+sub, "GRANT#"+env, g)
	if e != nil {
		return e
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.Table), Item: key("ENV#"+env, "BOOTSTRAP"), ConditionExpression: aws.String("attribute_not_exists(id)")}}, {Put: &types.Put{TableName: aws.String(s.Table), Item: m, ConditionExpression: aws.String("attribute_not_exists(id)")}}}})
	return conflict(e)
}

// Fence is the shared registry lease, checked atomically with default synchronization.
type Fence struct {
	Table, Environment, Token string
	Revision                  int64
}
type storedConfig struct {
	Config
	Override          bool
	DefaultGeneration int64
}

func (s Store) SyncDefaults(ctx context.Context, c Config, f Fence) error {
	if c.Environment != f.Environment || f.Table == "" || f.Token == "" || f.Revision < 1 {
		return ErrInvalid
	}
	if e := c.Validate(); e != nil {
		return e
	}
	var old storedConfig
	e := s.read(ctx, "ENV#"+c.Environment, "CONFIG", &old)
	if e != nil && e != ErrNotFound {
		return e
	}
	// Explicit overrides are permanent until an administrator chooses new values.
	if e == nil && old.Override {
		return nil
	}
	if e == nil && old.DefaultGeneration >= f.Revision {
		if old.DefaultGeneration == f.Revision && old.Features == c.Features && old.Capture == c.Capture {
			return nil
		}
		return ErrConflict
	}
	changed := e == ErrNotFound || old.Features != c.Features || old.Capture != c.Capture
	if e == nil {
		c.Revision = old.Revision
		if changed {
			c.Revision++
		}
	} else {
		c.Revision = 0
	}
	if !changed {
		c.UpdatedAt = old.UpdatedAt
	}
	m, e := item("ENV#"+c.Environment, "CONFIG", storedConfig{Config: c, DefaultGeneration: f.Revision})
	if e != nil {
		return e
	}
	condition := "attribute_not_exists(id)"
	var values map[string]types.AttributeValue
	if old.SchemaVersion != 0 {
		condition = "Revision = :revision AND (attribute_not_exists(Override) OR Override = :false) AND (attribute_not_exists(DefaultGeneration) OR DefaultGeneration < :generation)"
		values = map[string]types.AttributeValue{":revision": revision(old.Revision), ":false": &types.AttributeValueMemberBOOL{Value: false}, ":generation": revision(f.Revision)}
	}
	writes := []types.TransactWriteItem{{ConditionCheck: &types.ConditionCheck{TableName: aws.String(f.Table), Key: map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: f.Environment}}, ConditionExpression: aws.String("revision = :revision AND #lock = :token"), ExpressionAttributeNames: map[string]string{"#lock": "lock"}, ExpressionAttributeValues: map[string]types.AttributeValue{":revision": revision(f.Revision), ":token": &types.AttributeValueMemberS{Value: f.Token}}}}, {Put: &types.Put{TableName: aws.String(s.Table), Item: m, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}}}
	if changed {
		v, e := item("ENV#"+c.Environment, "VERSION#"+fmtRevision(c.Revision), Version{Config: c, Actor: "deployment:" + strconv.FormatInt(f.Revision, 10), Action: "defaults"})
		if e != nil {
			return e
		}
		writes = append(writes, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.Table), Item: v, ConditionExpression: aws.String("attribute_not_exists(id)")}})
	}
	_, e = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	return conflict(e)
}

func (s Store) Bootstrapped(ctx context.Context, env string) (bool, error) {
	var marker map[string]any
	e := s.read(ctx, "ENV#"+env, "BOOTSTRAP", &marker)
	if errors.Is(e, ErrNotFound) {
		return false, nil
	}
	return e == nil, e
}
