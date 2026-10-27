package main

import (
	"context"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lambdawalker/go.attestra.aws.auth/registry"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"log"
	"os"
)

func main() {
	cfg, e := config.LoadDefaultConfig(context.Background())
	if e != nil {
		log.Fatal("cannot load AWS configuration")
	}
	table := os.Getenv("REGISTRY_TABLE")
	if table == "" {
		log.Fatal("REGISTRY_TABLE missing")
	}
	db := dynamodb.NewFromConfig(cfg)
	service := registry.Service{DB: db, Table: table, Runtime: &runtimeconfig.Store{DB: db, Table: os.Getenv("RUNTIME_TABLE")}}
	lambda.Start(service.Handle)
}
