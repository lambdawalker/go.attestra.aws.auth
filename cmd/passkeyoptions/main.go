package main

import (
 "context"
 "log"
 "os"
 "github.com/aws/aws-lambda-go/lambda"
 "github.com/aws/aws-sdk-go-v2/config"
 "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
 "github.com/lambdawalker/go.attestra.aws.auth/passkey"
)
func main() {
 cfg,err:=config.LoadDefaultConfig(context.Background()); if err!=nil { log.Fatal(err) }
 handler:=passkey.Handler{Service:passkey.Service{Cognito:cognitoidentityprovider.NewFromConfig(cfg),Endpoint:"https://cognito-idp."+os.Getenv("AWS_REGION")+".amazonaws.com/"}}
 lambda.Start(handler.Options)
}
