package slack

import (
	"context"
	"errors"
	"net/http"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

// do owns dispatch and status handling. Error responses are closed here;
// successful callers own the response body. No path follows redirects or retries.
func (c *Client) do(ctx context.Context, token Token, request *http.Request, class operationClass) (*http.Response, error) {
	if token == "" {
		return nil, errx.New(errx.Auth, "CREDENTIAL_MISSING", "profile credential is missing", "login the exact profile")
	}
	if err := ctx.Err(); err != nil {
		if class == writeOperation {
			return nil, errx.New(errx.Conflict, "WRITE_NOT_STARTED", "write was cancelled before dispatch", "create a fresh dry-run before deciding whether to send").WithStage(errx.StagePreDispatch).Wrap(err)
		}
		return nil, networkError(class, errx.StagePreDispatch, err)
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	client := *c.httpClient
	if class == downloadOperation {
		client.Timeout = contract.FileDownloadDeadline
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		// net/http errors may include a private URL. Keep only a fixed cause.
		return nil, networkError(class, errx.StageTransport, errors.New("slack transport failed"))
	}
	err = responseError(response, class)
	if err != nil {
		_ = response.Body.Close() // The status error is authoritative; never expose close errors.
		return nil, err
	}
	return response, nil
}

func responseError(response *http.Response, class operationClass) error {
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		if class == writeOperation {
			return networkError(class, errx.StageHTTPResponse, errors.New("slack redirect after dispatch"))
		}
		if class == downloadOperation {
			return errx.New(errx.PermissionDenied, "FILE_REDIRECT_REJECTED", "file download redirect was rejected", "use a directly downloadable hosted Slack file")
		}
		return errx.New(errx.Internal, "REDIRECT_REJECTED", "Slack redirect was rejected", "report unexpected Slack endpoint behavior")
	case response.StatusCode == http.StatusTooManyRequests:
		return retryAfterError(response.Header.Get("Retry-After"), class)
	case response.StatusCode >= 500:
		return networkError(class, errx.StageHTTPServer, errors.New("slack server failure"))
	case response.StatusCode == http.StatusUnauthorized:
		return errx.New(errx.Auth, "SLACK_AUTH_REJECTED", "Slack rejected the credential", "login or rotate this profile")
	case response.StatusCode == http.StatusForbidden:
		return errx.New(errx.PermissionDenied, "SLACK_PERMISSION_DENIED", "Slack denied the operation", "request the required Slack permission")
	case class == downloadOperation && response.StatusCode == http.StatusNotFound:
		return errx.New(errx.NotFound, "SLACK_OBJECT_NOT_FOUND", "file was not found or is not visible", "verify the exact attachment")
	case response.StatusCode < 200 || response.StatusCode >= 300 || (class == downloadOperation && response.StatusCode != http.StatusOK):
		if class == writeOperation {
			return networkError(class, errx.StageHTTPResponse, errors.New("unexpected Slack write HTTP status"))
		}
		return errx.New(errx.Internal, "SLACK_HTTP_ERROR", "Slack returned an unexpected HTTP status", "report this response class")
	}
	return nil
}
