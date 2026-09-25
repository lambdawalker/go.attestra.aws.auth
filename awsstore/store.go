package awsstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.attestra.aws.auth/email"
)

type Store struct{ DB *dynamodb.Client;Table string }
func key(id string)map[string]types.AttributeValue{return map[string]types.AttributeValue{"id":&types.AttributeValueMemberS{Value:id}}}
func conditional(err error)bool{var c *types.ConditionalCheckFailedException;return errors.As(err,&c)}
func(s Store)Put(ctx context.Context,t email.Transaction)error{
	item,err:=attributevalue.MarshalMap(t);if err!=nil{return err}
	_,err=s.DB.PutItem(ctx,&dynamodb.PutItemInput{TableName:aws.String(s.Table),Item:item,ConditionExpression:aws.String("attribute_not_exists(id)")})
	if conditional(err){return email.ErrConflict};return err
}
func(s Store)Get(ctx context.Context,id string)(email.Transaction,error){
	out,err:=s.DB.GetItem(ctx,&dynamodb.GetItemInput{TableName:aws.String(s.Table),Key:key(id),ConsistentRead:aws.Bool(true)});if err!=nil{return email.Transaction{},err};if len(out.Item)==0{return email.Transaction{},email.ErrNotFound}
	var t email.Transaction;if err=attributevalue.UnmarshalMap(out.Item,&t);err!=nil{return t,err};return t,nil
}
func(s Store)Swap(ctx context.Context,before,after email.Transaction)error{
	item,err:=attributevalue.MarshalMap(after);if err!=nil{return err}
	_,err=s.DB.PutItem(ctx,&dynamodb.PutItemInput{TableName:aws.String(s.Table),Item:item,ConditionExpression:aws.String("#v = :v AND #s = :s"),ExpressionAttributeNames:map[string]string{"#v":"version","#s":"state"},ExpressionAttributeValues:map[string]types.AttributeValue{":v":&types.AttributeValueMemberN{Value:fmt.Sprint(before.Version)},":s":&types.AttributeValueMemberS{Value:string(before.State)}}})
	if conditional(err){return email.ErrConflict};return err
}
func(s Store)Charge(ctx context.Context,name string,limit int,now time.Time)error{
	// Hourly account/source budget persists across resend generations and request IDs.
	id:=fmt.Sprintf("budget#%s#%d",name,now.Unix()/3600)
	_,err:=s.DB.UpdateItem(ctx,&dynamodb.UpdateItemInput{TableName:aws.String(s.Table),Key:key(id),UpdateExpression:aws.String("ADD attempts :one SET #ttl = :ttl"),ConditionExpression:aws.String("attribute_not_exists(attempts) OR attempts < :limit"),ExpressionAttributeNames:map[string]string{"#ttl":"ttl"},ExpressionAttributeValues:map[string]types.AttributeValue{":one":&types.AttributeValueMemberN{Value:"1"},":limit":&types.AttributeValueMemberN{Value:fmt.Sprint(limit)},":ttl":&types.AttributeValueMemberN{Value:fmt.Sprint(now.Add(2*time.Hour).Unix())}}})
	if conditional(err){return email.ErrLimited};return err
}
