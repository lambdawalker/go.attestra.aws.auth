package email

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalid=errors.New("invalid request")
	ErrNotFound=errors.New("not found")
	ErrConflict=errors.New("conflict")
	ErrLimited=errors.New("attempt limit")
	ErrUnusable=errors.New("link unusable")
	ErrIncorrect=errors.New("incorrect code")
	ErrUsed=errors.New("already confirmed")
	ErrSignInRequired=errors.New("confirmed; sign in required")
)
type State string
const (Pending State="pending"; Confirming State="confirming"; Confirmed State="confirmed"; Failed State="failed")
type Transaction struct {
	ID string `dynamodbav:"id"`
	Email string `dynamodbav:"email"`
	Challenge string `dynamodbav:"challenge"`
	BHash string `dynamodbav:"b_hash"`
	CDigest string `dynamodbav:"c_digest"`
	State State `dynamodbav:"state"`
	Generation int `dynamodbav:"generation"`
	Version int `dynamodbav:"version"`
	Attempts int `dynamodbav:"attempts"`
	Resends int `dynamodbav:"resends"`
	Expires int64 `dynamodbav:"expires"`
	LastSent int64 `dynamodbav:"last_sent"`
	ClaimedAt int64 `dynamodbav:"claimed_at"`
	TTL int64 `dynamodbav:"ttl"`
	Subject string `dynamodbav:"subject"`
}
type Store interface {
	Put(context.Context,Transaction)error
	Get(context.Context,string)(Transaction,error)
	Swap(context.Context,Transaction,Transaction)error
	Charge(context.Context,string,int,time.Time)error
}
type Sender interface{ Send(context.Context,string,string,string)error }
type Session struct {AccessToken string `json:"access_token"`;IDToken string `json:"id_token"`;RefreshToken string `json:"refresh_token"`;ExpiresIn int32 `json:"expires_in"`;TokenType string `json:"token_type"`}
type Identity interface {
	Eligible(context.Context,string)(bool,error)
	Confirm(context.Context,string)(string,error)
	Session(context.Context,string)(Session,error)
}
type Service struct {Store Store;Sender Sender;Identity Identity;Key []byte;Origin string;Now func()time.Time;Random func([]byte)error}
type IncorrectCode struct{Remaining int}
func(e IncorrectCode)Error()string{return ErrIncorrect.Error()}
func(e IncorrectCode)Is(target error)bool{return target==ErrIncorrect}
type SignupResult struct{RequestID string `json:"request_id"`}
type ConfirmInput struct{RequestID string `json:"request_id"`;TokenB string `json:"token_b"`;TokenA string `json:"token_a,omitempty"`;TokenC string `json:"token_c,omitempty"`}

func (s *Service) now()time.Time{if s.Now!=nil{return s.Now().UTC()};return time.Now().UTC()}
func (s *Service) random(b []byte)error{if s.Random!=nil{return s.Random(b)};_,err:=rand.Read(b);return err}
func (s *Service) secret() (string,error){b:=make([]byte,32);if err:=s.random(b);err!=nil{return "",err};return base64.RawURLEncoding.EncodeToString(b),nil}
func (s *Service) digest(parts ...string)string{mac:=hmac.New(sha256.New,s.Key);for _,p:=range parts{mac.Write([]byte(p));mac.Write([]byte{0})};return hex.EncodeToString(mac.Sum(nil))}
func hash(s string)string{sum:=sha256.Sum256([]byte(s));return hex.EncodeToString(sum[:])}
func validToken(v string)bool{b,e:=base64.RawURLEncoding.DecodeString(v);return e==nil&&len(b)==32&&base64.RawURLEncoding.EncodeToString(b)==v}
func validChallenge(v string)bool{return validToken(v)}
func same(a,b string)bool{return hmac.Equal([]byte(a),[]byte(b))}
func cleanEmail(email string)(string,error){e:=strings.ToLower(strings.TrimSpace(email));parsed,err:=mail.ParseAddress(e);if err!=nil||parsed.Address!=e||len(e)>254||strings.Count(e,"@")!=1{return "",ErrInvalid};return e,nil}
func (s *Service) code()(string,error){var code strings.Builder;for code.Len()<6 {b:=make([]byte,1);if err:=s.random(b);err!=nil{return "",err};if b[0]<250{code.WriteByte('0'+b[0]%10)}};return code.String(),nil}
func (s *Service) origin() (string,error) {u,e:=url.Parse(s.Origin);if e!=nil||u.Scheme!="https"||u.Host==""||u.Path!=""||u.RawQuery!=""||u.Fragment!=""||u.User!=nil{return "",ErrInvalid};return strings.TrimRight(s.Origin,"/"),nil}

func (s *Service) Signup(ctx context.Context,email,challenge,method,source string)(SignupResult,error){
	e,err:=cleanEmail(email);if err!=nil||method!="S256"||!validChallenge(challenge){return SignupResult{},ErrInvalid}
	origin,err:=s.origin();if err!=nil||len(s.Key)<32{return SignupResult{},ErrInvalid}
	id,err:=s.secret();if err!=nil{return SignupResult{},err};result:=SignupResult{RequestID:id}
	if err=s.Store.Charge(ctx,"signup-source:"+s.digest(source),10,s.now());err!=nil {if errors.Is(err,ErrLimited){return result,nil};return result,err}
	if err=s.Store.Charge(ctx,"signup-email:"+s.digest(e),3,s.now());err!=nil {if errors.Is(err,ErrLimited){return result,nil};return result,err}
	eligible,err:=s.Identity.Eligible(ctx,e);if err!=nil||!eligible{return result,nil}
	b,err:=s.secret();if err!=nil{return result,err};c,err:=s.code();if err!=nil{return result,err}
	now:=s.now();t:=Transaction{ID:id,Email:e,Challenge:challenge,BHash:hash(b),CDigest:s.digest(id,"1",c),State:Pending,Generation:1,Version:1,Expires:now.Add(10*time.Minute).Unix(),TTL:now.Add(24*time.Hour).Unix(),LastSent:now.Unix()}
	if err=s.Store.Put(ctx,t);err!=nil{return result,err}
	link:=origin+"/verify-email?request_id="+url.QueryEscape(id)+"&b="+url.QueryEscape(b)
	// Delivery errors stay generic to avoid revealing account state. An unsent transaction expires normally.
	_ = s.Sender.Send(ctx,e,link,c)
	return result,nil
}

