package assistant

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

// toolsJSON describes the tools to the model: names, descriptions and JSON schemas. The
// schemas guide the model; run is what enforces them, and it runs nothing else.
//
//go:embed tools.json
var toolsJSON []byte

// toolDefinition is one tool as tools.json describes it.
type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Properties  map[string]any `json:"properties"`
	Required    []string       `json:"required"`
}

// toolDefinitions returns the tools and the schema definitions ($defs) they refer to.
func toolDefinitions() ([]toolDefinition, map[string]any, error) {
	var file struct {
		Defs  map[string]any   `json:"$defs"`
		Tools []toolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(toolsJSON, &file); err != nil {
		return nil, nil, fmt.Errorf("read tools.json: %w", err)
	}
	return file.Tools, file.Defs, nil
}

// Tool arguments. Decoding rejects any field they don't have.
type (
	createArgs struct {
		Name    string           `json:"name"`
		Segment campaign.Segment `json:"segment"`
		Content campaign.Content `json:"content"`
	}
	updateArgs struct {
		CampaignID int64             `json:"campaign_id"`
		Name       *string           `json:"name"`
		Segment    *campaign.Segment `json:"segment"`
		Content    *campaign.Content `json:"content"`
	}
	campaignArgs struct {
		CampaignID int64 `json:"campaign_id"`
	}
	estimateArgs struct {
		CampaignID int64             `json:"campaign_id"`
		Segment    *campaign.Segment `json:"segment"`
	}
	dispatchArgs struct {
		CampaignID  int64      `json:"campaign_id"`
		ScheduledAt *time.Time `json:"scheduled_at"`
	}
)

// summary is a campaign in a list: no segment or texts.
type summary struct {
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	Status    campaign.Status    `json:"status"`
	Channels  []customer.Channel `json:"channels"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// run validates and runs one tool call, and returns its result for the model and, for
// changes and estimates, a card for the chat. A call it can't validate runs nothing.
func (s *Service) run(ctx context.Context, conversationID string, call toolCall) (toolResult, *Action) {
	switch call.Name {
	case "create_campaign":
		var args createArgs
		if err := decode(call.Input, &args); err != nil {
			return s.reject(ctx, call, err)
		}
		c, err := s.campaigns.Create(ctx, campaign.Draft{Name: args.Name, Segment: args.Segment, Content: args.Content})
		if err != nil {
			return s.failed(ctx, call, 0, err), nil
		}
		view := campaign.NewView(c)
		return success(call, map[string]any{"campaign": view}), &Action{Type: "campaign_created", Campaign: &view}

	case "update_campaign":
		var args updateArgs
		if err := decode(call.Input, &args); err != nil {
			return s.reject(ctx, call, err)
		}
		if args.CampaignID <= 0 {
			return s.reject(ctx, call, missing("campaign_id"))
		}
		if args.Name == nil && args.Segment == nil && args.Content == nil {
			return s.reject(ctx, call, errors.New("give at least one of name, segment and content"))
		}
		c, err := s.campaigns.Get(ctx, args.CampaignID)
		if err != nil {
			return s.failed(ctx, call, args.CampaignID, err), nil
		}
		d := c.Draft()
		if args.Name != nil {
			d.Name = *args.Name
		}
		if args.Segment != nil {
			d.Segment = *args.Segment
		}
		if args.Content != nil {
			d.Content = *args.Content
		}
		if c, err = s.campaigns.Update(ctx, args.CampaignID, d); err != nil {
			return s.failed(ctx, call, args.CampaignID, err), nil
		}
		view := campaign.NewView(c)
		return success(call, map[string]any{"campaign": view}), &Action{Type: "campaign_updated", Campaign: &view}

	case "get_campaign":
		var args campaignArgs
		if err := decode(call.Input, &args); err != nil {
			return s.reject(ctx, call, err)
		}
		if args.CampaignID <= 0 {
			return s.reject(ctx, call, missing("campaign_id"))
		}
		c, err := s.campaigns.Get(ctx, args.CampaignID)
		if err != nil {
			return s.failed(ctx, call, args.CampaignID, err), nil
		}
		result := map[string]any{"campaign": campaign.NewView(c)}
		if c.Status() != campaign.StatusDraft {
			progress, err := s.progress(ctx, c)
			if err != nil {
				return s.failed(ctx, call, args.CampaignID, err), nil
			}
			result["delivery"] = progress
		}
		return success(call, result), nil

	case "list_campaigns":
		var args struct{}
		if err := decode(call.Input, &args); err != nil {
			return s.reject(ctx, call, err)
		}
		list, err := s.campaigns.List(ctx)
		if err != nil {
			return s.failed(ctx, call, 0, err), nil
		}
		summaries := make([]summary, len(list))
		for i, c := range list {
			summaries[i] = summary{ID: c.ID(), Name: c.Draft().Name, Status: c.Status(), Channels: c.Channels(), UpdatedAt: c.UpdatedAt()}
		}
		return success(call, map[string]any{"campaigns": summaries}), nil

	case "estimate_audience":
		var args estimateArgs
		if err := decode(call.Input, &args); err != nil {
			return s.reject(ctx, call, err)
		}
		if (args.Segment == nil) == (args.CampaignID <= 0) {
			return s.reject(ctx, call, errors.New("give either segment or campaign_id"))
		}
		var view *campaign.View
		segment := args.Segment
		if segment == nil {
			c, err := s.campaigns.Get(ctx, args.CampaignID)
			if err != nil {
				return s.failed(ctx, call, args.CampaignID, err), nil
			}
			v := campaign.NewView(c)
			view, segment = &v, &v.Segment
		}
		audience, err := s.audiences.CountAudience(ctx, segmentation.Query{
			Segment:  *segment,
			Channels: []customer.Channel{customer.ChannelEmail, customer.ChannelSMS},
			AsOf:     s.now(),
		})
		if err != nil {
			return s.failed(ctx, call, args.CampaignID, err), nil
		}
		return success(call, audience), &Action{Type: "audience_estimated", Campaign: view, Audience: &audience}

	case "dispatch_campaign":
		var args dispatchArgs
		if err := decode(call.Input, &args); err != nil {
			return s.reject(ctx, call, err)
		}
		if args.CampaignID <= 0 {
			return s.reject(ctx, call, missing("campaign_id"))
		}
		return s.requestDispatch(ctx, conversationID, call, args)

	default:
		s.logger.WarnContext(ctx, "assistant tool call rejected",
			slog.String("tool", call.Name), slog.String("reason", "unknown_tool"))
		return failure(call, "unknown_tool", nil), nil
	}
}

// requestDispatch checks that the campaign could be dispatched as asked, without saving
// anything, and records a request for the user to approve or cancel. Nothing is sent here.
func (s *Service) requestDispatch(ctx context.Context, conversationID string, call toolCall, args dispatchArgs) (toolResult, *Action) {
	c, err := s.campaigns.Get(ctx, args.CampaignID)
	if err != nil {
		return s.failed(ctx, call, args.CampaignID, err), nil
	}
	view := campaign.NewView(c)
	// The domain's own check, on this copy only: a draft, and a time in the allowed window.
	if err := c.Dispatch(args.ScheduledAt, s.now().UTC().Truncate(time.Microsecond)); err != nil {
		return s.failed(ctx, call, args.CampaignID, err), nil
	}
	audience, err := s.audiences.CountAudience(ctx, segmentation.Query{
		Segment:  view.Segment,
		Channels: view.Channels,
		AsOf:     s.now(),
	})
	if err != nil {
		return s.failed(ctx, call, args.CampaignID, err), nil
	}
	var at *time.Time
	if args.ScheduledAt != nil {
		t := args.ScheduledAt.UTC()
		at = &t
	}
	id := s.store.addApproval(approval{
		conversationID: conversationID,
		campaignID:     view.ID,
		version:        view.UpdatedAt,
		scheduledAt:    at,
		created:        s.now(),
	})
	result := map[string]any{
		"status":       "awaiting_user_approval",
		"campaign_id":  view.ID,
		"scheduled_at": at,
		"reach":        audience.Reachable,
		"note":         "The app now shows the user a card to approve or cancel this dispatch. Nothing is sent unless they approve it there.",
	}
	return success(call, result), &Action{
		Type:     "dispatch_requested",
		Campaign: &view,
		Approval: &Approval{ID: id, ScheduledAt: at, Reach: audience.Reachable},
	}
}

// decode reads tool arguments strictly: one JSON object with only the fields dst has.
func decode(input json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("more than one JSON value")
	}
	return nil
}

func missing(field string) error {
	return fmt.Errorf("%s is required", field)
}

// reject answers a call whose arguments don't validate: nothing ran.
func (s *Service) reject(ctx context.Context, call toolCall, err error) (toolResult, *Action) {
	s.logger.WarnContext(ctx, "assistant tool call rejected",
		slog.String("tool", call.Name), slog.String("reason", "invalid_arguments"))
	return failure(call, "invalid_arguments", map[string]any{"detail": err.Error()}), nil
}

// failed answers a call the services refused or couldn't run. Their problems go back to the
// model, which can fix the fields they name; unexpected errors are logged and not described.
func (s *Service) failed(ctx context.Context, call toolCall, campaignID int64, err error) toolResult {
	code := errorCode(err)
	var invalid *campaign.ValidationError
	if errors.As(err, &invalid) {
		return failure(call, code, map[string]any{"problems": invalid.Problems})
	}
	if code == "internal" {
		s.logger.ErrorContext(ctx, "assistant tool failed",
			slog.String("tool", call.Name), slog.Int64("campaign_id", campaignID), slog.Any("error", err))
	}
	return failure(call, code, nil)
}

// errorCode names an error with the code the API and the chat use for it.
func errorCode(err error) string {
	var invalid *campaign.ValidationError
	var me *modelError
	switch {
	case errors.As(err, &invalid):
		return "invalid_campaign"
	case errors.Is(err, campaign.ErrNotFound):
		return "not_found"
	case errors.Is(err, campaign.ErrNotDraft):
		return "not_draft"
	case errors.Is(err, campaign.ErrAlreadyDispatched):
		return "already_dispatched"
	case errors.Is(err, campaign.ErrChanged):
		return "campaign_changed"
	case errors.As(err, &me):
		return me.code
	default:
		return "internal"
	}
}

func success(call toolCall, v any) toolResult {
	data, err := json.Marshal(v)
	if err != nil {
		return failure(call, "internal", nil)
	}
	return toolResult{CallID: call.ID, Content: string(data)}
}

func failure(call toolCall, code string, extra map[string]any) toolResult {
	body := map[string]any{"error": code}
	for k, v := range extra {
		body[k] = v
	}
	data, _ := json.Marshal(body) // a map of strings and problems always encodes
	return toolResult{CallID: call.ID, Content: string(data), IsError: true}
}
