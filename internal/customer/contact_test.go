package customer

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestParseEmail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // empty when the input is invalid
	}{
		{name: "plain address", in: "ayse@example.com", want: "ayse@example.com"},
		{name: "case and spaces normalised", in: "  Jane.Doe@Example.COM ", want: "jane.doe@example.com"},
		{name: "empty", in: ""},
		{name: "no at sign", in: "jane.example.com"},
		{name: "no local part", in: "@example.com"},
		{name: "no domain", in: "jane@"},
		{name: "display name", in: "Jane <jane@example.com>"},
		{name: "two addresses", in: "a@example.com, b@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEmail(tt.in)
			if tt.want == "" {
				assertInvalidWithoutInput(t, err, tt.in)
				return
			}
			if err != nil {
				t.Fatalf("ParseEmail(%q): %v", tt.in, err)
			}
			if got.String() != tt.want {
				t.Errorf("ParseEmail(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParsePhone(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // empty when the input is invalid
	}{
		{name: "E.164", in: "+905321234567", want: "+905321234567"},
		{name: "spaces removed", in: " +90 532 123 45 67 ", want: "+905321234567"},
		{name: "punctuation removed", in: "+49 (30) 1234-5678", want: "+493012345678"},
		{name: "shortest", in: "+1234567", want: "+1234567"},
		{name: "longest", in: "+123456789012345", want: "+123456789012345"},
		{name: "empty", in: ""},
		{name: "national format", in: "05321234567"},
		{name: "country code starting with 0", in: "+05321234567"},
		{name: "too short", in: "+123456"},
		{name: "too long", in: "+1234567890123456"},
		{name: "letters", in: "+90532ABC4567"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePhone(tt.in)
			if tt.want == "" {
				assertInvalidWithoutInput(t, err, tt.in)
				return
			}
			if err != nil {
				t.Fatalf("ParsePhone(%q): %v", tt.in, err)
			}
			if got.String() != tt.want {
				t.Errorf("ParsePhone(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestContactDetailsAreMaskedInLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("contact",
		"email", mustEmail(t, "jane.doe@example.com"),
		"phone", mustPhone(t, "+905321234567"),
		"no_phone", Phone{},
	)

	out := buf.String()
	for _, want := range []string{`"email":"***@example.com"`, `"phone":"+90********67"`, `"no_phone":""`} {
		if !strings.Contains(out, want) {
			t.Errorf("log line %s does not contain %s", out, want)
		}
	}
	for _, secret := range []string{"jane.doe", "5321234567"} {
		if strings.Contains(out, secret) {
			t.Errorf("log line %s leaks %q", out, secret)
		}
	}
}

// assertInvalidWithoutInput checks that err wraps ErrInvalid and doesn't repeat the rejected
// input, which may be personal data.
func assertInvalidWithoutInput(t *testing.T, err error, input string) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	if in := strings.TrimSpace(input); in != "" && strings.Contains(err.Error(), in) {
		t.Errorf("error %q repeats the rejected input", err)
	}
}

func mustEmail(t *testing.T, s string) Email {
	t.Helper()
	e, err := ParseEmail(s)
	if err != nil {
		t.Fatalf("ParseEmail(%q): %v", s, err)
	}
	return e
}

func mustPhone(t *testing.T, s string) Phone {
	t.Helper()
	p, err := ParsePhone(s)
	if err != nil {
		t.Fatalf("ParsePhone(%q): %v", s, err)
	}
	return p
}
