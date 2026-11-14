package campaign

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bengisupinar/campaign/internal/customer"
)

// SegmentVersion is the version of the segment DSL this package accepts and writes.
const SegmentVersion = 1

// Limits that keep a segment, which may come from an AI model, cheap to validate and compile.
const (
	maxSegmentDepth      = 3    // the root's conditions are at depth 1
	maxSegmentConditions = 20   // in the whole tree, groups included
	maxListValues        = 50   // per "in" or "categories" list
	maxValueLength       = 100  // per city or category name
	maxWindowDays        = 3650 // ten years
)

// Segment selects the customers a campaign targets: a tree of conditions over customer and
// commerce data, in a JSON DSL. The segmentation engine compiles each condition into one
// parameterised SQL fragment, so values are never interpolated. Consent is never a condition:
// a campaign always requires permission for each channel it uses.
type Segment struct {
	Version    int         `json:"version"`
	Match      Match       `json:"match"`
	Conditions []Condition `json:"conditions"` // none: every customer
}

// Match says whether all of a group's conditions must hold, or any one of them.
type Match string

// Matches.
const (
	MatchAll Match = "all"
	MatchAny Match = "any"
)

// ConditionType names a kind of condition; each takes its own parameters.
type ConditionType string

// Condition types, with their parameters; "?" marks an optional one.
const (
	ConditionCountry    ConditionType = "country"     // in: ISO 3166-1 alpha-2 codes
	ConditionCity       ConditionType = "city"        // in: city names
	ConditionLanguage   ConditionType = "language"    // in: tr, de, en
	ConditionSignedUp   ConditionType = "signed_up"   // within_days
	ConditionBasket     ConditionType = "basket"      // within_days?, categories?
	ConditionPurchased  ConditionType = "purchased"   // within_days?, categories? (completed orders)
	ConditionOrderCount ConditionType = "order_count" // within_days?, min?, max? (completed orders)
	ConditionTotalSpent ConditionType = "total_spent" // within_days?, min?, max? in kuruş (completed orders)
	ConditionGroup      ConditionType = "group"       // match, conditions
)

// Condition is one test of a customer. Which parameters it takes depends on its type, and any
// other parameter makes it invalid. Negate inverts it: "purchased" within 30 days, negated,
// matches customers without a completed order in the last 30 days.
type Condition struct {
	Type       ConditionType `json:"type"`
	Negate     bool          `json:"negate,omitempty"`
	In         []string      `json:"in,omitempty"`
	WithinDays *int          `json:"within_days,omitempty"`
	Categories []string      `json:"categories,omitempty"`
	Min        *int          `json:"min,omitempty"` // orders for order_count; kuruş for total_spent (100 = 1 TRY)
	Max        *int          `json:"max,omitempty"`
	Match      Match         `json:"match,omitempty"`
	Conditions []Condition   `json:"conditions,omitempty"`
}

// Reference is a name a segment uses that must exist in the reference data, and the field it
// appears in.
type Reference struct {
	Kind  ReferenceKind
	Name  string
	Field string // e.g. "segment.conditions[0].in[1]"
}

// ReferenceKind is the reference data a name must exist in.
type ReferenceKind string

// Reference kinds.
const (
	ReferenceCountry  ReferenceKind = "country"  // a country condition's codes
	ReferenceCity     ReferenceKind = "city"     // a city condition's names, in any country
	ReferenceCategory ReferenceKind = "category" // a basket or purchased condition's categories
)

// Problems lists what is wrong with the segment's structure: version, match, condition types,
// parameters and limits, with field paths starting with "segment". Whether the names it uses
// exist is for the segmentation engine to check (see References).
func (s Segment) Problems() []Problem {
	var ps problems
	s.validate(&ps)
	return ps
}

// References lists the country codes, city names and category names the segment uses, in the
// order they appear.
func (s Segment) References() []Reference {
	return appendReferences(nil, "segment.conditions", s.Conditions)
}

