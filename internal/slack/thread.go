package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

type threadContinuation struct {
	Version  int    `json:"v"`
	Upstream string `json:"upstream"`
	After    string `json:"after"`
}

func invalidThreadCursor() error {
	return errx.New(errx.Usage, "INVALID_CURSOR", "thread continuation is invalid", "use the bound cursor from the preceding thread page")
}

func invalidThreadPage() error {
	return errx.New(errx.Internal, "SLACK_RESPONSE_INVALID", "Slack returned an invalid thread page", "report this response contract violation")
}

func validThreadTimestamp(value string) bool {
	return len(value) <= contract.MaxSlackTimestampBytes && validSlackTimestamp(value)
}

func compareThreadTimestamps(left, right string) int {
	wholeA, fractionA, _ := strings.Cut(left, ".")
	wholeB, fractionB, _ := strings.Cut(right, ".")
	wholeA, wholeB = strings.TrimLeft(wholeA, "0"), strings.TrimLeft(wholeB, "0")
	if len(wholeA) < len(wholeB) {
		return -1
	}
	if len(wholeA) > len(wholeB) {
		return 1
	}
	if order := strings.Compare(wholeA, wholeB); order != 0 {
		return order
	}
	width := max(len(fractionA), len(fractionB))
	return strings.Compare(fractionA+strings.Repeat("0", width-len(fractionA)), fractionB+strings.Repeat("0", width-len(fractionB)))
}

func validNativeThreadCursor(value string) bool {
	return len(value) <= contract.MaxCursorBytes && !strings.ContainsAny(value, "\r\n\x00")
}

func readThreadContinuation(options ThreadOptions) (threadContinuation, error) {
	state := threadContinuation{Version: 1, Upstream: options.Cursor}
	if !validNativeThreadCursor(options.Cursor) || (options.Cursor != "" && options.PageCursor != "") {
		return state, invalidThreadCursor()
	}
	if options.PageCursor == "" {
		return state, nil
	}
	if len(options.PageCursor) > contract.MaxCursorBytes || json.Unmarshal([]byte(options.PageCursor), &state) != nil {
		return state, invalidThreadCursor()
	}
	canonical, err := json.Marshal(state)
	if err != nil || string(canonical) != options.PageCursor || state.Version != 1 ||
		!validNativeThreadCursor(state.Upstream) || (state.After != "" && !validThreadTimestamp(state.After)) ||
		(state.Upstream == "" && state.After == "") {
		return state, invalidThreadCursor()
	}
	return state, nil
}

func validateThreadMessages(messages []Message, parent string, initial bool) error {
	if !validThreadTimestamp(parent) || (initial && (len(messages) == 0 || messages[0].TS != parent)) {
		return invalidThreadPage()
	}
	for index, message := range messages {
		if !validThreadTimestamp(message.TS) || compareThreadTimestamps(message.TS, parent) < 0 ||
			(index > 0 && compareThreadTimestamps(messages[index-1].TS, message.TS) >= 0) {
			return invalidThreadPage()
		}
		if message.TS == parent {
			if index != 0 || (message.ThreadTS != "" && message.ThreadTS != parent) {
				return invalidThreadPage()
			}
		} else if message.ThreadTS != parent {
			return invalidThreadPage()
		}
	}
	return nil
}

func (c *Client) threadPage(ctx context.Context, token Token, options ThreadOptions) (MessagePage, error) {
	state, err := readThreadContinuation(options)
	if err != nil {
		return MessagePage{}, err
	}
	values := url.Values{"channel": {options.ConversationID}, "ts": {options.ThreadTS}, "limit": {strconv.Itoa(options.Limit)}}
	if state.Upstream != "" {
		values.Set("cursor", state.Upstream)
	}
	var response apiEnvelope
	if err := c.call(ctx, token, http.MethodGet, "conversations.replies", values, &response, readOperation); err != nil {
		return MessagePage{}, err
	}
	if err := validateResponseCount(len(response.Messages), contract.MaxCollectionLimit); err != nil {
		return MessagePage{}, err
	}
	initial := options.Cursor == "" && options.PageCursor == ""
	if err := validateThreadMessages(response.Messages, options.ThreadTS, initial); err != nil {
		return MessagePage{}, err
	}
	upstream := response.ResponseMetadata.NextCursor
	if !validNativeThreadCursor(upstream) || (upstream != "" && upstream == state.Upstream) {
		return MessagePage{}, invalidThreadPage()
	}
	remaining := make([]Message, 0, len(response.Messages))
	for _, message := range response.Messages {
		if (!initial && message.TS == options.ThreadTS) || (state.After != "" && compareThreadTimestamps(message.TS, state.After) <= 0) {
			continue
		}
		remaining = append(remaining, message)
	}
	count := min(len(remaining), options.Limit)
	result := MessagePage{Messages: remaining[:count]}
	next := state
	if count > 0 {
		next.After = result.Messages[count-1].TS
	}
	if len(remaining) <= options.Limit {
		if upstream == "" {
			return result, nil
		}
		next.Upstream = upstream
	}
	// Refetch the same native page until its overflow is consumed; advancing
	// Slack's cursor here would skip messages that were not emitted yet.
	if next == state {
		return MessagePage{}, invalidThreadPage()
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return MessagePage{}, err
	}
	if len(encoded) > contract.MaxCursorBytes {
		return MessagePage{}, invalidThreadPage()
	}
	result.NextCursor = string(encoded)
	return result, nil
}
