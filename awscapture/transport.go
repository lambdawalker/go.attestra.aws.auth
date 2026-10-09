package awscapture

import (
 "bytes"
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/base64"
 "strings"
 "encoding/json"
 "encoding/xml"
 "errors"
 "io"
 "net/http"
 "net/url"
 "strconv"
 "time"

 "github.com/aws/aws-sdk-go-v2/aws"
 v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
 "github.com/lambdawalker/go.attestra.aws.auth/capture"
)
// Narrow REST adapters use the existing AWS SDK credential chain and SigV4 signer.
// No caller-supplied hosts/keys reach this transport; errors never contain signed URLs.
type Transport struct { Config aws.Config; HTTP *http.Client; Bucket,QueueURL string }
type objectVersion struct {Key string `xml:"Key"`; VersionID string `xml:"VersionId"`}
var errAWS=errors.New("storage_unavailable")
func(t *Transport)client()*http.Client{if t.HTTP!=nil{return t.HTTP};return &http.Client{Timeout:25*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}}
func(t *Transport)objectURL(key string,q url.Values)string{u:=url.URL{Scheme:"https",Host:t.Bucket+".s3."+t.Config.Region+".amazonaws.com",Path:"/"+key,RawQuery:q.Encode()};return u.String()}
func(t *Transport)request(ctx context.Context,service,method,target string,body []byte,headers map[string]string)(*http.Response,error){r,e:=http.NewRequestWithContext(ctx,method,target,bytes.NewReader(body));if e!=nil{return nil,errAWS};for k,v:=range headers{r.Header.Set(k,v)};credentials,e:=t.Config.Credentials.Retrieve(ctx);if e!=nil{return nil,errAWS};h:=sha256.Sum256(body);if service=="s3"{r.Header.Set("x-amz-content-sha256",hex.EncodeToString(h[:]))};e=v4.NewSigner().SignHTTP(ctx,credentials,r,hex.EncodeToString(h[:]),service,t.Config.Region,time.Now(),func(o *v4.SignerOptions){o.DisableURIPathEscaping=service=="s3"});if e!=nil{return nil,errAWS};out,e:=t.client().Do(r);if e!=nil{return nil,errAWS};return out,nil}
func(t *Transport)Presign(ctx context.Context,u capture.Upload)(string,map[string]string,error){
 r,e:=http.NewRequestWithContext(ctx,http.MethodPut,t.objectURL(u.Key,url.Values{"X-Amz-Expires":{"300"}}),nil);if e!=nil{return "",nil,errAWS}
 r.ContentLength=u.Size
 headers:=map[string]string{"Content-Type":"image/jpeg","x-amz-checksum-sha256":u.SHA256,"x-amz-server-side-encryption":"AES256"};for k,v:=range headers{r.Header.Set(k,v)}
 credentials,e:=t.Config.Credentials.Retrieve(ctx);if e!=nil{return "",nil,errAWS};signed,_,e:=v4.NewSigner().PresignHTTP(ctx,credentials,r,"UNSIGNED-PAYLOAD","s3",t.Config.Region,time.Now(),func(o *v4.SignerOptions){o.DisableURIPathEscaping=true;o.DisableHeaderHoisting=true});if e!=nil{return "",nil,errAWS};return signed,headers,nil
}
func(t *Transport)Head(ctx context.Context,u capture.Upload)(capture.Asset,error){r,e:=t.request(ctx,"s3","HEAD",t.objectURL(u.Key,nil),nil,map[string]string{"x-amz-checksum-mode":"ENABLED"});if e!=nil{return capture.Asset{},e};defer r.Body.Close();if r.StatusCode==404{return capture.Asset{},capture.ErrIncomplete};if r.StatusCode!=200{return capture.Asset{},errAWS};size,e:=strconv.ParseInt(r.Header.Get("Content-Length"),10,64);if e!=nil||size!=u.Size||r.Header.Get("Content-Type")!="image/jpeg"{return capture.Asset{},capture.ErrImage};return capture.Asset{VersionID:r.Header.Get("x-amz-version-id"),Size:size,SHA256:r.Header.Get("x-amz-checksum-sha256")},nil}
func(t *Transport)Read(ctx context.Context,a capture.Asset)([]byte,error){r,e:=t.request(ctx,"s3","GET",t.objectURL(a.Key,url.Values{"versionId":{a.VersionID}}),nil,nil);if e!=nil{return nil,e};defer r.Body.Close();if r.StatusCode!=200{return nil,errAWS};b,e:=io.ReadAll(io.LimitReader(r.Body,capture.MaxBytes+1));if e!=nil{clear(b);return nil,errAWS};if int64(len(b))>capture.MaxBytes{clear(b);return nil,capture.ErrImage};return b,nil}
func(t *Transport)Write(ctx context.Context,record capture.Record,slot string,b []byte)(capture.Asset,error){
 sum:=sha256.Sum256(b);hash:=base64.StdEncoding.EncodeToString(sum[:]);key:=strings.Replace(capture.Prefix(record.Owner,record.ID),"uploads/","processed/",1)+strconv.FormatInt(record.Generation,10)+"/"+slot+"-"+hex.EncodeToString(sum[:])+".jpg"
 r,e:=t.request(ctx,"s3","PUT",t.objectURL(key,nil),b,map[string]string{"Content-Type":"image/jpeg","x-amz-checksum-sha256":hash,"x-amz-server-side-encryption":"AES256"});if e!=nil{return capture.Asset{},e};defer r.Body.Close();version:=r.Header.Get("x-amz-version-id");if r.StatusCode!=200||version==""||version=="null"{return capture.Asset{},errAWS};return capture.Asset{Slot:slot,Key:key,VersionID:version,SHA256:hash,Size:int64(len(b))},nil
}
func(t *Transport)DeleteCapture(ctx context.Context,prefix string)error{
 if e:=t.deletePrefix(ctx,prefix);e!=nil{return e};return t.deletePrefix(ctx,strings.Replace(prefix,"uploads/","processed/",1))
}
func(t *Transport)deletePrefix(ctx context.Context,prefix string)error{
 // Delete all versions, including delete markers. Repeat the first page so retries
 // cannot skip objects after deleting a pagination marker. Expired PUT URLs precede cleanup.
 for page:=0;page<25;page++{r,e:=t.request(ctx,"s3","GET",t.objectURL("",url.Values{"versions":{""},"prefix":{prefix},"max-keys":{"100"}}),nil,nil);if e!=nil{return e};var result struct{Versions []objectVersion `xml:"Version"`;Markers []objectVersion `xml:"DeleteMarker"`};if r.StatusCode!=200{r.Body.Close();return errAWS};e=xml.NewDecoder(io.LimitReader(r.Body,1024*1024)).Decode(&result);r.Body.Close();if e!=nil{return errAWS};objects:=append(result.Versions,result.Markers...);if len(objects)==0{return nil};for _,object:=range objects{resp,e:=t.request(ctx,"s3","DELETE",t.objectURL(object.Key,url.Values{"versionId":{object.VersionID}}),nil,nil);if e!=nil{return e};resp.Body.Close();if resp.StatusCode!=204{return errAWS}}};return errAWS
}
func(t *Transport)Send(ctx context.Context,job capture.Job)error{b,e:=json.Marshal(job);if e!=nil{return e};body:=url.Values{"Action":{"SendMessage"},"Version":{"2012-11-05"},"MessageBody":{string(b)}}.Encode();r,e:=t.request(ctx,"sqs","POST",t.QueueURL,[]byte(body),map[string]string{"Content-Type":"application/x-www-form-urlencoded"});if e!=nil{return e};defer r.Body.Close();if r.StatusCode!=200{return errAWS};return nil}
