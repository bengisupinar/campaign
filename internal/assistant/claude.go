package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

const (
	claudeMaxTokens      = 4096
	claudeRequestTimeout = 60 * time.Second // per attempt; the SDK retries 429, 5xx and network errors twice
)

// claude is the Anthropic Messages API, with the assistant's tools.
type claude struct {
	client anthropic.Client
	model  string
	tools  []anthropic.ToolUnionParam
}

// NewClaude returns the Claude model, authenticated with apiKey. opts adjust the client, e.g.
// its base URL in tests.
func NewClaude(apiKey, model string, opts ...option.RequestOption) (Model, error) {
	definitions, defs, err := toolDefinitions()
	if err != nil {
		return nil, err
	}
	tools := make([]anthropic.ToolUnionParam, len(definitions))
	for i, d := range definitions {
		tools[i] = anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        d.Name,
			Description: anthropic.String(d.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties:  d.Properties,
				Required:    d.Required,
				ExtraFields: map[string]any{"additionalProperties": false, "$defs": defs},
			},
		}}
	}
	options := append([]option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithRequestTimeout(claudeRequestTimeout),
		option.WithMaxRetries(2),
	}, opts...)
	return &claude{client: anthropic.NewClient(options...), model: model, tools: tools}, nil
}

func (c *claude) reply(ctx context.Context, system string, history []message) (message, error) {
	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: claudeMaxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Tools:     c.tools,
		Messages:  toClaude(history),
	})
	if err != nil {
		return message{}, classify(ctx, err)
	}
	m := message{assistant: true, native: resp.ToParam(), refused: resp.StopReason == anthropic.StopReasonRefusal}
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			m.text += b.Text
		case anthropic.ToolUseBlock:
			m.calls = append(m.calls, toolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	return m, nil
}

// toClaude turns the history into API messages. Assistant messages go back exactly as Claude
// returned them.
func toClaude(history []message) []anthropic.MessageParam {
	params := make([]anthropic.MessageParam, 0, len(history))
	for _, m := range history {
		if native, ok := m.native.(anthropic.MessageParam); ok {
			params = append(params, native)
			continue
		}
		var blocks []anthropic.ContentBlockParamUnion
		for _, r := range m.results {
			blocks = append(blocks, anthropic.NewToolResultBlock(r.CallID, r.Content, r.IsError))
		}
		if m.text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.text))
		}
		params = append(params, anthropic.NewUserMessage(blocks...))
	}
	return params
}

// modelError is a failed model call: a code for the app, and what may be logged about it.
type modelError struct {
	code      string // assistant_busy, assistant_timeout or assistant_unavailable
	status    int
	requestID string
	err       error
}

func (e *modelError) Error() string {
	return fmt.Sprintf("%s (status %d)", e.code, e.status)
}

func (e *modelError) Unwrap() error { return e.err }

// classify sorts an API failure by what the user can do about it: wait (busy, timeout) or
// nothing (unavailable). The response body isn't kept, so logs can't echo request content.
func classify(ctx context.Context, err error) error {
	me := &modelError{code: "assistant_unavailable"}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		me.status, me.requestID = apiErr.StatusCode, apiErr.RequestID
	}
	switch {
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
		me.code = "assistant_timeout"
	case me.status == http.StatusTooManyRequests || me.status == 529: // 529: overloaded
		me.code = "assistant_busy"
	}
	if apiErr == nil {
		me.err = err // a network error: no API body to leak
	}
	return me
}
