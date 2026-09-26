package signin

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "errors"
 "log"
 "strings"

 "github.com/aws/aws-lambda-go/events"
)

type Handler struct { Service Service; Route string }
func respond(req events.APIGatewayV2HTTPRequest,code int,v any)(events.APIGatewayV2HTTPResponse,error){
 b,_:=json.Marshal(v)
 log.Printf("endpoint=%s status=%d trace_id=%s",req.RawPath,code,req.RequestContext.RequestID)
 return events.APIGatewayV2HTTPResponse{StatusCode:code,Headers:map[string]string{"content-type":"application/json; charset=utf-8","cache-control":"no-store","x-request-id":req.RequestContext.RequestID},Body:string(b)},nil
}
func failure(req events.APIGatewayV2HTTPRequest,err error)(events.APIGatewayV2HTTPResponse,error){
 if errors.Is(err,ErrUnauthenticated){return respond(req,401,map[string]string{"error":"sign_in_required"})}
 if errors.Is(err,ErrRejected){return respond(req,400,map[string]string{"error":"authentication_failed"})}
 return respond(req,503,map[string]string{"error":"temporarily_unavailable"})
}
func (h Handler) Serve(ctx context.Context,req events.APIGatewayV2HTTPRequest)(events.APIGatewayV2HTTPResponse,error){
 if req.RequestContext.HTTP.Method!="POST"||req.RawPath!="/auth/"+h.Route {return respond(req,404,map[string]string{"error":"not_found"})}
 if len(req.Body)>48000 {return failure(req,ErrRejected)}
 body:=req.Body
 if req.IsBase64Encoded {b,e:=base64.StdEncoding.DecodeString(body);if e!=nil||len(b)>36000{return failure(req,ErrRejected)};body=string(b)}
 var v struct {Email string `json:"email"`; Session string `json:"session"`; Code string `json:"code"`; Credential json.RawMessage `json:"credential"`; RefreshToken string `json:"refresh_token"`}
 if body!=""&&json.Unmarshal([]byte(body),&v)!=nil{return failure(req,ErrRejected)}
 switch h.Route {
 case "email/start", "passkey/start":
  method:="EMAIL_OTP";if h.Route=="passkey/start"{method="WEB_AUTHN"}
  result,e:=h.Service.Start(ctx,v.Email,method);if e!=nil{return failure(req,e)}
  return respond(req,200,result)
 case "email/complete","passkey/complete":
  method,answer:="EMAIL_OTP",v.Code
  if h.Route=="passkey/complete" {method="WEB_AUTHN";if len(v.Credential)==0||!json.Valid(v.Credential){return failure(req,ErrRejected)};answer=string(v.Credential)}
  result,e:=h.Service.Finish(ctx,v.Email,method,v.Session,answer);if e!=nil{return failure(req,e)}
  return respond(req,200,result)
 case "refresh":
  result,e:=h.Service.Refresh(ctx,v.RefreshToken);if e!=nil{return failure(req,e)}
  return respond(req,200,result)
 case "status":
  header:=req.Headers["authorization"];if header==""{header=req.Headers["Authorization"]}
  parts:=strings.Split(header," ");if len(parts)!=2||parts[0]!="Bearer"{return failure(req,ErrUnauthenticated)}
  has,e:=h.Service.HasPasskey(ctx,parts[1]);if e!=nil{return failure(req,e)}
  return respond(req,200,map[string]bool{"passkey_registered":has})
 }
 return respond(req,404,map[string]string{"error":"not_found"})
}
