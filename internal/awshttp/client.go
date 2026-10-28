// Package awshttp supplies bounded SigV4 calls to deployment-owned AWS endpoints.
package awshttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"io"
	"net/http"
	"time"
)

var ErrUnavailable = errors.New("aws_operation_unavailable")

type Client struct {
	Config aws.Config
	HTTP   *http.Client
}

func (c Client) Do(ctx context.Context, service, method, target string, body []byte, headers map[string]string) (int, []byte, error) {
	r, e := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if e != nil {
		return 0, nil, ErrUnavailable
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	credentials, e := c.Config.Credentials.Retrieve(ctx)
	if e != nil {
		return 0, nil, ErrUnavailable
	}
	h := sha256.Sum256(body)
	if service == "s3" {
		r.Header.Set("x-amz-content-sha256", hex.EncodeToString(h[:]))
	}
	if e = v4.NewSigner().SignHTTP(ctx, credentials, r, hex.EncodeToString(h[:]), service, c.Config.Region, time.Now(), func(o *v4.SignerOptions) { o.DisableURIPathEscaping = service == "s3" }); e != nil {
		return 0, nil, ErrUnavailable
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	out, e := client.Do(r)
	if e != nil {
		return 0, nil, ErrUnavailable
	}
	defer out.Body.Close()
	b, e := io.ReadAll(io.LimitReader(out.Body, 1024*1024+1))
	if e != nil || len(b) > 1024*1024 {
		return 0, nil, ErrUnavailable
	}
	return out.StatusCode, b, nil
}
