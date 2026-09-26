package passkey

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "errors"
 "log"
 "net/http"

 "github.com/aws/aws-lambda-go/events"
)

type Handler struct { Service Service }
func reply(req events.APIGatewayV2HTTPRequest, code int, body any) (events.APIGatewayV2HTTPResponse, error) {
 b, _ := json.Marshal(body)
 log.Printf("endpoint=%s status=%d trace_id=%s", req.RawPath, code, req.RequestContext.RequestID)
 return events.APIGatewayV2HTTPResponse{StatusCode:code, Body:string(b), Headers:map[string]string{"content-type":"application/json; charset=utf-8", "cache-control":"no-store", "referrer-policy":"no-referrer", "x-request-id":req.RequestContext.RequestID}}, nil
}
func fail(req events.APIGatewayV2HTTPRequest, err error) (events.APIGatewayV2HTTPResponse,error) {
 switch {
 case errors.Is(err,ErrUnauthorized): return reply(req,401,map[string]string{"error":"sign_in_required"})
 case errors.Is(err,ErrInvalid): return reply(req,400,map[string]string{"error":"invalid_credential"})
 default: return reply(req,503,map[string]string{"error":"temporarily_unavailable"})
 }
}
func (h Handler) Options(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
 if req.RawPath!="/passkeys/options" || req.RequestContext.HTTP.Method!="POST" { return reply(req,404,map[string]string{"error":"not_found"}) }
 header := req.Headers["authorization"]; if header=="" { header=req.Headers["Authorization"] }
 token, err := Bearer(header)
 if err != nil { return fail(req,err) }
 result, err := h.Service.Options(ctx,token)
 if err != nil { return fail(req,err) }
 return reply(req,200,map[string]json.RawMessage{"creation_options":result})
}
func (h Handler) Complete(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
 if req.RawPath!="/passkeys/complete" || req.RequestContext.HTTP.Method!="POST" { return reply(req,404,map[string]string{"error":"not_found"}) }
 header := req.Headers["authorization"]; if header=="" { header=req.Headers["Authorization"] }
 token, err := Bearer(header)
 if err != nil { return fail(req,err) }
 body:=req.Body
 if req.IsBase64Encoded { raw,e:=base64.StdEncoding.DecodeString(body); if e!=nil { return fail(req,ErrInvalid) }; body=string(raw) }
 if len(body)>40000 { return fail(req,ErrInvalid) }
 var input struct { Credential json.RawMessage `json:"credential"` }
 if json.Unmarshal([]byte(body),&input)!=nil { return fail(req,ErrInvalid) }
 if err=h.Service.Complete(ctx,token,input.Credential); err!=nil { return fail(req,err) }
 return reply(req,http.StatusOK,map[string]bool{"registered":true})
}
