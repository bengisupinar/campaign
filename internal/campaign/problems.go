package campaign

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Problem codes: stable, machine-readable reasons a draft is invalid. A form shows them next
// to their field, and the AI phase will send them back to the model to repair its proposal.
const (
	ProblemRequired             = "required"
	ProblemTooLong              = "too_long"
	ProblemTooMany              = "too_many"
	ProblemTooDeep              = "too_deep"
	ProblemInvalid              = "invalid"
	ProblemNotAllowed           = "not_allowed" // a parameter the condition type doesn't take
	ProblemUnknown              = "unknown"     // a country, city or category that doesn't exist
	ProblemUnknownPlaceholder   = "unknown_placeholder"
	ProblemMalformedPlaceholder = "malformed_placeholder"
)

// maxDetailLength bounds a problem's detail, which echoes input that may be long.
const maxDetailLength = 40

// Problem is one reason a draft is invalid.
type Problem struct {
	Field  string `json:"field"`            // a JSON path, e.g. "content.sms.body" or "segment.conditions[2].in[0]"
	Code   string `json:"problem"`          // one of the Problem constants
	Detail string `json:"detail,omitempty"` // e.g. the unknown placeholder or condition type
}

// ValidationError lists every problem with a draft, in the order of its fields.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString("invalid campaign:")
	for i, p := range e.Problems {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, " %s %s", p.Field, p.Code)
	}
	return b.String()
}

// problems collects a draft's problems while it is validated.
type problems []Problem

func (ps *problems) add(field, code string) {
	*ps = append(*ps, Problem{Field: field, Code: code})
}

func (ps *problems) addDetail(field, code, detail string) {
	if utf8.RuneCountInString(detail) > maxDetailLength {
		detail = string([]rune(detail)[:maxDetailLength]) + "…"
	}
	*ps = append(*ps, Problem{Field: field, Code: code, Detail: detail})
}

// err returns a *ValidationError with the problems, or nil when there are none.
func (ps problems) err() error {
	if len(ps) == 0 {
		return nil
	}
	return &ValidationError{Problems: ps}
}
