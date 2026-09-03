package slack

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

type operationClass int

const (
	readOperation operationClass = iota
	writeOperation
)

// Client exposes only the fixed typed Slack Web API matrix.
type Client struct {
	origin     string
	httpClient *http.Client
}

// New returns a hardened production Slack client.
func New() *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &Client{origin: contract.ProductionSlackOrigin, httpClient: &http.Client{
		Transport:     transport,
		Timeout:       contract.NetworkDeadline,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func newTestClient(origin string, client *http.Client) *Client {
	return &Client{origin: origin, httpClient: client}
}

type apiEnvelope struct {
	OK           bool           `json:"ok"`
	Error        string         `json:"error"`
	Needed       string         `json:"needed"`
	Provided     string         `json:"provided"`
	Team         string         `json:"team"`
	TeamID       string         `json:"team_id"`
	URL          string         `json:"url"`
	UserID       string         `json:"user_id"`
	BotID        string         `json:"bot_id"`
	EnterpriseID string         `json:"enterprise_id"`
	Channels     []Conversation `json:"channels"`
	Channel      Conversation   `json:"channel"`
	Messages     []Message      `json:"messages"`
	User         struct {
		ID       string `json:"id"`
		TeamID   string `json:"team_id"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		Deleted  bool   `json:"deleted"`
		IsBot    bool   `json:"is_bot"`
		Profile  struct {
			DisplayName string `json:"display_name"`
			RealName    string `json:"real_name"`
		} `json:"profile"`
	} `json:"user"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

type authTestEnvelope struct {
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	Team         string `json:"team"`
	TeamID       string `json:"team_id"`
	URL          string `json:"url"`
	User         string `json:"user"`
	UserID       string `json:"user_id"`
	BotID        string `json:"bot_id"`
	EnterpriseID string `json:"enterprise_id"`
}

type apiBase struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}
type postEnvelope struct {
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

// AuthTest validates a Web API token and returns exact identity.
func (c *Client) AuthTest(ctx context.Context, token Token) (Identity, error) {
	var response authTestEnvelope
	if err := c.call(ctx, token, http.MethodPost, "auth.test", nil, &response, readOperation); err != nil {
		return Identity{}, err
	}
	return Identity{WorkspaceID: response.TeamID, WorkspaceName: response.Team, WorkspaceURL: response.URL, UserID: response.UserID, BotID: response.BotID, EnterpriseID: response.EnterpriseID}, nil
}

// ConversationsList returns one bounded discovery page.
func (c *Client) ConversationsList(ctx context.Context, token Token, options ListOptions) (ConversationPage, error) {
	if err := validateRequestedLimit(options.Limit); err != nil {
		return ConversationPage{}, err
	}
	values := url.Values{"types": {options.Types}, "limit": {strconv.Itoa(options.Limit)}, "exclude_archived": {"false"}}
	if options.Cursor != "" {
		values.Set("cursor", options.Cursor)
	}
	var response apiEnvelope
	if err := c.call(ctx, token, http.MethodGet, "conversations.list", values, &response, readOperation); err != nil {
		return ConversationPage{}, err
	}
	if err := validateResponseCount(len(response.Channels), options.Limit); err != nil {
		return ConversationPage{}, err
	}
	conversations := make([]ConversationSummary, 0, len(response.Channels))
	for _, item := range response.Channels {
		summary := ConversationSummary{ID: item.ID, Name: item.Name, User: item.User, IsChannel: item.IsChannel, IsIM: item.IsIM, IsMPIM: item.IsMPIM, IsPrivate: item.IsPrivate, IsArchived: item.IsArchived}
		if item.IsShared != nil {
			summary.IsShared = *item.IsShared
		}
		if item.IsExtShared != nil {
			summary.IsExtShared = *item.IsExtShared
		}
		if item.IsOrgShared != nil {
			summary.IsOrgShared = *item.IsOrgShared
		}
		conversations = append(conversations, summary)
	}
	return ConversationPage{Conversations: conversations, NextCursor: response.ResponseMetadata.NextCursor}, nil
}

// ConversationInfo returns one exact conversation.
func (c *Client) ConversationInfo(ctx context.Context, token Token, conversationID string) (Conversation, error) {
	var response apiEnvelope
	if err := c.call(ctx, token, http.MethodGet, "conversations.info", url.Values{"channel": {conversationID}}, &response, readOperation); err != nil {
		return Conversation{}, err
	}
	return response.Channel, nil
}

// History returns one bounded exact-conversation history page.
func (c *Client) History(ctx context.Context, token Token, options HistoryOptions) (MessagePage, error) {
	if err := validateRequestedLimit(options.Limit); err != nil {
		return MessagePage{}, err
	}
	values := url.Values{"channel": {options.ConversationID}, "limit": {strconv.Itoa(options.Limit)}}
	if options.Cursor != "" {
		values.Set("cursor", options.Cursor)
	}
	if options.Oldest != "" {
		values.Set("oldest", options.Oldest)
	}
	if options.Latest != "" {
		values.Set("latest", options.Latest)
	}
	var response apiEnvelope
	if err := c.call(ctx, token, http.MethodGet, "conversations.history", values, &response, readOperation); err != nil {
		return MessagePage{}, err
	}
	if err := validateResponseCount(len(response.Messages), options.Limit); err != nil {
		return MessagePage{}, err
	}
	return MessagePage{Messages: response.Messages, NextCursor: response.ResponseMetadata.NextCursor}, nil
}

// Replies returns one bounded exact thread page.
func (c *Client) Replies(ctx context.Context, token Token, options ThreadOptions) (MessagePage, error) {
	if err := validateRequestedLimit(options.Limit); err != nil {
		return MessagePage{}, err
	}
	values := url.Values{"channel": {options.ConversationID}, "ts": {options.ThreadTS}, "limit": {strconv.Itoa(options.Limit)}}
	if options.Cursor != "" {
		values.Set("cursor", options.Cursor)
	}
	var response apiEnvelope
	if err := c.call(ctx, token, http.MethodGet, "conversations.replies", values, &response, readOperation); err != nil {
		return MessagePage{}, err
	}
	if err := validateResponseCount(len(response.Messages), options.Limit); err != nil {
		return MessagePage{}, err
	}
	return MessagePage{Messages: response.Messages, NextCursor: response.ResponseMetadata.NextCursor}, nil
}

// UserInfo returns one minimal user projection.
func (c *Client) UserInfo(ctx context.Context, token Token, userID string) (User, error) {
	var response apiEnvelope
	if err := c.call(ctx, token, http.MethodGet, "users.info", url.Values{"user": {userID}}, &response, readOperation); err != nil {
		return User{}, err
	}
	return User{ID: response.User.ID, TeamID: response.User.TeamID, Name: response.User.Name, RealName: response.User.RealName, DisplayName: response.User.Profile.DisplayName, Deleted: response.User.Deleted, IsBot: response.User.IsBot}, nil
}

// PostMessage dispatches exactly one bounded plain-text message request.
func (c *Client) PostMessage(ctx context.Context, token Token, options PostOptions) (PostResult, error) {
	if err := validatePostOptions(options); err != nil {
		return PostResult{}, err
	}
	values := url.Values{"channel": {options.ConversationID}, "text": {options.Text}, "unfurl_links": {"false"}, "unfurl_media": {"false"}}
	if options.ThreadTS != "" {
		values.Set("thread_ts", options.ThreadTS)
	}
	var response postEnvelope
	if err := c.call(ctx, token, http.MethodPost, "chat.postMessage", values, &response, writeOperation); err != nil {
		return PostResult{}, err
	}
	return PostResult{ConversationID: response.Channel, TS: response.TS, ThreadTS: options.ThreadTS}, nil
}

func validatePostOptions(options PostOptions) error {
	if !validSlackID(options.ConversationID) {
		return errx.New(errx.Usage, "INVALID_CONVERSATION_ID", "conversation ID is invalid", "use an exact bounded Slack conversation ID")
	}
	if options.ThreadTS != "" && !validSlackTimestamp(options.ThreadTS) {
		return errx.New(errx.Usage, "INVALID_TIMESTAMP", "thread timestamp is invalid", "use an exact bounded Slack parent timestamp")
	}
	if !utf8.ValidString(options.Text) || strings.TrimSpace(options.Text) == "" || strings.ContainsRune(options.Text, '\x00') || len(options.Text) > contract.MaxMessageBytes || utf8.RuneCountInString(options.Text) > contract.MaxMessageRunes {
		return errx.New(errx.Usage, "INVALID_MESSAGE_TEXT", "message text is outside the v1 bound", "provide non-empty bounded UTF-8 plain text without NUL")
	}
	return nil
}

func validSlackID(value string) bool {
	if value == "" || len(value) > contract.MaxSlackIDBytes {
		return false
	}
	for _, char := range value {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func validSlackTimestamp(value string) bool {
	if value == "" || len(value) > contract.MaxSlackTimestampBytes {
		return false
	}
	dot := false
	for _, char := range value {
		if char == '.' && !dot {
			dot = true
			continue
		}
		if char < '0' || char > '9' {
			return false
		}
	}
	return value[0] != '.' && value[len(value)-1] != '.'
}

func (c *Client) call(ctx context.Context, token Token, method, route string, values url.Values, target any, class operationClass) error {
	if token == "" {
		return errx.New(errx.Auth, "CREDENTIAL_MISSING", "profile credential is missing", "login the exact profile")
	}
	if !routeAllowed(route) {
		return errx.New(errx.Internal, "ROUTE_NOT_ALLOWED", "Slack route is not allowed", "report this defect")
	}
	if values == nil {
		values = url.Values{}
	}
	encoded := values.Encode()
	if len(encoded) > contract.MaxRequestBodyBytes {
		return errx.New(errx.Usage, "REQUEST_TOO_LARGE", "request exceeds the v1 bound", "narrow the request")
	}
	endpoint := c.origin + "/api/" + route
	var body io.Reader
	if method == http.MethodGet {
		if encoded != "" {
			endpoint += "?" + encoded
		}
	} else {
		body = bytes.NewBufferString(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errx.New(errx.Internal, "REQUEST_BUILD_FAILED", "failed to build Slack request", "report this defect").Wrap(err)
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "gzip")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if err := ctx.Err(); err != nil {
		if class == writeOperation {
			return errx.New(errx.Conflict, "WRITE_NOT_STARTED", "write was cancelled before dispatch", "create a fresh dry-run before deciding whether to send").WithStage(errx.StagePreDispatch).Wrap(err)
		}
		return networkError(class, errx.StagePreDispatch, err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return networkError(class, errx.StageTransport, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if class == writeOperation {
			return networkError(class, errx.StageHTTPResponse, errors.New("slack redirect after dispatch"))
		}
		return errx.New(errx.Internal, "REDIRECT_REJECTED", "Slack redirect was rejected", "report unexpected Slack endpoint behavior")
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return retryAfterError(response.Header.Get("Retry-After"), class)
	}
	if response.StatusCode >= 500 {
		return networkError(class, errx.StageHTTPServer, errors.New("slack server failure"))
	}
	if response.StatusCode == http.StatusUnauthorized {
		return errx.New(errx.Auth, "SLACK_AUTH_REJECTED", "Slack rejected the credential", "login or rotate this profile")
	}
	if response.StatusCode == http.StatusForbidden {
		return errx.New(errx.PermissionDenied, "SLACK_PERMISSION_DENIED", "Slack denied the operation", "request the required Slack permission")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if class == writeOperation {
			return networkError(class, errx.StageHTTPResponse, errors.New("unexpected Slack write HTTP status"))
		}
		return errx.New(errx.Internal, "SLACK_HTTP_ERROR", "Slack returned an unexpected HTTP status", "report this response class")
	}
	payload, err := readResponse(response)
	if err != nil {
		return networkError(class, errx.StageResponseBody, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		return networkError(class, errx.StageResponseJSON, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return networkError(class, errx.StageResponseJSON, errors.New("slack response contains trailing data"))
	}
	var base apiBase
	if err := json.Unmarshal(payload, &base); err != nil {
		return networkError(class, errx.StageResponseJSON, err)
	}
	if !base.OK {
		return slackError(base.Error, class)
	}
	return nil
}

func readResponse(response *http.Response) ([]byte, error) {
	encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	if encoding == "" {
		return readBounded(response.Body, contract.MaxResponseBytes)
	}
	if encoding != "gzip" {
		return nil, errors.New("unsupported Slack content encoding")
	}
	compressed, err := readBounded(response.Body, contract.MaxCompressedResponseBytes)
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, errors.New("invalid compressed Slack response")
	}
	defer func() { _ = reader.Close() }()
	return readBounded(reader, contract.MaxResponseBytes)
}

func readBounded(reader io.Reader, maximum int) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maximum {
		return nil, errors.New("slack response exceeds v1 bound")
	}
	return payload, nil
}

func routeAllowed(route string) bool {
	for _, allowed := range contract.Routes() {
		if route == allowed {
			return true
		}
	}
	return false
}

func validateResponseCount(count, requested int) error {
	if count > requested {
		return errx.New(errx.Internal, "SLACK_RESPONSE_COUNT_EXCEEDED", "Slack returned more items than the bounded request permits", "report this response contract violation")
	}
	return nil
}

func validateRequestedLimit(limit int) error {
	if limit < contract.MinCollectionLimit || limit > contract.MaxCollectionLimit {
		return errx.New(errx.Internal, "INVALID_INTERNAL_LIMIT", "Slack request limit is outside the v1 bound", "report this defect")
	}
	return nil
}

func networkError(class operationClass, stage errx.Stage, cause error) error {
	if class == writeOperation {
		return errx.New(errx.Conflict, "WRITE_OUTCOME_UNKNOWN", "Slack write outcome is unknown", "reconcile with a bounded read; never retry automatically").WithStage(stage).Wrap(cause)
	}
	return errx.New(errx.Retryable, "SLACK_READ_FAILED", "Slack read failed safely", "retry the bounded read with backoff").WithStage(stage).Wrap(cause)
}

func retryAfterError(value string, class operationClass) error {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < contract.MinRetryAfterSeconds || seconds > contract.MaxRetryAfterSeconds {
		return networkError(class, errx.StageRateLimitResponse, errors.New("invalid Retry-After"))
	}
	if class == writeOperation {
		return networkError(class, errx.StageRateLimitResponse, errors.New("write rate limited after dispatch"))
	}
	result := errx.New(errx.Retryable, "SLACK_RATE_LIMITED", "Slack rate limited the read", "wait retry_after_seconds, then retry the same bounded read")
	result.RetryAfter = time.Duration(seconds) * time.Second
	return result
}

func slackError(code string, class operationClass) error {
	if class == writeOperation {
		switch code {
		case "invalid_auth", "account_inactive", "token_revoked", "not_authed":
			return errx.New(errx.Auth, "SLACK_AUTH_REJECTED", "Slack rejected the credential before committing the message", "login or rotate this profile")
		case "channel_not_found", "is_archived", "not_in_channel":
			return errx.New(errx.NotFound, "SLACK_WRITE_TARGET_UNAVAILABLE", "Slack rejected the target before committing the message", "re-read the exact target and policy")
		case "missing_scope", "not_allowed_token_type", "restricted_action", "access_denied":
			return errx.New(errx.PermissionDenied, "SLACK_WRITE_PERMISSION_DENIED", "Slack denied the write before committing the message", "request the required Slack scope or permission")
		case "msg_too_long", "no_text", "invalid_blocks":
			return errx.New(errx.Usage, "SLACK_WRITE_REJECTED", "Slack rejected the message before committing it", "fix the bounded message input")
		default:
			return networkError(class, errx.StageAPIError, fmt.Errorf("slack write error class %s", safeSlackCode(code)))
		}
	}
	switch code {
	case "invalid_auth", "account_inactive", "token_revoked", "not_authed":
		return errx.New(errx.Auth, "SLACK_AUTH_REJECTED", "Slack rejected the credential", "login or rotate this profile")
	case "channel_not_found", "user_not_found", "message_not_found", "thread_not_found":
		return errx.New(errx.NotFound, "SLACK_OBJECT_NOT_FOUND", "Slack object was not found or is not visible", "verify the exact ID and profile")
	case "missing_scope", "not_allowed_token_type", "restricted_action", "access_denied":
		return errx.New(errx.PermissionDenied, "SLACK_PERMISSION_DENIED", "Slack denied the operation", "request the required Slack scope or permission")
	case "ratelimited", "internal_error", "fatal_error":
		return errx.New(errx.Retryable, "SLACK_READ_RETRYABLE", "Slack could not complete the read", "retry the bounded read with backoff").WithStage(errx.StageAPIError)
	default:
		return errx.New(errx.Internal, "SLACK_API_ERROR", "Slack returned an unsupported API error", "report the error class without retrying unchanged")
	}
}

func safeSlackCode(code string) string {
	if len(code) > 64 {
		return "unknown"
	}
	for _, r := range code {
		if !(r == '_' || r >= 'a' && r <= 'z') {
			return "unknown"
		}
	}
	return code
}