func appendReferences(refs []Reference, path string, conditions []Condition) []Reference {
	for i, c := range conditions {
		at := fmt.Sprintf("%s[%d]", path, i)
		var kind ReferenceKind
		var param string
		var names []string
		switch c.Type {
		case ConditionCountry:
			kind, param, names = ReferenceCountry, "in", c.In
		case ConditionCity:
			kind, param, names = ReferenceCity, "in", c.In
		case ConditionBasket, ConditionPurchased:
			kind, param, names = ReferenceCategory, "categories", c.Categories
		case ConditionGroup:
			refs = appendReferences(refs, at+".conditions", c.Conditions)
		}
		for j, name := range names {
			refs = append(refs, Reference{Kind: kind, Name: name, Field: fmt.Sprintf("%s.%s[%d]", at, param, j)})
		}
	}
	return refs
}

func (s Segment) validate(ps *problems) {
	if s.Version == 0 && s.Match == "" && s.Conditions == nil {
		ps.add("segment", ProblemRequired)
		return
	}
	if s.Version != SegmentVersion {
		ps.add("segment.version", ProblemInvalid)
	}
	v := segmentValidator{ps: ps}
	v.match("segment.match", s.Match)
	v.conditions("segment.conditions", s.Conditions, 1)
	if v.count > maxSegmentConditions {
		ps.add("segment.conditions", ProblemTooMany)
	}
}

// segmentValidator walks a segment's conditions, recording their problems and counting them.
type segmentValidator struct {
	ps    *problems
	count int
}

func (v *segmentValidator) conditions(path string, cs []Condition, depth int) {
	for i, c := range cs {
		v.count++
		v.condition(fmt.Sprintf("%s[%d]", path, i), c, depth)
	}
}

// condition checks the parameters of c's type, which each case lists in takes; any other
// parameter is not allowed. Type and negate are always allowed.
func (v *segmentValidator) condition(path string, c Condition, depth int) {
	var takes []string
	switch c.Type {
	case ConditionCountry:
		takes = []string{"in"}
		v.list(path+".in", c.In, true, validCountryCode)
	case ConditionCity:
		takes = []string{"in"}
		v.list(path+".in", c.In, true, validName)
	case ConditionLanguage:
		takes = []string{"in"}
		v.list(path+".in", c.In, true, func(s string) bool { return customer.Language(s).Valid() })
	case ConditionSignedUp:
		takes = []string{"within_days"}
		v.window(path, c.WithinDays, true)
	case ConditionBasket, ConditionPurchased:
		takes = []string{"within_days", "categories"}
		v.window(path, c.WithinDays, false)
		v.list(path+".categories", c.Categories, false, validName)
	case ConditionOrderCount, ConditionTotalSpent:
		takes = []string{"within_days", "min", "max"}
		v.window(path, c.WithinDays, false)
		v.bounds(path, c.Min, c.Max)
	case ConditionGroup:
		takes = []string{"match", "conditions"}
		v.group(path, c, depth)
	case "":
		v.ps.add(path+".type", ProblemRequired)
		return
	default:
		v.ps.addDetail(path+".type", ProblemInvalid, string(c.Type))
		return
	}
	for _, name := range c.paramsSet() {
		if !slices.Contains(takes, name) {
			v.ps.add(path+"."+name, ProblemNotAllowed)
		}
	}
}

// list checks a list parameter: present when required, with 1 to 50 values, each valid. A list
// that is too long still has its values checked, so every problem is reported.
func (v *segmentValidator) list(path string, values []string, required bool, valid func(string) bool) {
	switch {
	case values == nil:
		if required {
			v.ps.add(path, ProblemRequired)
		}
		return
	case len(values) == 0:
		v.ps.add(path, ProblemRequired)
		return
	case len(values) > maxListValues:
		v.ps.add(path, ProblemTooMany)
	}
	for i, value := range values {
		if !valid(value) {
			v.ps.add(fmt.Sprintf("%s[%d]", path, i), ProblemInvalid)
		}
	}
}

