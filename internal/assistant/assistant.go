// Package assistant is the AI campaign composer: a chat in which a language model (Claude, or
// an offline mock) creates, changes, estimates and asks to dispatch campaigns through a small,
// fixed set of tools.
//
// The model only proposes. Every tool call is validated here and runs through the campaign
// and segmentation services, so their rules apply unchanged; the model sees campaign settings
// and aggregate counts, never customers. A dispatch the model asks for waits for a person's
// approval, given through Decide: no tool and no chat message can approve it.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	_ "time/tzdata" // users' time zones, in an image without zoneinfo
	"unicode/utf8"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/delivery"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

// Limits that keep a conversation bounded.
const (
	maxMessageLength = 2000 // characters in a user message
	maxUserTurns     = 30   // user messages in a conversation
	maxModelCalls    = 5    // model replies per user message; the last one runs no tools
	maxToolCalls     = 10   // tool calls per user message
	turnTimeout      = 90 * time.Second
)

// Errors a turn or a decision can end with.
var (
	ErrConversationNotFound = errors.New("conversation not found or expired")
	ErrConversationBusy     = errors.New("conversation is answering another message")
	ErrConversationFull     = errors.New("conversation has reached its length limit")
	ErrTooManyConversations = errors.New("every conversation is answering a message")
	ErrInvalidMessage       = errors.New("message is empty or too long")
	ErrApprovalNotFound     = errors.New("dispatch request not found or expired")
	ErrApprovalClosed       = errors.New("dispatch request already decided")
)

// Model is the language model behind the assistant: Claude (NewClaude) or the offline mock
// (NewMock).
type Model interface {
	// reply continues history, which ends with a user message, with the model's next message.
	reply(ctx context.Context, system string, history []message) (message, error)
}

// message is one message of a conversation. A user message holds the person's text or the
// results of the tool calls in the assistant message before it; an assistant message holds
// the model's text and tool calls.
type message struct {
	assistant bool
	text      string
	calls     []toolCall
	results   []toolResult
	refused   bool // the model declined to answer
	native    any  // the assistant message as the model returned it, sent back unchanged
}

// toolCall is a tool the model asked to run, with its arguments as the model wrote them.
type toolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// toolResult is what a tool call returned to the model: JSON, possibly describing an error.
type toolResult struct {
	CallID  string
	Content string
	IsError bool
}

// campaigns is what the assistant needs from the campaign service.
type campaigns interface {
	Create(ctx context.Context, d campaign.Draft) (*campaign.Campaign, error)
	Update(ctx context.Context, id int64, d campaign.Draft) (*campaign.Campaign, error)
	Get(ctx context.Context, id int64) (*campaign.Campaign, error)
	List(ctx context.Context) ([]*campaign.Campaign, error)
	DispatchApproved(ctx context.Context, id int64, approved time.Time, scheduledAt *time.Time) (*campaign.Campaign, error)
}

// audiences counts the customers a segment reaches and lists what segments can name: the
// segmentation service.
type audiences interface {
	CountAudience(ctx context.Context, q segmentation.Query) (segmentation.Audience, error)
	Options(ctx context.Context) (segmentation.Options, error)
}

// progressReader reads a dispatched campaign's delivery counts: delivery.ReadProgress.
type progressReader func(ctx context.Context, c *campaign.Campaign) (delivery.Progress, error)

// Service runs conversations with the model and the tools it calls.
type Service struct {
	campaigns campaigns
	audiences audiences
	progress  progressReader
	model     Model
	store     *store
	now       func() time.Time
	logger    *slog.Logger
}

// NewService returns an assistant that talks to model and acts through the given services.
func NewService(cs campaigns, as audiences, progress progressReader, model Model, now func() time.Time, logger *slog.Logger) *Service {
	return &Service{campaigns: cs, audiences: as, progress: progress, model: model, store: newStore(), now: now, logger: logger}
}

// Turn is the assistant's answer to one user message.
type Turn struct {
	ConversationID string   `json:"conversation_id"`
	Reply          string   `json:"reply"`
	Notice         string   `json:"notice,omitempty"` // "refused", "step_limit" or "interrupted": the app explains it
	Actions        []Action `json:"actions"`
}

// Action is something a tool did that the chat shows as a card, as data rather than words.
type Action struct {
	Type     string                 `json:"type"` // campaign_created, campaign_updated, audience_estimated, dispatch_requested
	Campaign *campaign.View         `json:"campaign,omitempty"`
	Audience *segmentation.Audience `json:"audience,omitempty"`
	Approval *Approval              `json:"approval,omitempty"`
}

// changes reports whether the action changed something: a campaign, or a dispatch request
// now awaiting approval. An estimate only reads.
func (a Action) changes() bool { return a.Type != "audience_estimated" }

