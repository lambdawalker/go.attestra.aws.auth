package main

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"log"
	"os"
)

func main() {
	cfg, e := config.LoadDefaultConfig(context.Background())
	if e != nil {
		log.Fatal(e)
	}
	store := runtimeconfig.Store{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("RUNTIME_TABLE")}
	environment := os.Getenv("ENVIRONMENT")
	if store.Table == "" || !runtimeconfig.ValidEnvironment(environment) {
		log.Fatal("missing runtime configuration")
	}
	lambda.Start(func(ctx context.Context, _ events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		c, e := store.Get(ctx, environment)
		if e != nil {
			return events.APIGatewayV2HTTPResponse{StatusCode: 503, Headers: map[string]string{"cache-control": "no-store"}, Body: `{"error":"config_unavailable"}`}, nil
		}
		b, _ := json.Marshal(c)
		return events.APIGatewayV2HTTPResponse{StatusCode: 200, Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store", "x-content-type-options": "nosniff"}, Body: string(b)}, nil
	})
}
