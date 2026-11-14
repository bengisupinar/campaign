package campaign

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func TestSegmentValidation(t *testing.T) {
	city := Condition{Type: ConditionCity, In: []string{"İstanbul"}}
	segment := func(cs ...Condition) Segment {
		return Segment{Version: SegmentVersion, Match: MatchAll, Conditions: cs}
	}
	group := func(cs ...Condition) Condition {
		return Condition{Type: ConditionGroup, Match: MatchAny, Conditions: cs}
	}
	repeat := func(c Condition, n int) []Condition {
		cs := make([]Condition, n)
		for i := range cs {
			cs[i] = c
		}
		return cs
	}
	cities := make([]string, 51)
	for i := range cities {
		cities[i] = fmt.Sprintf("City %d", i)
	}

	tests := []struct {
		name    string
		segment Segment
		want    []Problem // nil when the segment is valid
	}{
		{name: "every customer", segment: segment()},
		{name: "every condition type", segment: segment(
			Condition{Type: ConditionCountry, In: []string{"TR", "DE"}},
			Condition{Type: ConditionCity, In: []string{"İstanbul", "Frankfurt am Main"}},
			Condition{Type: ConditionLanguage, In: []string{"tr", "en"}},
			Condition{Type: ConditionSignedUp, WithinDays: new(30), Negate: true},
			Condition{Type: ConditionBasket},
			Condition{Type: ConditionBasket, WithinDays: new(7), Categories: []string{"Electronics"}},
			Condition{Type: ConditionPurchased, Negate: true},
			Condition{Type: ConditionOrderCount, WithinDays: new(365), Min: new(10)},
			Condition{Type: ConditionOrderCount, Max: new(0)},
			Condition{Type: ConditionTotalSpent, WithinDays: new(90), Min: new(100_000), Max: new(500_000)},
		)},
		{name: "groups three deep", segment: segment(group(city, group(city)))},
		{name: "missing", segment: Segment{},
			want: []Problem{{Field: "segment", Code: ProblemRequired}}},
		{name: "another version", segment: Segment{Version: 2, Match: MatchAll, Conditions: []Condition{}},
			want: []Problem{{Field: "segment.version", Code: ProblemInvalid}}},
		{name: "no match", segment: Segment{Version: SegmentVersion, Conditions: []Condition{}},
			want: []Problem{{Field: "segment.match", Code: ProblemRequired}}},
		{name: "unknown match", segment: Segment{Version: SegmentVersion, Match: "some", Conditions: []Condition{}},
			want: []Problem{{Field: "segment.match", Code: ProblemInvalid, Detail: "some"}}},
		{name: "no type", segment: segment(Condition{}),
			want: []Problem{{Field: "segment.conditions[0].type", Code: ProblemRequired}}},
		{name: "unknown type", segment: segment(Condition{Type: "age"}),
			want: []Problem{{Field: "segment.conditions[0].type", Code: ProblemInvalid, Detail: "age"}}},
		{name: "country without codes", segment: segment(Condition{Type: ConditionCountry}),
			want: []Problem{{Field: "segment.conditions[0].in", Code: ProblemRequired}}},
		{name: "empty list", segment: segment(Condition{Type: ConditionCountry, In: []string{}}),
			want: []Problem{{Field: "segment.conditions[0].in", Code: ProblemRequired}}},
		{name: "bad country codes", segment: segment(Condition{Type: ConditionCountry, In: []string{"tr", "TUR", "DE"}}),
			want: []Problem{
				{Field: "segment.conditions[0].in[0]", Code: ProblemInvalid},
				{Field: "segment.conditions[0].in[1]", Code: ProblemInvalid},
			}},
		{name: "unsupported language", segment: segment(Condition{Type: ConditionLanguage, In: []string{"fr"}}),
			want: []Problem{{Field: "segment.conditions[0].in[0]", Code: ProblemInvalid}}},
		{name: "blank and padded city names", segment: segment(Condition{Type: ConditionCity, In: []string{"", " İstanbul"}}),
			want: []Problem{
				{Field: "segment.conditions[0].in[0]", Code: ProblemInvalid},
				{Field: "segment.conditions[0].in[1]", Code: ProblemInvalid},
			}},
		{name: "51 values", segment: segment(Condition{Type: ConditionCity, In: cities}),
			want: []Problem{{Field: "segment.conditions[0].in", Code: ProblemTooMany}}},
		{name: "51 values, one of them blank", segment: segment(Condition{Type: ConditionCity, In: append(slices.Clone(cities[:50]), " ")}),
			want: []Problem{
				{Field: "segment.conditions[0].in", Code: ProblemTooMany},
				{Field: "segment.conditions[0].in[50]", Code: ProblemInvalid},
			}},
		{name: "whitespace-only names", segment: segment(
			Condition{Type: ConditionCity, In: []string{"   ", "\t", " "}},
			Condition{Type: ConditionBasket, Categories: []string{" "}},
		), want: []Problem{
			{Field: "segment.conditions[0].in[0]", Code: ProblemInvalid},
			{Field: "segment.conditions[0].in[1]", Code: ProblemInvalid},
			{Field: "segment.conditions[0].in[2]", Code: ProblemInvalid},
			{Field: "segment.conditions[1].categories[0]", Code: ProblemInvalid},
		}},
		{name: "signed_up without a window", segment: segment(Condition{Type: ConditionSignedUp}),
			want: []Problem{{Field: "segment.conditions[0].within_days", Code: ProblemRequired}}},
		{name: "windows out of range", segment: segment(
			Condition{Type: ConditionSignedUp, WithinDays: new(0)},
			Condition{Type: ConditionBasket, WithinDays: new(3651)},
		), want: []Problem{
			{Field: "segment.conditions[0].within_days", Code: ProblemInvalid},
			{Field: "segment.conditions[1].within_days", Code: ProblemInvalid},
		}},
		{name: "empty categories", segment: segment(Condition{Type: ConditionBasket, Categories: []string{}}),
			want: []Problem{{Field: "segment.conditions[0].categories", Code: ProblemRequired}}},
		{name: "order_count without bounds", segment: segment(Condition{Type: ConditionOrderCount}),
			want: []Problem{{Field: "segment.conditions[0]", Code: ProblemRequired, Detail: "min or max"}}},
		{name: "negative minimum", segment: segment(Condition{Type: ConditionOrderCount, Min: new(-1)}),
			want: []Problem{{Field: "segment.conditions[0].min", Code: ProblemInvalid}}},
		{name: "minimum above maximum", segment: segment(Condition{Type: ConditionOrderCount, Min: new(5), Max: new(2)}),
			want: []Problem{{Field: "segment.conditions[0].max", Code: ProblemInvalid}}},
		{name: "both bounds negative", segment: segment(Condition{Type: ConditionOrderCount, Min: new(-1), Max: new(-2)}),
			want: []Problem{
				{Field: "segment.conditions[0].min", Code: ProblemInvalid},
				{Field: "segment.conditions[0].max", Code: ProblemInvalid},
			}},
		{name: "negative minimum under a valid maximum", segment: segment(Condition{Type: ConditionTotalSpent, Min: new(-5), Max: new(10)}),
			want: []Problem{{Field: "segment.conditions[0].min", Code: ProblemInvalid}}},
		{name: "valid minimum above a negative maximum", segment: segment(Condition{Type: ConditionOrderCount, Min: new(3), Max: new(-1)}),
			want: []Problem{{Field: "segment.conditions[0].max", Code: ProblemInvalid}}},
		{name: "total_spent without bounds", segment: segment(Condition{Type: ConditionTotalSpent, WithinDays: new(30)}),
			want: []Problem{{Field: "segment.conditions[0]", Code: ProblemRequired, Detail: "min or max"}}},
		{name: "parameters the type doesn't take", segment: segment(
			Condition{Type: ConditionBasket, In: []string{"İstanbul"}},
			Condition{Type: ConditionCity, In: []string{"İstanbul"}, WithinDays: new(3)},
		), want: []Problem{
			{Field: "segment.conditions[0].in", Code: ProblemNotAllowed},
			{Field: "segment.conditions[1].within_days", Code: ProblemNotAllowed},
		}},
		{name: "empty group", segment: segment(Condition{Type: ConditionGroup, Match: MatchAll}),
			want: []Problem{{Field: "segment.conditions[0].conditions", Code: ProblemRequired}}},
		{name: "group without match", segment: segment(Condition{Type: ConditionGroup, Conditions: []Condition{city}}),
			want: []Problem{{Field: "segment.conditions[0].match", Code: ProblemRequired}}},
		{name: "groups four deep", segment: segment(group(group(group(city)))),
			want: []Problem{{Field: "segment.conditions[0].conditions[0].conditions[0]", Code: ProblemTooDeep}}},
		{name: "21 conditions", segment: segment(repeat(city, 21)...),
			want: []Problem{{Field: "segment.conditions", Code: ProblemTooMany}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDraft()
			d.Segment = tt.segment
			if got := problemsOf(t, d); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("problems = %+v, want %+v", got, tt.want)
			}
			// The segmentation engine asks the segment directly and must get the same answer.
			if got := tt.segment.Problems(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Problems() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSegmentReferences(t *testing.T) {
	s := Segment{Version: SegmentVersion, Match: MatchAll, Conditions: []Condition{
		{Type: ConditionCountry, In: []string{"TR", "DE"}},
		{Type: ConditionLanguage, In: []string{"tr"}},
		{Type: ConditionGroup, Match: MatchAny, Conditions: []Condition{
			{Type: ConditionCity, In: []string{"İstanbul"}},
			{Type: ConditionBasket, WithinDays: new(30)},
			{Type: ConditionPurchased, Categories: []string{"Books", "Electronics"}, Negate: true},
		}},
	}}
	want := []Reference{
		{Kind: ReferenceCountry, Name: "TR", Field: "segment.conditions[0].in[0]"},
		{Kind: ReferenceCountry, Name: "DE", Field: "segment.conditions[0].in[1]"},
		{Kind: ReferenceCity, Name: "İstanbul", Field: "segment.conditions[2].conditions[0].in[0]"},
		{Kind: ReferenceCategory, Name: "Books", Field: "segment.conditions[2].conditions[2].categories[0]"},
		{Kind: ReferenceCategory, Name: "Electronics", Field: "segment.conditions[2].conditions[2].categories[1]"},
	}
	if got := s.References(); !reflect.DeepEqual(got, want) {
		t.Errorf("References() = %+v, want %+v", got, want)
	}
	if got := (Segment{Version: SegmentVersion, Match: MatchAll}).References(); got != nil {
		t.Errorf("References() of an empty segment = %+v, want none", got)
	}
}
