package bootstrap

import (
	"context"
	"encoding/base64"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/lambdawalker/go.attestra.aws.auth/awsemail"
	"github.com/lambdawalker/go.attestra.aws.auth/awsidentity"
	"github.com/lambdawalker/go.attestra.aws.auth/awsstore"
	"github.com/lambdawalker/go.attestra.aws.auth/email"
)

// Service configures the common proof store and identity clients for each API Lambda.
func Service() *email.Service {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("PROOF_KEY"))
	if err != nil || len(key) < 32 {
		log.Fatal("invalid PROOF_KEY")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal("AWS config unavailable")
	}
	table := os.Getenv("TABLE_NAME")
	pool := os.Getenv("POOL_ID")
	client := os.Getenv("CLIENT_ID")
	origin := os.Getenv("APP_ORIGIN")
	sender := os.Getenv("SENDER_ADDRESS")
	if table == "" || pool == "" || client == "" || origin == "" || sender == "" {
		log.Fatal("missing required configuration")
	}
	db := dynamodb.NewFromConfig(cfg)
	return &email.Service{
		Store:    awsstore.Store{DB: db, Table: table},
		Identity: awsidentity.Identity{Cognito: cognitoidentityprovider.NewFromConfig(cfg), DB: db, Table: table, PoolID: pool, ClientID: client},
		Sender:   awsemail.Sender{Client: sesv2.NewFromConfig(cfg), From: sender},
		Key:      key,
		Origin:   origin,
		Diagnostics: os.Getenv("DIAGNOSTIC_MODE") == "true",
	}
}
