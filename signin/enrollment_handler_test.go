package signin

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"strings"
	"testing"
)

func TestEnrollmentHTTPContracts(t *testing.T) {
	s, _, cfg, _ := enrollmentFixture()
	cfg.c.Onboarding.Password = runtimeconfig.Required
	cfg.c.Onboarding.PasswordPlacement = "after_verification"
	h := Handler{Enrollment: s, Service: Service{Client: &fake{}, ClientID: "client"}, Route: "password/set"}
	req := events.APIGatewayV2HTTPRequest{RawPath: "/auth/password/set", Headers: map[string]string{"authorization": "Bearer " + testAccess(claims)}, Body: `{"password":"ValidPassword123"}`, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "POST"}}}
	out, _ := h.Serve(context.Background(), req)
	if out.StatusCode != 200 || !strings.Contains(out.Body, `"password_registered":true`) {
		t.Fatal(out)
	}
	req.Body = `{"password":"short"}`
	out, _ = h.Serve(context.Background(), req)
	if out.StatusCode != 400 || !strings.Contains(out.Body, "password_policy") {
		t.Fatal(out)
	}
	h.Route = "status"
	req.RawPath = "/auth/status"
	req.Body = "{}"
	out, _ = h.Serve(context.Background(), req)
	var status AccountStatus
	if json.Unmarshal([]byte(out.Body), &status) != nil || out.StatusCode != 200 || !status.PasswordRegistered || status.Complete {
		t.Fatal(out)
	}
	delete(req.Headers, "authorization")
	out, _ = h.Serve(context.Background(), req)
	if out.StatusCode != 401 {
		t.Fatal(out)
	}
	h.Route = "password/start"
	req.RawPath = "/auth/password/start"
	req.Body = `{"email":"user@example.com"}`
	out, _ = h.Serve(context.Background(), req)
	if out.StatusCode != 200 {
		t.Fatal(out)
	}
	h.Route = "password/complete"
	req.RawPath = "/auth/password/complete"
	req.Body = `{"email":"user@example.com","session":"challenge-session","password":"original password"}`
	out, _ = h.Serve(context.Background(), req)
	if out.StatusCode != 200 {
		t.Fatal(out)
	}
}

type factsDB struct {
	item    map[string]types.AttributeValue
	read    *dynamodb.GetItemInput
	updates []*dynamodb.UpdateItemInput
}

func (f *factsDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.read = in
	return &dynamodb.GetItemOutput{Item: f.item}, nil
}
func (f *factsDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updates = append(f.updates, in)
	return &dynamodb.UpdateItemOutput{}, nil
}
func TestEnrollmentFactsUseMonotonicUpdatesWithoutSecrets(t *testing.T) {
	db := &factsDB{item: map[string]types.AttributeValue{"PasswordRegistered": &types.AttributeValueMemberBOOL{Value: true}}}
	s := FactStore{DB: db, Table: "proofs"}
	ctx := context.Background()
	facts, err := s.Get(ctx, "subject")
	if err != nil || !facts.PasswordRegistered || facts.IDCaptured || !aws.ToBool(db.read.ConsistentRead) {
		t.Fatal(facts, err)
	}
	if err = s.Mark(ctx, "subject", false, true); err != nil {
		t.Fatal(err)
	}
	update := db.updates[0]
	if aws.ToString(update.UpdateExpression) != "SET #captured = :yes" || len(update.ExpressionAttributeValues) != 1 || update.Key["id"].(*types.AttributeValueMemberS).Value != "enrollment#subject" {
		t.Fatal(update)
	}
}
