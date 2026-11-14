package segmentation

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
)

// asOf is the reference time of the tests: windows end here.
var asOf = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// daysAgo returns the time n days before asOf; a negative n is after it.
func daysAgo(n int) time.Time { return asOf.Add(-time.Duration(n) * 24 * time.Hour) }

func segment(match campaign.Match, conditions ...campaign.Condition) campaign.Segment {
	return campaign.Segment{Version: campaign.SegmentVersion, Match: match, Conditions: conditions}
}

func TestCompileSegment(t *testing.T) {
	const (
		cities    = "c.city_id IN (SELECT id FROM cities WHERE name IN ?)"
		products  = "(SELECT id FROM products WHERE category_id IN (SELECT id FROM categories WHERE name IN ?))"
		completed = "o.customer_id = c.id AND o.status = ?"
		spend     = "(SELECT COALESCE(sum(i.quantity * i.unit_price_minor), 0) FROM orders o JOIN order_items i ON i.order_id = o.id WHERE "
	)
	electronics := []string{"Electronics"}
	tests := []struct {
		name     string
		segment  campaign.Segment
		wantSQL  string
		wantArgs []any
	}{
		{name: "empty segment", segment: segment(campaign.MatchAll), wantSQL: "TRUE"},
		{name: "empty segment matching any", segment: segment(campaign.MatchAny), wantSQL: "TRUE"},
		{name: "country",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionCountry, In: []string{"TR", "DE"}}),
			wantSQL:  "(c.city_id IN (SELECT id FROM cities WHERE country_code IN ?))",
			wantArgs: []any{[]string{"TR", "DE"}}},
		{name: "negated city",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionCity, In: []string{"İstanbul"}, Negate: true}),
			wantSQL:  "(NOT (" + cities + "))",
			wantArgs: []any{[]string{"İstanbul"}}},
		{name: "language",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionLanguage, In: []string{"en"}}),
			wantSQL:  "(c.preferred_language IN ?)",
			wantArgs: []any{[]string{"en"}}},
		{name: "signed up in the last 30 days",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionSignedUp, WithinDays: new(30)}),
			wantSQL:  "(c.created_at BETWEEN ? AND ?)",
			wantArgs: []any{daysAgo(30), asOf}},
		{name: "any basket line, at any time",
			segment: segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionBasket}),
			wantSQL: "(EXISTS (SELECT 1 FROM basket_items b WHERE b.customer_id = c.id))"},
		{name: "basket window and categories on the same line",
			segment: segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionBasket, WithinDays: new(30), Categories: electronics}),
			wantSQL: "(EXISTS (SELECT 1 FROM basket_items b " +
				"WHERE b.customer_id = c.id AND b.added_at BETWEEN ? AND ? AND b.product_id IN " + products + "))",
			wantArgs: []any{daysAgo(30), asOf, electronics}},
		{name: "purchased, ever",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionPurchased}),
			wantSQL:  "(EXISTS (SELECT 1 FROM orders o WHERE " + completed + "))",
			wantArgs: []any{"completed"}},
		{name: "purchase window and categories on the same order",
			segment: segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionPurchased, WithinDays: new(30), Categories: electronics}),
			wantSQL: "(EXISTS (SELECT 1 FROM orders o WHERE " + completed + " AND o.ordered_at BETWEEN ? AND ? AND " +
				"EXISTS (SELECT 1 FROM order_items i WHERE i.order_id = o.id AND i.product_id IN " + products + ")))",
			wantArgs: []any{"completed", daysAgo(30), asOf, electronics}},
		{name: "minimum order count",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionOrderCount, Min: new(10)}),
			wantSQL:  "((SELECT count(*) FROM orders o WHERE " + completed + ") >= ?)",
			wantArgs: []any{"completed", 10}},
		{name: "maximum order count in the last 30 days",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionOrderCount, WithinDays: new(30), Max: new(0)}),
			wantSQL:  "((SELECT count(*) FROM orders o WHERE " + completed + " AND o.ordered_at BETWEEN ? AND ?) <= ?)",
			wantArgs: []any{"completed", daysAgo(30), asOf, 0}},
		{name: "spend bounds in kuruş, compared as stored",
			segment:  segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionTotalSpent, Min: new(100_000), Max: new(500_000)}),
			wantSQL:  "(" + spend + completed + ") BETWEEN ? AND ?)",
			wantArgs: []any{"completed", 100_000, 500_000}},
		{name: "nested all and any",
			segment: segment(campaign.MatchAll,
				campaign.Condition{Type: campaign.ConditionLanguage, In: []string{"tr"}},
				campaign.Condition{Type: campaign.ConditionGroup, Match: campaign.MatchAny, Conditions: []campaign.Condition{
					{Type: campaign.ConditionCity, In: []string{"İstanbul"}},
					{Type: campaign.ConditionBasket, Negate: true},
				}},
			),
			wantSQL: "(c.preferred_language IN ? AND (" + cities +
				" OR NOT (EXISTS (SELECT 1 FROM basket_items b WHERE b.customer_id = c.id))))",
			wantArgs: []any{[]string{"tr"}, []string{"İstanbul"}}},
		{name: "negated group",
			segment: segment(campaign.MatchAny, campaign.Condition{Type: campaign.ConditionGroup, Match: campaign.MatchAny, Negate: true,
				Conditions: []campaign.Condition{
					{Type: campaign.ConditionCity, In: []string{"İstanbul"}},
					{Type: campaign.ConditionLanguage, In: []string{"en"}},
				}}),
			wantSQL:  "(NOT ((" + cities + " OR c.preferred_language IN ?)))",
			wantArgs: []any{[]string{"İstanbul"}, []string{"en"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := compileSegment(tt.segment, asOf)
			if err != nil {
				t.Fatalf("compileSegment(): %v", err)
			}
			if got.sql != tt.wantSQL {
				t.Errorf("SQL =\n%s\nwant\n%s", got.sql, tt.wantSQL)
			}
			if !reflect.DeepEqual(got.args, tt.wantArgs) {
				t.Errorf("args = %v, want %v", got.args, tt.wantArgs)
			}
			if n := strings.Count(got.sql, "?"); n != len(got.args) {
				t.Errorf("%d placeholders for %d arguments", n, len(got.args))
			}
		})
	}
}

