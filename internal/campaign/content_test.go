package campaign

import (
	"reflect"
	"strings"
	"testing"
)

func TestFill(t *testing.T) {
	v := Values{FirstName: "Ayşe", LastName: "Yılmaz", City: "İstanbul"}
	tests := []struct{ text, want string }{
		{"Merhaba {{first_name}}, sepetin seni bekliyor!", "Merhaba Ayşe, sepetin seni bekliyor!"},
		{"{{ first_name }} {{last_name}} from {{city}}", "Ayşe Yılmaz from İstanbul"},
		{"No placeholders", "No placeholders"},
		{"{{city}}{{city}}", "İstanbulİstanbul"},
	}
	for _, tt := range tests {
		if got := Fill(tt.text, v); got != tt.want {
			t.Errorf("Fill(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestDraftValidation(t *testing.T) {
	sms := func(body string) Content { return Content{SMS: &SMS{Body: body}} }
	email := func(subject, body string) Content { return Content{Email: &Email{Subject: subject, Body: body}} }

	tests := []struct {
		name   string
		change func(*Draft)
		want   []Problem // nil when the draft is valid
	}{
		{name: "SMS only"},
		{name: "email only", change: func(d *Draft) { d.Content = email("Sepetin seni bekliyor", "Merhaba {{first_name}}") }},
		{name: "both channels", change: func(d *Draft) { d.Content.Email = &Email{Subject: "Hi", Body: "Hello"} }},
		{name: "blank name", change: func(d *Draft) { d.Name = " \t" },
			want: []Problem{{Field: "name", Code: ProblemRequired}}},
		{name: "name of 100 characters", change: func(d *Draft) { d.Name = strings.Repeat("ş", 100) }},
		{name: "name of 101 characters", change: func(d *Draft) { d.Name = strings.Repeat("ş", 101) },
			want: []Problem{{Field: "name", Code: ProblemTooLong}}},
		{name: "no channel", change: func(d *Draft) { d.Content = Content{} },
			want: []Problem{{Field: "content", Code: ProblemRequired}}},
		{name: "blank SMS", change: func(d *Draft) { d.Content = sms(" ") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemRequired}}},
		{name: "SMS of 160 Turkish characters", change: func(d *Draft) { d.Content = sms(strings.Repeat("ğ", 160)) }},
		{name: "SMS of 161 characters", change: func(d *Draft) { d.Content = sms(strings.Repeat("ğ", 161)) },
			want: []Problem{{Field: "content.sms.body", Code: ProblemTooLong}}},
		{name: "subject of 80 characters", change: func(d *Draft) { d.Content = email(strings.Repeat("ı", 80), "Hello") }},
		{name: "subject of 81 characters", change: func(d *Draft) { d.Content = email(strings.Repeat("ı", 81), "Hello") },
			want: []Problem{{Field: "content.email.subject", Code: ProblemTooLong}}},
		{name: "blank email body", change: func(d *Draft) { d.Content = email("Hi", "") },
			want: []Problem{{Field: "content.email.body", Code: ProblemRequired}}},
		{name: "every placeholder, with spaces", change: func(d *Draft) { d.Content = sms("{{first_name}} {{ last_name }} from {{city}}") }},
		{name: "unknown placeholder", change: func(d *Draft) { d.Content = sms("Use {{coupon}} today") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemUnknownPlaceholder, Detail: "coupon"}}},
		{name: "placeholder in the wrong case", change: func(d *Draft) { d.Content = sms("Hi {{First_Name}}") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemUnknownPlaceholder, Detail: "First_Name"}}},
		{name: "long unknown placeholder", change: func(d *Draft) { d.Content = sms("{{" + strings.Repeat("x", 50) + "}}") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemUnknownPlaceholder, Detail: strings.Repeat("x", 40) + "…"}}},
		{name: "unclosed placeholder", change: func(d *Draft) { d.Content = sms("Hi {{first_name") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemMalformedPlaceholder}}},
		{name: "closing braces on their own", change: func(d *Draft) { d.Content = sms("Hi first_name}}") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemMalformedPlaceholder}}},
		{name: "empty placeholder", change: func(d *Draft) { d.Content = sms("Hi {{ }}") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemMalformedPlaceholder}}},
		{name: "closing braces before an unknown placeholder", change: func(d *Draft) { d.Content = sms("Hi }} and {{coupon}}") },
			want: []Problem{
				{Field: "content.sms.body", Code: ProblemUnknownPlaceholder, Detail: "coupon"},
				{Field: "content.sms.body", Code: ProblemMalformedPlaceholder},
			}},
		{name: "every unknown placeholder around an empty one", change: func(d *Draft) { d.Content = sms("{{coupon}} {{ }} {{discount}} {{first_name}}") },
			want: []Problem{
				{Field: "content.sms.body", Code: ProblemUnknownPlaceholder, Detail: "coupon"},
				{Field: "content.sms.body", Code: ProblemUnknownPlaceholder, Detail: "discount"},
				{Field: "content.sms.body", Code: ProblemMalformedPlaceholder},
			}},
		{name: "malformed braces reported once", change: func(d *Draft) { d.Content = sms("}} x }} {{ }} {{first_name") },
			want: []Problem{{Field: "content.sms.body", Code: ProblemMalformedPlaceholder}}},
		{name: "placeholders in an email", change: func(d *Draft) { d.Content = email("{{coupon}} inside", "Hi {{first_name}") },
			want: []Problem{
				{Field: "content.email.subject", Code: ProblemUnknownPlaceholder, Detail: "coupon"},
				{Field: "content.email.body", Code: ProblemMalformedPlaceholder},
			}},
		{name: "every problem at once", change: func(d *Draft) {
			d.Name = ""
			d.Content = Content{
				SMS:   &SMS{Body: strings.Repeat("a", 161) + "{{coupon}}"},
				Email: &Email{Subject: strings.Repeat("b", 81)},
			}
		}, want: []Problem{
			{Field: "name", Code: ProblemRequired},
			{Field: "content.sms.body", Code: ProblemTooLong},
			{Field: "content.sms.body", Code: ProblemUnknownPlaceholder, Detail: "coupon"},
			{Field: "content.email.subject", Code: ProblemTooLong},
			{Field: "content.email.body", Code: ProblemRequired},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDraft()
			if tt.change != nil {
				tt.change(&d)
			}
			if got := problemsOf(t, d); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("problems = %+v, want %+v", got, tt.want)
			}
		})
	}
}
