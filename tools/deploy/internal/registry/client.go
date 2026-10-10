package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	model "github.com/lambdawalker/go.attestra.aws.auth/registry"
)

var ErrConflict = errors.New("environment is locked by another deployment or this deployment was superseded; inspect pending index receipts before retrying")

type Client struct {
	URL, Region, Environment string
	Credentials              aws.Credentials
	Provider                 aws.CredentialsProvider
	HTTP                     *http.Client
}

func (c *Client) Change(change model.Change) (model.Receipt, error) {
	body, e := json.Marshal(change)
	if e != nil {
		return model.Receipt{}, e
	}
	// Retrying the exact receipt is safe, including when the response was lost.
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
		}
		request, e := http.NewRequest("POST", c.URL+"/v1/environments/"+c.Environment+"/changes", bytes.NewReader(body))
		if e != nil {
			return model.Receipt{}, e
		}
		request.Header.Set("Content-Type", "application/json")
		hash := sha256.Sum256(body)
		signing := c.Credentials
		if c.Provider != nil {
			signing, e = c.Provider.Retrieve(context.Background())
			if e != nil {
				return model.Receipt{}, e
			}
		}
		if e = v4.NewSigner().SignHTTP(context.Background(), signing, request, hex.EncodeToString(hash[:]), "execute-api", c.Region, time.Now()); e != nil {
			return model.Receipt{}, e
		}
		response, e := c.HTTP.Do(request)
		if e != nil {
			last = errors.New("registry request failed; retry with the saved deployment receipt")
			continue
		}
		data, e := io.ReadAll(io.LimitReader(response.Body, 65536))
		response.Body.Close()
		if e != nil {
			last = e
			continue
		}
		if response.StatusCode == 200 {
			var receipt model.Receipt
			if e = json.Unmarshal(data, &receipt); e != nil {
				return receipt, errors.New("invalid registry response")
			}
			if change.Operation == "begin" {
				if receipt.Token != change.Token || receipt.Revision < 1 {
					return receipt, errors.New("invalid registry acquisition receipt")
				}
			} else {
				var result struct {
					OK bool `json:"ok"`
				}
				if json.Unmarshal(data, &result) != nil || !result.OK {
					return receipt, errors.New("registry did not confirm the change")
				}
			}
			return receipt, nil
		}
		if response.StatusCode == 409 {
			return model.Receipt{}, ErrConflict
		}
		last = fmt.Errorf("registry update rejected (HTTP %d); check deployment role execute-api permission and registry logs", response.StatusCode)
		if response.StatusCode < 500 && response.StatusCode != 429 {
			return model.Receipt{}, last
		}
	}
	return model.Receipt{}, last
}
