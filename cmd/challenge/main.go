package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lambdawalker/go.attestra.aws.auth/challenge"
)

func main() {
	table := os.Getenv("TABLE_NAME")
	if table == "" {
		log.Fatal("missing TABLE_NAME")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal("AWS config unavailable")
	}
	h := challenge.Handler{DB: dynamodb.NewFromConfig(cfg), Table: table}
	lambda.Start(func(ctx context.Context, event json.RawMessage) (challenge.Event, error) { return h.Handle(ctx, event) })
}
