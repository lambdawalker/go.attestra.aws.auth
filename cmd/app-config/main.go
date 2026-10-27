package main

import (
	"context"
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
	lambda.Start(runtimeconfig.PublicHandler{Store: store, Environment: environment}.Handle)
}
