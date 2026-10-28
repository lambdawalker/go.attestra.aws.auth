package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lambdawalker/go.attestra.aws.auth/awscapture"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"github.com/lambdawalker/go.attestra.aws.auth/signin"
)

func main() {
	cfg, e := config.LoadDefaultConfig(context.Background())
	if e != nil {
		log.Fatal(e)
	}
	clientID, route := os.Getenv("CLIENT_ID"), os.Getenv("AUTH_ROUTE")
	if clientID == "" || route == "" {
		log.Fatal("missing auth config")
	}
	h := signin.Handler{Service: signin.Service{Client: cognitoidentityprovider.NewFromConfig(cfg), ClientID: clientID}, Route: route}
	if route == "status" || route == "password/set" {
		db := dynamodb.NewFromConfig(cfg)
		enrollment := &signin.Enrollment{Identity: cognitoidentityprovider.NewFromConfig(cfg), Config: runtimeconfig.Store{DB: db, Table: os.Getenv("RUNTIME_TABLE")}, Facts: signin.FactStore{DB: db, Table: os.Getenv("TABLE_NAME")}, Environment: os.Getenv("ENVIRONMENT"), PoolID: os.Getenv("POOL_ID"), ClientID: clientID, Issuer: os.Getenv("ISSUER")}
		if enrollment.Environment == "" || enrollment.PoolID == "" || enrollment.Issuer == "" || os.Getenv("RUNTIME_TABLE") == "" || os.Getenv("TABLE_NAME") == "" {
			log.Fatal("missing enrollment config")
		}
		if route == "status" {
			enrollment.Captures = &awscapture.Store{DB: db, Table: os.Getenv("CAPTURE_TABLE")}
		}
		h.Enrollment = enrollment
	}
	lambda.Start(h.Serve)
}
