package captureapi

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "errors"
 "io"
 "strings"

 "github.com/aws/aws-lambda-go/events"
 "github.com/lambdawalker/go.attestra.aws.auth/capture"
)
type Handler struct{Service capture.Service}
func reply(status int,v any)events.APIGatewayV2HTTPResponse{b,_:=json.Marshal(v);return events.APIGatewayV2HTTPResponse{StatusCode:status,Headers:map[string]string{"content-type":"application/json","cache-control":"no-store"},Body:string(b)}}
func fail(e error)events.APIGatewayV2HTTPResponse{status,code:=503,"service_unavailable";switch{case errors.Is(e,capture.ErrInvalid):status,code=400,"invalid_request";case errors.Is(e,capture.ErrNotFound):status,code=404,"not_found";case errors.Is(e,capture.ErrConflict):status,code=409,"revision_conflict";case errors.Is(e,capture.ErrExpired):status,code=410,"capture_expired";case errors.Is(e,capture.ErrLimited):status,code=429,"capture_limit";case errors.Is(e,capture.ErrDisabled):status,code=503,"capture_disabled";case errors.Is(e,capture.ErrIncomplete):status,code=409,"uploads_incomplete";case errors.Is(e,capture.ErrImage):status,code=422,"invalid_image"};return reply(status,map[string]string{"error":code})}
func decode(req events.APIGatewayV2HTTPRequest,out any)error{body:=req.Body;if len(body)>12000{return capture.ErrInvalid};if req.IsBase64Encoded{b,e:=base64.StdEncoding.DecodeString(body);if e!=nil{return capture.ErrInvalid};body=string(b)};if len(body)>8192{return capture.ErrInvalid};d:=json.NewDecoder(strings.NewReader(body));d.DisallowUnknownFields();if d.Decode(out)!=nil{return capture.ErrInvalid};var extra any;if d.Decode(&extra)!=io.EOF{return capture.ErrInvalid};return nil}
func(h Handler)Handle(ctx context.Context,req events.APIGatewayV2HTTPRequest)(events.APIGatewayV2HTTPResponse,error){
 // Only API Gateway's verified JWT authorizer context is trusted. No body owner or decoded bearer claims.
 if req.RequestContext.Authorizer==nil||req.RequestContext.Authorizer.JWT==nil{return reply(401,map[string]string{"error":"sign_in_required"}),nil};claims:=req.RequestContext.Authorizer.JWT.Claims;owner:=claims["sub"];if owner==""||claims["token_use"]!="access"{return reply(401,map[string]string{"error":"sign_in_required"}),nil}
 method,path:=req.RequestContext.HTTP.Method,req.RawPath
 if method=="GET"&&path=="/onboarding/id/document-policy"{return reply(200,h.Service.Policy()),nil}
 if method=="GET"&&path=="/onboarding/id/status"{r,e:=h.Service.Current(ctx,owner);if e!=nil{return fail(e),nil};return reply(200,map[string]any{"capture":r}),nil}
 if method=="POST"&&path=="/onboarding/id/captures"{var in struct{OperationKey string `json:"operation_key"`;DocumentType string `json:"document_type"`};if e:=decode(req,&in);e!=nil{return fail(e),nil};r,e:=h.Service.Create(ctx,owner,in.OperationKey,in.DocumentType);if e!=nil{return fail(e),nil};return reply(200,r),nil}
 parts:=strings.Split(strings.TrimPrefix(path,"/onboarding/id/captures/"),"/");if !strings.HasPrefix(path,"/onboarding/id/captures/")||len(parts)>2||!capture.ValidID(parts[0]){return fail(capture.ErrNotFound),nil};id:=parts[0]
 if method=="GET"&&len(parts)==1{r,e:=h.Service.Get(ctx,owner,id);if e!=nil{return fail(e),nil};return reply(200,r),nil};if method!="POST"||len(parts)!=2{return fail(capture.ErrNotFound),nil}
 var r capture.Record;var e error;status:=200
 switch parts[1]{case "uploads":var in capture.UploadRequest;if e=decode(req,&in);e!=nil{return fail(e),nil};out,e:=h.Service.Upload(ctx,owner,id,in);if e!=nil{return fail(e),nil};return reply(200,out),nil
 case "finalize":var in capture.FinalizeRequest;if e=decode(req,&in);e!=nil{return fail(e),nil};r,e=h.Service.Finalize(ctx,owner,id,in);status=202
 case "cancel":var in struct{};if e=decode(req,&in);e!=nil{return fail(e),nil};r,e=h.Service.Cancel(ctx,owner,id)
 case "retry-finalization":var in struct{OperationKey string `json:"operation_key"`};if e=decode(req,&in);e!=nil{return fail(e),nil};r,e=h.Service.Retry(ctx,owner,id,in.OperationKey);status=202
 default:return fail(capture.ErrNotFound),nil};if e!=nil{return fail(e),nil};return reply(status,r),nil
}
