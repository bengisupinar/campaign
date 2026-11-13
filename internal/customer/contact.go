package customer

import (
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"unicode"
)

// Email is a customer's email address, trimmed and lowercased. Create one with ParseEmail;
// the zero value means no address.
type Email struct{ address string }

// ParseEmail validates s as a single email address and returns it trimmed and lowercased.
// The error never contains s.
func ParseEmail(s string) (Email, error) {
	address := strings.ToLower(strings.TrimSpace(s))
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Name != "" || parsed.Address != address {
		return Email{}, fmt.Errorf("%w email address", ErrInvalid)
	}
	return Email{address: address}, nil
}

// String returns the address.
func (e Email) String() string { return e.address }

// IsZero reports whether e holds no address.
func (e Email) IsZero() bool { return e.address == "" }

// LogValue implements slog.LogValuer. Only the domain is logged, e.g. "***@example.com", so
// addresses never reach the logs.
func (e Email) LogValue() slog.Value {
	_, domain, _ := strings.Cut(e.address, "@")
	return slog.StringValue("***@" + domain)
}

// Phone is a phone number in E.164 format, e.g. +905321234567, as SMS providers expect.
// Create one with ParsePhone; the zero value means no phone number.
type Phone struct{ number string }

// ParsePhone validates s as an E.164 number after removing spaces, hyphens, dots and
// parentheses, so "+90 (532) 123-45-67" is accepted. The error never contains s.
func ParsePhone(s string) (Phone, error) {
	number := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("-.()", r) {
			return -1
		}
		return r
	}, s)
	if !isE164(number) {
		return Phone{}, fmt.Errorf("%w phone number: want E.164, a + and 7 to 15 digits", ErrInvalid)
	}
	return Phone{number: number}, nil
}

// isE164 reports whether s is a "+" followed by 7 to 15 digits, the first of which is not 0.
// The customers table's CHECK constraint applies the same rule.
func isE164(s string) bool {
	if len(s) < 8 || len(s) > 16 || s[0] != '+' || s[1] == '0' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String returns the number in E.164 format.
func (p Phone) String() string { return p.number }

// IsZero reports whether p holds no number.
func (p Phone) IsZero() bool { return p.number == "" }

// LogValue implements slog.LogValuer. Only the first three and last two characters are
// logged, e.g. "+90********67", so numbers never reach the logs.
func (p Phone) LogValue() slog.Value {
	if p.IsZero() {
		return slog.StringValue("")
	}
	n := p.number
	return slog.StringValue(n[:3] + strings.Repeat("*", len(n)-5) + n[len(n)-2:])
}
