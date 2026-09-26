package main

import (
 "context"
 "log"
 "os"

 "github.com/aws/aws-lambda-go/lambda"
 "github.com/aws/aws-sdk-go-v2/config"
 "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
 "github.com/lambdawalker/go.attestra.aws.auth/signin"
)
func main(){
 cfg,e:=config.LoadDefaultConfig(context.Background());if e!=nil{log.Fatal(e)}
 clientID,route:=os.Getenv("CLIENT_ID"),os.Getenv("AUTH_ROUTE");if clientID==""||route==""{log.Fatal("missing auth config")}
 h:=signin.Handler{Service:signin.Service{Client:cognitoidentityprovider.NewFromConfig(cfg),ClientID:clientID},Route:route}
 lambda.Start(h.Serve)
}
