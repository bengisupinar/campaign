package customer

import "slices"

// Language is a customer's preferred language for messages, as an ISO 639-1 code.
//
// The set is closed: supporting another language needs content generation for it, and the
// customers table's CHECK constraint lists the same codes.
type Language string

// Supported languages.
const (
	LanguageTurkish Language = "tr"
	LanguageGerman  Language = "de"
	LanguageEnglish Language = "en"
)

// Languages returns the supported languages.
func Languages() []Language {
	return []Language{LanguageTurkish, LanguageGerman, LanguageEnglish}
}

// Valid reports whether l is a supported language.
func (l Language) Valid() bool { return slices.Contains(Languages(), l) }
