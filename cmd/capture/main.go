package main
import("context";"log";"github.com/aws/aws-lambda-go/lambda";"github.com/lambdawalker/go.attestra.aws.auth/awscapture";"github.com/lambdawalker/go.attestra.aws.auth/captureapi")
func main(){s,_,_,e:=awscapture.New(context.Background());if e!=nil{log.Fatal("capture initialization failed")};lambda.Start(captureapi.Handler{Service:s}.Handle)}
