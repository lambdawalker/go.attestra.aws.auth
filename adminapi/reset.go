package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/lambdawalker/go.attestra.aws.auth/envreset"
	"github.com/lambdawalker/go.attestra.aws.auth/internal/awshttp"
	"github.com/lambdawalker/go.attestra.aws.auth/runtimeconfig"
	"net/url"
)

type ResetClient interface {
	Invoke(context.Context, envreset.Request) (envreset.Status, error)
}
type LambdaResetClient struct{ HTTP awshttp.Client }

func (c LambdaResetClient) Invoke(ctx context.Context, q envreset.Request) (envreset.Status, error) {
	if !runtimeconfig.ValidEnvironment(q.Environment) {
		return envreset.Status{}, runtimeconfig.ErrInvalid
	}
	body, e := json.Marshal(q)
	if e != nil {
		return envreset.Status{}, e
	}
	target := "https://lambda." + c.HTTP.Config.Region + ".amazonaws.com/2015-03-31/functions/" + url.PathEscape("attestra-reset-"+q.Environment) + "/invocations"
	status, data, e := c.HTTP.Do(ctx, "lambda", "POST", target, body, map[string]string{"Content-Type": "application/json", "X-Amz-Invocation-Type": "RequestResponse"})
	if e != nil || status != 200 {
		return envreset.Status{}, errors.New("reset_unavailable")
	}
	var reply struct {
		Status *envreset.Status `json:"status"`
		Error  string           `json:"error"`
	}
	if json.Unmarshal(data, &reply) != nil {
		return envreset.Status{}, errors.New("reset_unavailable")
	}
	switch reply.Error {
	case "reset_disabled":
		return envreset.Status{}, runtimeconfig.ErrForbidden
	case "invalid_reset_request":
		return envreset.Status{}, runtimeconfig.ErrInvalid
	case "reset_in_progress":
		return envreset.Status{}, runtimeconfig.ErrConflict
	}
	if reply.Error != "" || reply.Status == nil || reply.Status.Environment != q.Environment {
		return envreset.Status{}, errors.New("reset_unavailable")
	}
	return *reply.Status, nil
}
