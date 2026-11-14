package campaign

import (
	"strings"
	"unicode/utf8"

	"github.com/bengisupinar/campaign/internal/customer"
)

// MaxSMSLength is the length of one GSM-7 text message: the limit of an SMS template as
// written, and of each message once delivery fills in its placeholders. Turkish letters such
// as ş and ğ switch a message to UCS-2, where one message holds only 70 characters
// (DECISIONS.md).
const MaxSMSLength = 160

const maxEmailSubjectLength = 80

// Content is a campaign's message for each channel it uses. A nil block means the channel
// isn't used, so a campaign can never target a channel it has no message for.
type Content struct {
	SMS   *SMS   `json:"sms,omitempty"`
	Email *Email `json:"email,omitempty"`
}

// SMS is a text message. Its body may contain placeholders.
type SMS struct {
	Body string `json:"body"`
}

// Email is an email message. Its subject and body may contain placeholders.
type Email struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Channels returns the channels the content is for: email before SMS.
func (c Content) Channels() []customer.Channel {
	channels := []customer.Channel{}
	if c.Email != nil {
		channels = append(channels, customer.ChannelEmail)
	}
	if c.SMS != nil {
		channels = append(channels, customer.ChannelSMS)
	}
	return channels
}

func (c Content) validate(ps *problems) {
	if c.SMS == nil && c.Email == nil {
		ps.add("content", ProblemRequired)
		return
	}
	if c.SMS != nil {
		checkText(ps, "content.sms.body", c.SMS.Body, MaxSMSLength)
	}
	if c.Email != nil {
		checkText(ps, "content.email.subject", c.Email.Subject, maxEmailSubjectLength)
		checkText(ps, "content.email.body", c.Email.Body, 0)
	}
}

func (c Content) clone() Content {
	c.SMS, c.Email = clonePtr(c.SMS), clonePtr(c.Email)
	return c
}

// Values are what one recipient's message fills its placeholders with.
type Values struct {
	FirstName string
	LastName  string
	City      string
}

// value returns what a placeholder name stands for, and whether it is a placeholder at all.
// It is the only list of placeholders: validation and Fill both read it.
func (v Values) value(name string) (string, bool) {
	switch name {
	case "first_name":
		return v.FirstName, true
	case "last_name":
		return v.LastName, true
	case "city":
		return v.City, true
	default:
		return "", false
	}
}

func isPlaceholder(name string) bool {
	_, ok := Values{}.value(name)
	return ok
}

// Fill replaces each placeholder in text, which must be valid content, with v's value.
func Fill(text string, v Values) string {
	var b strings.Builder
	for {
		open := strings.Index(text, "{{")
		if open < 0 {
			break
		}
		end := strings.Index(text[open+2:], "}}")
		if end < 0 {
			break
		}
		b.WriteString(text[:open])
		if value, ok := v.value(strings.TrimSpace(text[open+2 : open+2+end])); ok {
			b.WriteString(value)
		} else {
			b.WriteString(text[open : open+2+end+2]) // not a placeholder: kept as written
		}
		text = text[open+2+end+2:]
	}
	b.WriteString(text)
	return b.String()
}

// checkText requires text, applies a length limit when there is one, and checks the
// placeholders. Length counts the characters of the text as written, so a placeholder counts
// as its own text: the rendered length depends on the customer, and delivery checks it.
func checkText(ps *problems, field, text string, limit int) {
	if strings.TrimSpace(text) == "" {
		ps.add(field, ProblemRequired)
		return
	}
	if limit > 0 && utf8.RuneCountInString(text) > limit {
		ps.add(field, ProblemTooLong)
	}
	checkPlaceholders(ps, field, text)
}

// checkPlaceholders reports each {{placeholder}} in text that isn't a customer field delivery
// can fill in, and, once, braces that don't form a placeholder: "{{" without "}}", "}}" on its
// own, or "{{}}". It scans the whole text, so every unknown placeholder is reported.
func checkPlaceholders(ps *problems, field, text string) {
	malformed := false
	for rest := text; ; {
		open, closing := strings.Index(rest, "{{"), strings.Index(rest, "}}")
		if open < 0 && closing < 0 {
			break
		}
		if open < 0 || (closing >= 0 && closing < open) { // "}}" on its own: skip past it
			malformed = true
			rest = rest[closing+2:]
			continue
		}
		end := strings.Index(rest[open+2:], "}}")
		if end < 0 { // nothing closes this "{{"
			malformed = true
			break
		}
		switch name := strings.TrimSpace(rest[open+2 : open+2+end]); {
		case name == "":
			malformed = true
		case !isPlaceholder(name):
			ps.addDetail(field, ProblemUnknownPlaceholder, name)
		}
		rest = rest[open+2+end+2:]
	}
	if malformed {
		ps.add(field, ProblemMalformedPlaceholder)
	}
}
