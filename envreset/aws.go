package envreset

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	ct "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dt "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.attestra.aws.auth/internal/awshttp"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Manifest is injected by Pulumi, never accepted from dashboard requests.
type Manifest struct {
	Functions []string
	Tables    []string
	Pool      string
	Bucket    string
	Queues    []string
}
type AWSBackend struct {
	Manifest Manifest
	HTTP     awshttp.Client
	DB       *dynamodb.Client
	Cognito  *cognitoidentityprovider.Client
}

func (b AWSBackend) lambda(ctx context.Context, name, method string, count int) ([]byte, error) {
	version := "2017-10-31"
	if method == "GET" {
		version = "2019-09-30"
	}
	var body []byte
	if method == "PUT" {
		body = []byte(`{"ReservedConcurrentExecutions":` + strconv.Itoa(count) + `}`)
	}
	status, data, e := b.HTTP.Do(ctx, "lambda", method, "https://lambda."+b.HTTP.Config.Region+".amazonaws.com/"+version+"/functions/"+url.PathEscape(name)+"/concurrency", body, map[string]string{"Content-Type": "application/json"})
	if e != nil || status < 200 || status > 299 {
		return nil, awshttp.ErrUnavailable
	}
	return data, nil
}
func (b AWSBackend) Snapshot(ctx context.Context) (map[string]int, error) {
	// This contract relies on self-signup being disabled. Stop before pausing if drift enables it.
	pool, e := b.Cognito.DescribeUserPool(ctx, &cognitoidentityprovider.DescribeUserPoolInput{UserPoolId: aws.String(b.Manifest.Pool)})
	if e != nil || pool.UserPool == nil || pool.UserPool.AdminCreateUserConfig == nil || !pool.UserPool.AdminCreateUserConfig.AllowAdminCreateUserOnly {
		return nil, awshttp.ErrUnavailable
	}
	out := map[string]int{}
	for _, name := range b.Manifest.Functions {
		data, e := b.lambda(ctx, name, "GET", 0)
		if e != nil {
			return nil, e
		}
		var r struct{ ReservedConcurrentExecutions *int }
		if json.Unmarshal(data, &r) != nil {
			return nil, awshttp.ErrUnavailable
		}
		out[name] = -1
		if r.ReservedConcurrentExecutions != nil {
			out[name] = *r.ReservedConcurrentExecutions
		}
	}
	return out, nil
}
func (b AWSBackend) validateSnapshot(snapshot map[string]int) error {
	if len(snapshot) != len(b.Manifest.Functions) {
		return ErrInvalid
	}
	for _, name := range b.Manifest.Functions {
		if n, ok := snapshot[name]; !ok || n < -1 {
			return ErrInvalid
		}
	}
	return nil
}
func (b AWSBackend) Pause(ctx context.Context, snapshot map[string]int) error {
	if e := b.validateSnapshot(snapshot); e != nil {
		return e
	}
	for _, name := range b.Manifest.Functions {
		if _, e := b.lambda(ctx, name, "PUT", 0); e != nil {
			return e
		}
	}
	return nil
}
func (b AWSBackend) Restore(ctx context.Context, snapshot map[string]int) error {
	if e := b.validateSnapshot(snapshot); e != nil {
		return e
	}
	names := append([]string{}, b.Manifest.Functions...)
	sort.Strings(names)
	for _, name := range names {
		method := "PUT"
		if snapshot[name] < 0 {
			method = "DELETE"
		}
		if _, e := b.lambda(ctx, name, method, snapshot[name]); e != nil {
			return e
		}
	}
	return nil
}
func (b AWSBackend) Purge(ctx context.Context) error {
	for _, queue := range b.Manifest.Queues {
		body := url.Values{"Action": {"PurgeQueue"}, "Version": {"2012-11-05"}}.Encode()
		status, data, e := b.HTTP.Do(ctx, "sqs", "POST", queue, []byte(body), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		if e != nil {
			return e
		}
		if status != 200 && !(status == 400 && strings.Contains(string(data), "PurgeQueueInProgress")) {
			return awshttp.ErrUnavailable
		}
	}
	return nil
}
func (b AWSBackend) Clear(ctx context.Context, kind string) (bool, error) {
	switch kind {
	case "users":
		out, e := b.Cognito.ListUsers(ctx, &cognitoidentityprovider.ListUsersInput{UserPoolId: aws.String(b.Manifest.Pool), Limit: aws.Int32(25)})
		if e != nil {
			return false, e
		}
		for _, user := range out.Users {
			_, e = b.Cognito.AdminDeleteUser(ctx, &cognitoidentityprovider.AdminDeleteUserInput{UserPoolId: aws.String(b.Manifest.Pool), Username: user.Username})
			var missing *ct.UserNotFoundException
			if e != nil && !errors.As(e, &missing) {
				return false, e
			}
		}
		return len(out.Users) == 0, nil
	case "tables":
		for _, table := range b.Manifest.Tables {
			out, e := b.DB.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(table), Limit: aws.Int32(25), ConsistentRead: aws.Bool(true), ProjectionExpression: aws.String("id")})
			if e != nil {
				return false, e
			}
			if len(out.Items) == 0 {
				continue
			}
			writes := []dt.WriteRequest{}
			for _, item := range out.Items {
				if _, ok := item["id"]; !ok {
					return false, ErrInvalid
				}
				writes = append(writes, dt.WriteRequest{DeleteRequest: &dt.DeleteRequest{Key: item}})
			}
			_, e = b.DB.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]dt.WriteRequest{table: writes}})
			// Unprocessed items remain for the next scan, so partial throttling cannot lose work.
			return false, e
		}
		return true, nil
	case "objects":
		return b.clearObjects(ctx)
	}
	return false, ErrInvalid
}
func (b AWSBackend) clearObjects(ctx context.Context) (bool, error) {
	target := url.URL{Scheme: "https", Host: b.Manifest.Bucket + ".s3." + b.HTTP.Config.Region + ".amazonaws.com", Path: "/", RawQuery: url.Values{"versions": {""}, "max-keys": {"25"}}.Encode()}
	status, data, e := b.HTTP.Do(ctx, "s3", "GET", target.String(), nil, nil)
	if e != nil || status != 200 {
		return false, awshttp.ErrUnavailable
	}
	type object struct {
		Key     string `xml:"Key"`
		Version string `xml:"VersionId"`
	}
	var result struct {
		Versions  []object `xml:"Version"`
		Markers   []object `xml:"DeleteMarker"`
		Truncated bool     `xml:"IsTruncated"`
	}
	if xml.Unmarshal(data, &result) != nil {
		return false, awshttp.ErrUnavailable
	}
	objects := append(result.Versions, result.Markers...)
	for _, o := range objects {
		target.Path = "/" + o.Key
		target.RawQuery = url.Values{"versionId": {o.Version}}.Encode()
		status, _, e = b.HTTP.Do(ctx, "s3", "DELETE", target.String(), nil, nil)
		if e != nil || status != 204 {
			return false, awshttp.ErrUnavailable
		}
	}
	return len(objects) == 0 && !result.Truncated, nil
}
