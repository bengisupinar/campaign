package assistant

import (
	"context"
	"encoding/json"
	"fmt"
)

// mock is the offline model (LLM_PROVIDER=mock). It ignores what is written and plays a fixed
// script, one step per user message, so the whole flow can be tried without an API key:
// create a demo draft, estimate it, ask to dispatch it, then explain itself.
type mock struct{}

// NewMock returns the offline model.
func NewMock() Model { return mock{} }

const mockDraft = `{
	"name": "Demo: İstanbul baskets",
	"segment": {"version": 1, "match": "all", "conditions": [
		{"type": "city", "in": ["İstanbul"]},
		{"type": "basket", "within_days": 30}
	]},
	"content": {"sms": {"body": "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor!"}}
}`

func (mock) reply(_ context.Context, _ string, history []message) (message, error) {
	last := history[len(history)-1]
	step := 0
	for _, m := range history {
		if !m.assistant && len(m.results) == 0 {
			step++
		}
	}
	if len(last.results) > 0 { // the step's tool ran: say so
		if last.results[0].IsError {
			return message{assistant: true, text: "The tool reported a problem: " + last.results[0].Content}, nil
		}
		return message{assistant: true, text: mockReports[min(step, len(mockReports))-1]}, nil
	}
	id := mockCampaignID(history)
	switch {
	case step == 1:
		return mockCall("create_campaign", mockDraft), nil
	case step == 2 && id > 0:
		return mockCall("estimate_audience", fmt.Sprintf(`{"campaign_id": %d}`, id)), nil
	case step == 3 && id > 0:
		return mockCall("dispatch_campaign", fmt.Sprintf(`{"campaign_id": %d}`, id)), nil
	}
	return message{assistant: true, text: mockHelp}, nil
}

var mockReports = []string{
	"I created a demo draft. Send another message and I'll estimate its audience.",
	"Here is its audience. Send another message and I'll ask to send it.",
	"Approve or cancel the dispatch on the card.",
}

const mockHelp = "This is the offline mock assistant: it plays a fixed script and doesn't read your messages. " +
	"Start a new conversation to run it again, or set LLM_PROVIDER=claude and ANTHROPIC_API_KEY to chat with Claude."

func mockCall(name, input string) message {
	return message{assistant: true, calls: []toolCall{{ID: "mock_" + name, Name: name, Input: json.RawMessage(input)}}}
}

// mockCampaignID finds the campaign the script created, in its tool result.
func mockCampaignID(history []message) int64 {
	for _, m := range history {
		for _, r := range m.results {
			var created struct {
				Campaign struct {
					ID int64 `json:"id"`
				} `json:"campaign"`
			}
			if r.CallID == "mock_create_campaign" && !r.IsError && json.Unmarshal([]byte(r.Content), &created) == nil {
				return created.Campaign.ID
			}
		}
	}
	return 0
}
