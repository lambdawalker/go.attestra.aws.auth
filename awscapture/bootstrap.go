package awscapture

import (
 "context"
 "errors"
 "os"
 "time"

 "github.com/aws/aws-sdk-go-v2/config"
 "github.com/aws/aws-sdk-go-v2/service/dynamodb"
 "github.com/lambdawalker/go.attestra.aws.auth/capture"
)
func New(ctx context.Context)(capture.Service,*Store,*Transport,error){cfg,e:=config.LoadDefaultConfig(ctx);if e!=nil{return capture.Service{},nil,nil,e};table,bucket:=os.Getenv("CAPTURE_TABLE"),os.Getenv("CAPTURE_BUCKET");if table==""||bucket==""||cfg.Region==""{return capture.Service{},nil,nil,errors.New("missing capture configuration")};store:=&Store{DB:dynamodb.NewFromConfig(cfg),Table:table};transport:=&Transport{Config:cfg,Bucket:bucket,QueueURL:os.Getenv("CAPTURE_QUEUE_URL")};s:=capture.Service{Store:store,Objects:transport,Enabled:os.Getenv("CAPTURE_ENABLED")=="true",DocumentType:os.Getenv("CAPTURE_DOCUMENT_TYPE"),Purpose:os.Getenv("CAPTURE_PURPOSE"),Jurisdiction:os.Getenv("CAPTURE_JURISDICTION"),Retention:7*24*time.Hour};return s,store,transport,nil}
