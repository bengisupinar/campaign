package campaign

import (
	"strings"
	"unicode/utf8"

	"github.com/bengisupinar/campaign/internal/customer"
)

const (
	// maxSMSLength is the length of one GSM-7 text message. Turkish letters such as ş and ğ
	// switch a message to UCS-2, where one message holds only 70 characters (DECISIONS.md).
	maxSMSLength          = 160
	maxEmailSubjectLength = 80
)

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
		checkText(ps, "content.sms.body", c.SMS.Body, maxSMSLength)
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
		switch name := strings.TrimSpace(rest[open+2 : open+2+end]); name {
		case "":
			malformed = true
		case "first_name", "last_name", "city":
		default:
			ps.addDetail(field, ProblemUnknownPlaceholder, name)
		}
		rest = rest[open+2+end+2:]
	}
	if malformed {
		ps.add(field, ProblemMalformedPlaceholder)
	}
}
