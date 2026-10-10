package registry

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// This in-memory store enforces the same conditional writes as DynamoDB. It
// intentionally executes each operation under a mutex, like a single-item write.
type memoryDB struct {
	mu      sync.Mutex
	records map[string]Record
}

func (db *memoryDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	item, _ := attributevalue.MarshalMap(db.records[in.Key["id"].(*types.AttributeValueMemberS).Value])
	return &dynamodb.GetItemOutput{Item: item}, nil
}
func (db *memoryDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	id := in.Key["id"].(*types.AttributeValueMemberS).Value
	r := db.records[id]
	r.ID = id
	v := in.ExpressionAttributeValues
	token := v[":token"].(*types.AttributeValueMemberS).Value
	if strings.Contains(*in.ConditionExpression, "attribute_not_exists") {
		if r.Lock != "" || r.LastToken == token {
			return nil, &types.ConditionalCheckFailedException{}
		}
		r.Revision++
		r.Lock = token
	} else {
		if r.Lock != token || jsonNumber(r.Revision) != v[":revision"].(*types.AttributeValueMemberN).Value {
			return nil, &types.ConditionalCheckFailedException{}
		}
		if strings.Contains(*in.UpdateExpression, "#lock") {
			r.Lock = ""
			r.LastToken = token
		}
		if entry, ok := v[":entry"]; ok {
			r.Entry = &Entry{}
			_ = attributevalue.Unmarshal(entry, r.Entry)
		}
		if deleted, ok := v[":deleted"]; ok {
			r.Deleted = deleted.(*types.AttributeValueMemberBOOL).Value
			if r.Deleted {
				r.Entry = nil
			}
		}
	}
	db.records[id] = r
	attrs, _ := attributevalue.MarshalMap(r)
	return &dynamodb.UpdateItemOutput{Attributes: attrs}, nil
}
func (db *memoryDB) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := &dynamodb.ScanOutput{}
	for _, r := range db.records {
		m, _ := attributevalue.MarshalMap(r)
		out.Items = append(out.Items, m)
	}
	return out, nil
}
func validConfig() Configuration {
	return Configuration{APIURL: "https://dev.api.example.com", AWSRegion: "us-east-2", CognitoUserPoolID: "us-east-2_pool", CognitoClientID: "client123"}
}
func TestConcurrentDeploymentAndDeletion(t *testing.T) {
	s := Service{DB: &memoryDB{records: map[string]Record{}}, Table: "test"}
	ctx := context.Background()
	token := strings.Repeat("a", 48)
	first, e := s.change(ctx, "dev", Change{Operation: "begin", Token: token})
	if e != nil {
		t.Fatal(e)
	}
	receipt := first.(Receipt)
	retry, e := s.change(ctx, "dev", Change{Operation: "begin", Token: token})
	if e != nil || retry != first {
		t.Fatalf("begin retry: %v %v", retry, e)
	}
	if _, e = s.change(ctx, "dev", Change{Operation: "begin", Token: strings.Repeat("b", 48)}); e == nil {
		t.Fatal("overlapping deployment acquired dev")
	}
	if _, e = s.change(ctx, "qa", Change{Operation: "begin", Token: strings.Repeat("b", 48)}); e != nil {
		t.Fatal("independent qa blocked", e)
	}
	publish := Change{Operation: "publish", Token: token, Revision: receipt.Revision, Config: validConfig()}
	for i := 0; i < 2; i++ {
		if _, e = s.change(ctx, "dev", publish); e != nil {
			t.Fatal(e)
		}
	}
	next, e := s.change(ctx, "dev", Change{Operation: "begin", Token: strings.Repeat("c", 48)})
	if e != nil {
		t.Fatal(e)
	}
	second := next.(Receipt)
	if _, e = s.change(ctx, "dev", publish); e == nil {
		t.Fatal("stale writer accepted")
	}
	del := Change{Operation: "delete", Token: second.Token, Revision: second.Revision}
	for i := 0; i < 2; i++ {
		if _, e = s.change(ctx, "dev", del); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.change(ctx, "dev", publish); e == nil {
		t.Fatal("late publish resurrected deleted environment")
	}
	res, _ := s.Handle(ctx, events.APIGatewayV2HTTPRequest{RawPath: "/v1/environments", RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"}}})
	if res.StatusCode != 200 || !strings.Contains(res.Body, `"environments":[]`) || strings.Contains(res.Body, token) {
		t.Fatalf("public response leaked state: %s", res.Body)
	}
}
func TestUnchangedConfigurationPreservesPublicRevision(t *testing.T) {
	db := &memoryDB{records: map[string]Record{}}
	s := Service{DB: db}
	for _, letter := range []string{"a", "b"} {
		v, e := s.change(context.Background(), "dev", Change{Operation: "begin", Token: strings.Repeat(letter, 48)})
		if e != nil {
			t.Fatal(e)
		}
		r := v.(Receipt)
		if _, e = s.change(context.Background(), "dev", Change{Operation: "publish", Token: r.Token, Revision: r.Revision, Config: validConfig()}); e != nil {
			t.Fatal(e)
		}
	}
	if db.records["dev"].Revision != 2 || db.records["dev"].Entry.Revision != 1 {
		t.Fatal("identical config rewrote public revision")
	}
	data, _ := json.Marshal(db.records["dev"].Entry)
	if strings.Contains(string(data), "Token") || strings.Contains(string(data), "lock") {
		t.Fatal("internal state exposed")
	}
}
func TestOnlyOneConcurrentBeginWins(t *testing.T) {
	s := Service{DB: &memoryDB{records: map[string]Record{}}}
	var wg sync.WaitGroup
	wins := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.change(context.Background(), "dev", Change{Operation: "begin", Token: strings.Repeat(string(rune('a'+i%6)), 47) + string(rune('0'+i))})
			wins <- e == nil
		}(i)
	}
	wg.Wait()
	close(wins)
	count := 0
	for won := range wins {
		if won {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d winners", count)
	}
}
func TestMalformedRequestDoesNotMutate(t *testing.T) {
	for _, body := range []string{`{"operation":"begin","token":"bad"}`, `{"operation":"delete","revision":0}`, `{"operation":"begin","token":"` + strings.Repeat("a", 48) + `"} {}`, `{"operation":"unknown"}`, `{"operation":"begin","secret":"x"}`} {
		s := Service{}
		res, _ := s.Handle(context.Background(), events.APIGatewayV2HTTPRequest{RawPath: "/v1/environments/dev/changes", Body: body, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"}}})
		if res.StatusCode != 400 {
			t.Fatalf("%s: %d", body, res.StatusCode)
		}
	}
}
func TestSameTokenConcurrentBeginIsIdempotent(t *testing.T) {
	s := Service{DB: &memoryDB{records: map[string]Record{}}}
	token := strings.Repeat("a", 48)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.change(context.Background(), "dev", Change{Operation: "begin", Token: token})
			if e != nil {
				t.Error(e)
				return
			}
			if r.(Receipt).Revision != 1 {
				t.Error("replay incremented revision")
			}
		}()
	}
	wg.Wait()
	if _, e := s.change(context.Background(), "dev", Change{Operation: "abandon", Token: token, Revision: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.change(context.Background(), "dev", Change{Operation: "begin", Token: token}); e == nil {
		t.Fatal("completed token acquired a new lock")
	}
}
func TestTeardownRetirementKeepsLockUntilCleanup(t *testing.T) {
	s := Service{DB: &memoryDB{records: map[string]Record{}}}
	ctx := context.Background()
	token := strings.Repeat("a", 48)
	value, e := s.change(ctx, "qa", Change{Operation: "begin", Token: token})
	if e != nil {
		t.Fatal(e)
	}
	r := value.(Receipt)
	for i := 0; i < 2; i++ {
		if _, e = s.change(ctx, "qa", Change{Operation: "retire", Token: token, Revision: r.Revision}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.change(ctx, "qa", Change{Operation: "begin", Token: strings.Repeat("b", 48)}); e == nil {
		t.Fatal("new setup overlapped teardown cleanup")
	}
	record, e := s.get(ctx, "qa")
	if e != nil || !record.Deleted || record.Lock != token {
		t.Fatal("retirement did not retain fence", record, e)
	}
	if _, e = s.change(ctx, "qa", Change{Operation: "delete", Token: token, Revision: r.Revision}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.change(ctx, "qa", Change{Operation: "begin", Token: strings.Repeat("b", 48)}); e != nil {
		t.Fatal("completed teardown blocked new setup", e)
	}
}
