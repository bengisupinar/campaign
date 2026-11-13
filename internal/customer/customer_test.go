package customer

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// signedUp is when the test customers signed up.
var signedUp = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		change    func(p *Profile)
		createdAt time.Time
		wantErr   bool
	}{
		{name: "valid", createdAt: signedUp},
		{name: "without a phone", change: func(p *Profile) { p.Phone = Phone{} }, createdAt: signedUp},
		{name: "empty first name", change: func(p *Profile) { p.FirstName = "  " }, createdAt: signedUp, wantErr: true},
		{name: "empty last name", change: func(p *Profile) { p.LastName = "" }, createdAt: signedUp, wantErr: true},
		{name: "no email", change: func(p *Profile) { p.Email = Email{} }, createdAt: signedUp, wantErr: true},
		{name: "no city", change: func(p *Profile) { p.CityID = 0 }, createdAt: signedUp, wantErr: true},
		{name: "unsupported language", change: func(p *Profile) { p.Language = "fr" }, createdAt: signedUp, wantErr: true},
		{name: "no signup time", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validProfile(t)
			if tt.change != nil {
				tt.change(&p)
			}
			c, err := New(p, tt.createdAt)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("New() error = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			if got := c.Profile(); got != p {
				t.Errorf("Profile() = %+v, want %+v", got, p)
			}
			if !c.CreatedAt().Equal(tt.createdAt) {
				t.Errorf("CreatedAt() = %v, want %v", c.CreatedAt(), tt.createdAt)
			}
			if c.ID() != 0 {
				t.Errorf("ID() = %d before saving, want 0", c.ID())
			}
			if got := c.Permissions(); len(got) != 0 {
				t.Errorf("a new customer has permissions %v, want none", got)
			}
			for _, ch := range []Channel{ChannelEmail, ChannelSMS} {
				if c.Permitted(ch) {
					t.Errorf("a new customer is permitted on %s, want opt-in first", ch)
				}
			}
		})
	}
}

func TestNewTrimsNames(t *testing.T) {
	p := validProfile(t)
	p.FirstName, p.LastName = "  Ayşe ", "\tYılmaz\n"

	c, err := New(p, signedUp)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if got := c.Profile(); got.FirstName != "Ayşe" || got.LastName != "Yılmaz" {
		t.Errorf("names = %q %q, want trimmed", got.FirstName, got.LastName)
	}
}

func TestGrant(t *testing.T) {
	later := signedUp.Add(24 * time.Hour)
	tests := []struct {
		name    string
		noPhone bool
		channel Channel
		at      time.Time
		wantErr error
	}{
		{name: "email", channel: ChannelEmail, at: later},
		{name: "sms with a phone", channel: ChannelSMS, at: later},
		{name: "at signup", channel: ChannelEmail, at: signedUp},
		{name: "email without a phone", noPhone: true, channel: ChannelEmail, at: later},
		{name: "sms without a phone", noPhone: true, channel: ChannelSMS, at: later, wantErr: ErrUnreachable},
		{name: "unknown channel", channel: "push", at: later, wantErr: ErrInvalid},
		{name: "before signup", channel: ChannelEmail, at: signedUp.Add(-time.Second), wantErr: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCustomer(t, !tt.noPhone)

			err := c.Grant(tt.channel, tt.at)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Grant() error = %v, want %v", err, tt.wantErr)
			}
			if got, want := c.Permitted(tt.channel), tt.wantErr == nil; got != want {
				t.Errorf("Permitted(%s) = %v, want %v", tt.channel, got, want)
			}
			var want []Permission
			if tt.wantErr == nil {
				want = []Permission{{Channel: tt.channel, Granted: true, UpdatedAt: tt.at}}
			}
			if got := c.Permissions(); !slices.Equal(got, want) {
				t.Errorf("Permissions() = %v, want %v", got, want)
			}
		})
	}
}

func TestRevoke(t *testing.T) {
	granted := signedUp.Add(time.Hour)
	revoked := signedUp.Add(48 * time.Hour)
	tests := []struct {
		name       string
		noPhone    bool
		grantFirst bool
		channel    Channel
		at         time.Time
		wantErr    error
	}{
		{name: "after a grant", grantFirst: true, channel: ChannelEmail, at: revoked},
		{name: "without a grant", channel: ChannelSMS, at: revoked},
		{name: "sms without a phone", noPhone: true, channel: ChannelSMS, at: revoked},
		{name: "unknown channel", channel: "push", at: revoked, wantErr: ErrInvalid},
		{name: "before signup", channel: ChannelEmail, at: signedUp.Add(-time.Second), wantErr: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCustomer(t, !tt.noPhone)
			if tt.grantFirst {
				if err := c.Grant(tt.channel, granted); err != nil {
					t.Fatalf("Grant(): %v", err)
				}
			}

			err := c.Revoke(tt.channel, tt.at)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Revoke() error = %v, want %v", err, tt.wantErr)
			}
			if c.Permitted(tt.channel) {
				t.Errorf("Permitted(%s) = true after Revoke", tt.channel)
			}
			if tt.wantErr != nil {
				return
			}
			want := []Permission{{Channel: tt.channel, Granted: false, UpdatedAt: tt.at}}
			if got := c.Permissions(); !slices.Equal(got, want) {
				t.Errorf("Permissions() = %v, want %v", got, want)
			}
		})
	}
}

func TestPermissionsAreOrderedByChannel(t *testing.T) {
	c := newCustomer(t, true)
	at := signedUp.Add(time.Hour)
	if err := c.Grant(ChannelSMS, at); err != nil {
		t.Fatalf("Grant(sms): %v", err)
	}
	if err := c.Revoke(ChannelEmail, at); err != nil {
		t.Fatalf("Revoke(email): %v", err)
	}

	want := []Permission{
		{Channel: ChannelEmail, Granted: false, UpdatedAt: at},
		{Channel: ChannelSMS, Granted: true, UpdatedAt: at},
	}
	if got := c.Permissions(); !slices.Equal(got, want) {
		t.Errorf("Permissions() = %v, want %v", got, want)
	}
}

func validProfile(t *testing.T) Profile {
	t.Helper()
	return Profile{
		FirstName: "Ayşe",
		LastName:  "Yılmaz",
		Email:     mustEmail(t, "ayse@example.com"),
		Phone:     mustPhone(t, "+905321234567"),
		CityID:    1,
		Language:  LanguageTurkish,
	}
}

func newCustomer(t *testing.T, withPhone bool) *Customer {
	t.Helper()
	p := validProfile(t)
	if !withPhone {
		p.Phone = Phone{}
	}
	c, err := New(p, signedUp)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return c
}