// window checks a "within_days" parameter: present when required, between 1 and 3650.
func (v *segmentValidator) window(path string, days *int, required bool) {
	switch {
	case days == nil:
		if required {
			v.ps.add(path+".within_days", ProblemRequired)
		}
	case *days < 1 || *days > maxWindowDays:
		v.ps.add(path+".within_days", ProblemInvalid)
	}
}

// bounds checks a "min"/"max" range: at least one end, neither negative, and min not above
// max. It reports every problem; the order of the ends is only checked when both are valid on
// their own.
func (v *segmentValidator) bounds(path string, lo, hi *int) {
	if lo == nil && hi == nil {
		v.ps.addDetail(path, ProblemRequired, "min or max")
		return
	}
	loValid, hiValid := lo == nil || *lo >= 0, hi == nil || *hi >= 0
	if !loValid {
		v.ps.add(path+".min", ProblemInvalid)
	}
	if !hiValid {
		v.ps.add(path+".max", ProblemInvalid)
	}
	if lo != nil && hi != nil && loValid && hiValid && *lo > *hi {
		v.ps.add(path+".max", ProblemInvalid)
	}
}

func (v *segmentValidator) group(path string, c Condition, depth int) {
	if depth >= maxSegmentDepth {
		v.ps.add(path, ProblemTooDeep) // its conditions would be deeper than allowed
		return
	}
	v.match(path+".match", c.Match)
	if len(c.Conditions) == 0 {
		v.ps.add(path+".conditions", ProblemRequired)
		return
	}
	v.conditions(path+".conditions", c.Conditions, depth+1)
}

func (v *segmentValidator) match(path string, m Match) {
	switch m {
	case MatchAll, MatchAny:
	case "":
		v.ps.add(path, ProblemRequired)
	default:
		v.ps.addDetail(path, ProblemInvalid, string(m))
	}
}

func validCountryCode(s string) bool {
	return len(s) == 2 && 'A' <= s[0] && s[0] <= 'Z' && 'A' <= s[1] && s[1] <= 'Z'
}

// validName accepts a city or category name: not empty or whitespace only, no surrounding
// whitespace, at most 100 characters. Whether it exists is checked by the segmentation engine.
func validName(s string) bool {
	trimmed := strings.TrimSpace(s)
	return trimmed != "" && trimmed == s && utf8.RuneCountInString(s) <= maxValueLength
}

// paramsSet lists the parameters c sets, by their JSON names.
func (c Condition) paramsSet() []string {
	var set []string
	for _, p := range []struct {
		name string
		set  bool
	}{
		{"in", c.In != nil},
		{"within_days", c.WithinDays != nil},
		{"categories", c.Categories != nil},
		{"min", c.Min != nil},
		{"max", c.Max != nil},
		{"match", c.Match != ""},
		{"conditions", c.Conditions != nil},
	} {
		if p.set {
			set = append(set, p.name)
		}
	}
	return set
}

// clone copies the segment deeply, so no caller shares its slices or pointers. A segment
// without conditions gets an empty list, which encodes as [] rather than null.
func (s Segment) clone() Segment {
	s.Conditions = cloneConditions(s.Conditions)
	if s.Conditions == nil {
		s.Conditions = []Condition{}
	}
	return s
}

func cloneConditions(cs []Condition) []Condition {
	if cs == nil {
		return nil
	}
	out := make([]Condition, len(cs))
	for i, c := range cs {
		c.In, c.Categories = slices.Clone(c.In), slices.Clone(c.Categories)
		c.WithinDays, c.Min, c.Max = clonePtr(c.WithinDays), clonePtr(c.Min), clonePtr(c.Max)
		c.Conditions = cloneConditions(c.Conditions)
		out[i] = c
	}
	return out
}