func (s *Service) Resend(ctx context.Context,id,source string)error{
	if !validToken(id){return ErrInvalid}
	if err:=s.Store.Charge(ctx,"resend-source:"+s.digest(source),10,s.now());err!=nil {if errors.Is(err,ErrLimited){return nil};return err}
	t,err:=s.Store.Get(ctx,id);if err!=nil {if errors.Is(err,ErrNotFound){return nil};return err}
	now:=s.now();if t.State!=Pending||t.Resends>=3||now.Unix()-t.LastSent<60{return nil}
	if err=s.Store.Charge(ctx,"resend-email:"+s.digest(t.Email),3,now);err!=nil {if errors.Is(err,ErrLimited){return nil};return err}
	b,err:=s.secret();if err!=nil{return err};c,err:=s.code();if err!=nil{return err}
	next:=t;next.Generation++;next.Version++;next.Resends++;next.Attempts=0;next.BHash=hash(b);next.CDigest=s.digest(id,fmt.Sprint(next.Generation),c);next.Expires=now.Add(10*time.Minute).Unix();next.LastSent=now.Unix()
	if err=s.Store.Swap(ctx,t,next);err!=nil {if errors.Is(err,ErrConflict){return nil};return err}
	origin,err:=s.origin();if err!=nil{return err}
	_ = s.Sender.Send(ctx,t.Email,origin+"/verify-email?request_id="+url.QueryEscape(id)+"&b="+url.QueryEscape(b),c)
	return nil
}

func (s *Service) Confirm(ctx context.Context,in ConfirmInput)(Session,error){
	if !validToken(in.RequestID)||!validToken(in.TokenB)||(in.TokenA=="")== (in.TokenC==""){return Session{},ErrInvalid}
	t,err:=s.Store.Get(ctx,in.RequestID);if err!=nil {if errors.Is(err,ErrNotFound){return Session{},ErrUnusable};return Session{},err}
	if t.State==Confirmed{return Session{},ErrUsed}
	if t.State==Confirming && s.now().Unix()-t.ClaimedAt>=60 && t.Expires>s.now().Unix() && same(t.BHash,hash(in.TokenB)) {
		// The API Lambda timeout is 25 seconds. After 60 seconds an absent account
		// can safely reclaim an interrupted confirmation; an existing account
		// must recover via email OTP rather than receive another session here.
		eligible,lookupErr:=s.Identity.Eligible(ctx,t.Email)
		if lookupErr!=nil{return Session{},ErrConflict}
		if !eligible{return Session{},ErrSignInRequired}
		retry:=t;retry.State=Pending;retry.Version++
		if err=s.Store.Swap(ctx,t,retry);err!=nil{return Session{},ErrConflict};t=retry
	}
	if t.State==Confirming{return Session{},ErrConflict}
	if t.State!=Pending||t.Expires<=s.now().Unix()||!same(t.BHash,hash(in.TokenB)){return Session{},ErrUnusable}
	if in.TokenA!="" {a,decodeErr:=base64.RawURLEncoding.DecodeString(in.TokenA);if decodeErr!=nil||len(a)!=32{return Session{},ErrUnusable};sum:=sha256.Sum256(a);if !same(t.Challenge,base64.RawURLEncoding.EncodeToString(sum[:])){return Session{},ErrUnusable}
	}else{
		if len(in.TokenC)!=6||strings.Trim(in.TokenC,"0123456789")!=""{return Session{},ErrInvalid}
		if t.Attempts>=5{return Session{},ErrLimited}
		if err=s.Store.Charge(ctx,"code-email:"+s.digest(t.Email),10,s.now());err!=nil{return Session{},err}
		if !same(t.CDigest,s.digest(t.ID,fmt.Sprint(t.Generation),in.TokenC)){
			next:=t;next.Attempts++;next.Version++;if err=s.Store.Swap(ctx,t,next);err!=nil{return Session{},ErrConflict};if next.Attempts>=5{return Session{},ErrLimited};return Session{},IncorrectCode{Remaining:5-next.Attempts}
		}
	}
	claim:=t;claim.State=Confirming;claim.ClaimedAt=s.now().Unix();claim.Version++
	if err=s.Store.Swap(ctx,t,claim);err!=nil{return Session{},ErrConflict}
	sub,err:=s.Identity.Confirm(ctx,t.Email)
	if err!=nil {failed:=claim;failed.State=Failed;failed.Version++;_ = s.Store.Swap(ctx,claim,failed);eligible,lookupErr:=s.Identity.Eligible(ctx,t.Email);if lookupErr==nil&&eligible{return Session{},ErrUnusable};return Session{},ErrSignInRequired}
	done:=claim;done.State=Confirmed;done.Subject=sub;done.BHash="";done.CDigest="";done.Challenge="";done.Version++
	if err=s.Store.Swap(ctx,claim,done);err!=nil{return Session{},ErrSignInRequired}
	session,err:=s.Identity.Session(ctx,t.Email);if err!=nil{return Session{},ErrSignInRequired}
	return session,nil
}
