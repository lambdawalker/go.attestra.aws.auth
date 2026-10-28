package main

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lambdawalker/go.attestra.aws.auth/envreset"
	"github.com/lambdawalker/go.attestra.aws.auth/internal/awshttp"
	"log"
	"os"
	"time"
)

type response struct {
	Status *envreset.Status `json:"status,omitempty"`
	Error  string           `json:"error,omitempty"`
}

func main() {
	cfg, e := config.LoadDefaultConfig(context.Background())
	if e != nil {
		log.Fatal("reset configuration unavailable")
	}
	var manifest envreset.Manifest
	if json.Unmarshal([]byte(os.Getenv("RESET_MANIFEST")), &manifest) != nil || len(manifest.Functions) == 0 || len(manifest.Tables) != 2 || manifest.Pool == "" || manifest.Bucket == "" || len(manifest.Queues) != 2 || os.Getenv("RESET_TABLE") == "" {
		log.Fatal("invalid reset inventory")
	}
	db := dynamodb.NewFromConfig(cfg)
	s := envreset.Service{Environment: os.Getenv("ENVIRONMENT"), Enabled: os.Getenv("RESET_ENABLED") == "true", Store: envreset.DynamoStore{DB: db, Table: os.Getenv("RESET_TABLE")}, Backend: envreset.AWSBackend{Manifest: manifest, DB: db, Cognito: cognitoidentityprovider.NewFromConfig(cfg), HTTP: awshttp.Client{Config: cfg}}}
	lambda.Start(func(ctx context.Context, q envreset.Request) (response, error) {
		var status envreset.Status
		var e error
		switch q.Action {
		case "status":
			status, e = s.Status(ctx)
		case "start":
			status, e = s.Start(ctx, q)
		case "resume":
			status, e = s.Resume(ctx, q)
		case "work":
			until := time.Now().Add(35 * time.Second)
			for time.Now().Before(until) {
				if e = s.Work(ctx); e != nil {
					return response{}, e
				}
				v, err := s.Status(ctx)
				if err != nil {
					return response{}, err
				}
				if v.State.Phase == "" || v.State.Phase == "complete" || v.State.Error != "" || v.State.Due > time.Now().Unix() {
					break
				}
			}
			return response{}, nil
		default:
			e = envreset.ErrInvalid
		}
		if e != nil {
			code := "reset_unavailable"
			if e == envreset.ErrInvalid || e == envreset.ErrConflict || e == envreset.ErrDisabled {
				code = e.Error()
			}
			return response{Error: code}, nil
		}
		return response{Status: &status}, nil
	})
}
