package seed

import (
	"fmt"
	"strings"

	"github.com/bengisupinar/campaign/internal/customer"
)

// names returns common first and last names among speakers of a language.
func names(l customer.Language) (first, last []string) {
	switch l {
	case customer.LanguageTurkish:
		return []string{
			"Ayşe", "Fatma", "Zeynep", "Elif", "Merve", "Emine", "Esra", "Selin", "Deniz", "Ece",
			"Mehmet", "Mustafa", "Ahmet", "Ali", "Emre", "Can", "Burak", "Murat", "Kerem", "Oğuz",
		}, []string{
			"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir",
			"Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek",
		}
	case customer.LanguageGerman:
		return []string{
			"Anna", "Emma", "Mia", "Hannah", "Lena", "Lea", "Sophie", "Marie", "Laura", "Julia",
			"Lukas", "Leon", "Finn", "Jonas", "Paul", "Felix", "Maximilian", "Elias", "Noah", "Tim",
		}, []string{
			"Müller", "Schmidt", "Schneider", "Fischer", "Weber", "Meyer", "Wagner", "Becker", "Schulz", "Hoffmann",
			"Schäfer", "Koch", "Bauer", "Richter", "Klein", "Wolf", "Schröder", "Neumann", "Schwarz", "Zimmermann",
		}
	default:
		return []string{
			"Olivia", "Amelia", "Isla", "Ava", "Emily", "Sophia", "Grace", "Lily", "Freya", "Ella",
			"Oliver", "George", "Harry", "Jack", "Charlie", "Thomas", "James", "William", "Henry", "Oscar",
		}, []string{
			"Smith", "Jones", "Taylor", "Brown", "Williams", "Wilson", "Johnson", "Davies", "Robinson", "Wright",
			"Thompson", "Evans", "Walker", "White", "Roberts", "Green", "Hall", "Wood", "Jackson", "Clarke",
		}
	}
}

// newASCIIFolder returns a replacer that spells the Turkish and German letters of names in
// ASCII, for email addresses.
func newASCIIFolder() *strings.Replacer {
	return strings.NewReplacer(
		"Ç", "C", "ç", "c", "Ğ", "G", "ğ", "g", "İ", "I", "ı", "i", "Ö", "O", "ö", "o",
		"Ş", "S", "ş", "s", "Ü", "U", "ü", "u", "Ä", "A", "ä", "a", "ß", "ss",
	)
}

// email returns the address of the seq-th customer, e.g. ayse.yilmaz.42@example.com. The
// sequence number makes it unique, and the domains are reserved for examples (RFC 2606), so
// no address can reach a real mailbox.
func (g *generator) email(first, last string, seq int) (customer.Email, error) {
	domains := []string{"example.com", "example.net", "example.org"}
	address := fmt.Sprintf("%s.%s.%d@%s", g.fold.Replace(first), g.fold.Replace(last), seq, domains[seq%len(domains)])
	return customer.ParseEmail(address)
}

// phone returns the synthetic mobile number of the seq-th customer in a market. The sequence
// number makes it unique.
func (g *generator) phone(m market, seq int) (customer.Phone, error) {
	prefix := m.phonePrefixes[g.rng.IntN(len(m.phonePrefixes))]
	return customer.ParsePhone(fmt.Sprintf("%s%0*d", prefix, m.phoneDigits, seq))
}