func TestCompileSegmentRejectsUnknownTypes(t *testing.T) {
	_, err := compileSegment(segment(campaign.MatchAll, campaign.Condition{Type: "age", Negate: true}), asOf)
	if err == nil {
		t.Fatal("compileSegment() accepted an unknown condition type")
	}
}

func TestQueryProblems(t *testing.T) {
	valid := segment(campaign.MatchAll)
	tests := []struct {
		name  string
		query Query
		want  []campaign.Problem
	}{
		{name: "valid", query: Query{Segment: valid, Channels: []customer.Channel{customer.ChannelSMS}, AsOf: asOf}},
		{name: "no channels", query: Query{Segment: valid, AsOf: asOf},
			want: []campaign.Problem{{Field: "channels", Code: campaign.ProblemRequired}}},
		{name: "unknown and repeated channels",
			query: Query{Segment: valid, Channels: []customer.Channel{customer.ChannelSMS, "push", customer.ChannelSMS}, AsOf: asOf},
			want: []campaign.Problem{
				{Field: "channels[1]", Code: campaign.ProblemInvalid},
				{Field: "channels[2]", Code: campaign.ProblemInvalid},
			}},
		{name: "no reference time", query: Query{Segment: valid, Channels: []customer.Channel{customer.ChannelEmail}},
			want: []campaign.Problem{{Field: "as_of", Code: campaign.ProblemRequired}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.query.problems(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("problems() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInvalidQueriesFailBeforeTouchingTheDatabase(t *testing.T) {
	svc := NewService(nil) // any database access would panic
	valid := Query{Segment: segment(campaign.MatchAll), Channels: []customer.Channel{customer.ChannelSMS}, AsOf: asOf}
	invalid := Query{Segment: campaign.Segment{Version: 2}, Channels: nil}

	for _, limit := range []int{0, maxBatchSize + 1} {
		if _, err := svc.Recipients(t.Context(), valid, 0, limit); err == nil {
			t.Errorf("Recipients() with limit %d succeeded", limit)
		}
	}
	if _, err := svc.Recipients(t.Context(), valid, -1, 10); err == nil {
		t.Error("Recipients() after -1 succeeded")
	}

	_, err := svc.CountAudience(t.Context(), invalid)
	var problems *campaign.ValidationError
	if !errors.As(err, &problems) {
		t.Fatalf("CountAudience() error = %v, want a *campaign.ValidationError", err)
	}
	want := []campaign.Problem{
		{Field: "segment.version", Code: campaign.ProblemInvalid},
		{Field: "segment.match", Code: campaign.ProblemRequired},
		{Field: "channels", Code: campaign.ProblemRequired},
		{Field: "as_of", Code: campaign.ProblemRequired},
	}
	if !reflect.DeepEqual(problems.Problems, want) {
		t.Errorf("problems = %+v, want %+v", problems.Problems, want)
	}
}