// Approval is a dispatch waiting for a person's decision.
type Approval struct {
	ID          string     `json:"id"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	Reach       int        `json:"reach"` // customers reachable on the campaign's channels now
}

// Chat answers message in a conversation, starting one when conversationID is empty. The
// model may call tools, at most maxToolCalls of them over maxModelCalls replies; their cards
// come back with the reply. timeZone (IANA, e.g. "Europe/Istanbul") tells the model the
// user's time.
//
// A turn that fails before any tool changed something leaves the conversation as it was. Once
// one has, the turn is kept even if the model then fails: it ends with the notice
// "interrupted" and its cards, and the model sees what was done with the next message.
func (s *Service) Chat(ctx context.Context, conversationID, text, timeZone string) (Turn, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxMessageLength {
		return Turn{}, ErrInvalidMessage
	}
	conv, err := s.store.open(conversationID, s.now())
	if err != nil {
		return Turn{}, err
	}
	defer func() { s.store.release(conv, s.now()) }() // a closure: the time the turn ends
	if conv.turns >= maxUserTurns {
		return Turn{}, ErrConversationFull
	}

	ctx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	system, err := s.systemPrompt(ctx)
	if err != nil {
		return Turn{}, err
	}

	started := time.Now()
	before := len(conv.history)
	conv.history = append(conv.history, message{text: timeContext(s.now(), timeZone) + text})
	turn := Turn{ConversationID: conv.id, Actions: []Action{}}
	changed := false
	modelCalls, toolCalls := 0, 0
	for {
		// The reply that reaches a limit runs no tools: the model would not see their results.
		last := modelCalls == maxModelCalls-1 || toolCalls >= maxToolCalls
		modelCalls++
		reply, err := s.model.reply(ctx, system, conv.history)
		if err != nil {
			s.logModelError(ctx, err)
			if !changed {
				conv.history = conv.history[:before]
				return Turn{}, err
			}
			turn.Notice = "interrupted" // the history ends with the tool results, which the next message follows
			break
		}
		conv.history = append(conv.history, reply)
		if reply.refused {
			turn.Notice = "refused"
			break
		}
		if len(reply.calls) == 0 {
			turn.Reply = reply.text
			break
		}
		results := make([]toolResult, 0, len(reply.calls))
		for _, call := range reply.calls {
			if last || toolCalls == maxToolCalls {
				results = append(results, failure(call, "too_many_tool_calls", nil))
				continue
			}
			toolCalls++
			result, action := s.run(ctx, conv.id, call)
			results = append(results, result)
			if action != nil {
				turn.Actions = append(turn.Actions, *action)
				changed = changed || action.changes()
			}
		}
		conv.history = append(conv.history, message{results: results})
		if last {
			turn.Notice = "step_limit"
			break
		}
	}
	conv.turns++
	s.logger.InfoContext(ctx, "assistant turn",
		slog.Int("model_calls", modelCalls),
		slog.Int("tool_calls", toolCalls),
		slog.String("notice", turn.Notice),
		slog.Duration("duration", time.Since(started)),
	)
	return turn, nil
}

// Decision is the outcome of a person's decision on a dispatch request.
type Decision struct {
	Status   string         `json:"status"` // "dispatched" or "cancelled"
	Campaign *campaign.View `json:"campaign,omitempty"`
}

// Decide records a person's decision on dispatch request approvalID. Either way the request
// closes first, so it is used at most once. Approving dispatches the campaign through the
// campaign service, and only as it was when the request was made: a draft changed since gets
// campaign.ErrChanged, checked in the transaction that dispatches it.
func (s *Service) Decide(ctx context.Context, approvalID string, approve bool) (Decision, error) {
	a, err := s.store.closeApproval(approvalID, s.now())
	if err != nil {
		return Decision{}, err
	}
	if !approve {
		return Decision{Status: "cancelled"}, nil
	}
	c, err := s.campaigns.DispatchApproved(ctx, a.campaignID, a.version, a.scheduledAt)
	if err != nil {
		s.logger.WarnContext(ctx, "assistant dispatch approval failed",
			slog.Int64("campaign_id", a.campaignID),
			slog.String("error_type", errorCode(err)),
		)
		return Decision{}, err
	}
	view := campaign.NewView(c)
	return Decision{Status: "dispatched", Campaign: &view}, nil
}

// timeContext is the line that starts every user message: the user's current time, so the
// model can schedule "tomorrow at 9". An unknown time zone falls back to UTC.
func timeContext(now time.Time, timeZone string) string {
	loc, err := time.LoadLocation(timeZone)
	if err != nil || timeZone == "" {
		loc = time.UTC
	}
	return "[Current time: " + now.In(loc).Format("Monday 2006-01-02 15:04 -07:00") + " (" + loc.String() + ")]\n\n"
}

func (s *Service) logModelError(ctx context.Context, err error) {
	attrs := []any{slog.String("error_type", errorCode(err))}
	var me *modelError
	if errors.As(err, &me) {
		attrs = append(attrs, slog.Int("status", me.status), slog.String("request_id", me.requestID))
	}
	s.logger.WarnContext(ctx, "assistant model call failed", attrs...)
}
