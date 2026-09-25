package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/lambdawalker/go.attestra.aws.auth/api"
	"github.com/lambdawalker/go.attestra.aws.auth/cmd/internal/bootstrap"
)

func main() {
	handler := api.Handler{Service: bootstrap.Service()}
	lambda.Start(handler.Signup)
}
