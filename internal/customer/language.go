package customer

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

func (l Language) valid() bool {
	switch l {
	case LanguageTurkish, LanguageGerman, LanguageEnglish:
		return true
	default:
		return false
	}
}
