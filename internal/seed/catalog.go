package seed

import (
	"fmt"

	"github.com/bengisupinar/campaign/internal/commerce"
	"github.com/bengisupinar/campaign/internal/customer"
)

// weighted is a value with its relative frequency.
type weighted[T any] struct {
	value  T
	weight int
}

// market describes the customers of one country.
type market struct {
	country       customer.Country
	share         int // percent of all customers
	cities        []weighted[string]
	languages     []weighted[customer.Language]
	phoneShare    int      // percent of customers with a phone
	phonePrefixes []string // country code and mobile prefix
	phoneDigits   int      // digits after the prefix
}

// markets returns the countries customers live in. Most customers are in Türkiye; German and
// British customers include Turkish speakers, as the diaspora does.
func markets() []market {
	return []market{
		{
			country: customer.Country{Code: "TR", Name: "Türkiye"},
			share:   60,
			cities: []weighted[string]{
				{"İstanbul", 35}, {"Ankara", 14}, {"İzmir", 10}, {"Bursa", 7}, {"Antalya", 6}, {"Adana", 5},
				{"Konya", 5}, {"Gaziantep", 4}, {"Kocaeli", 4}, {"Mersin", 4}, {"Kayseri", 3}, {"Eskişehir", 3},
			},
			languages:     []weighted[customer.Language]{{customer.LanguageTurkish, 92}, {customer.LanguageEnglish, 8}},
			phoneShare:    95,
			phonePrefixes: []string{"+90532", "+90533", "+90542", "+90544", "+90505", "+90555"},
			phoneDigits:   7,
		},
		{
			country: customer.Country{Code: "DE", Name: "Germany"},
			share:   25,
			cities: []weighted[string]{
				{"Berlin", 25}, {"Hamburg", 15}, {"München", 15}, {"Köln", 12},
				{"Frankfurt am Main", 10}, {"Stuttgart", 8}, {"Düsseldorf", 8}, {"Leipzig", 7},
			},
			languages: []weighted[customer.Language]{
				{customer.LanguageGerman, 70}, {customer.LanguageTurkish, 25}, {customer.LanguageEnglish, 5},
			},
			phoneShare:    80,
			phonePrefixes: []string{"+49151", "+49160", "+49170", "+49176"},
			phoneDigits:   7,
		},
		{
			country: customer.Country{Code: "GB", Name: "United Kingdom"},
			share:   15,
			cities: []weighted[string]{
				{"London", 40}, {"Manchester", 12}, {"Birmingham", 12}, {"Glasgow", 8},
				{"Leeds", 8}, {"Liverpool", 7}, {"Edinburgh", 7}, {"Bristol", 6},
			},
			languages:     []weighted[customer.Language]{{customer.LanguageEnglish, 90}, {customer.LanguageTurkish, 10}},
			phoneShare:    85,
			phonePrefixes: []string{"+447700", "+447911", "+447400"},
			phoneDigits:   6,
		},
	}
}

// geography returns the countries and cities of the markets and, per market, the weighted
// choice of its cities as indexes into the returned cities.
func geography(ms []market) ([]customer.Country, []customer.City, [][]weighted[int]) {
	countries := make([]customer.Country, 0, len(ms))
	var cities []customer.City
	choices := make([][]weighted[int], len(ms))
	for i, m := range ms {
		countries = append(countries, m.country)
		for _, c := range m.cities {
			choices[i] = append(choices[i], weighted[int]{value: len(cities), weight: c.weight})
			cities = append(cities, customer.City{CountryCode: m.country.Code, Name: c.value})
		}
	}
	return countries, cities, choices
}

type categorySpec struct {
	name   string
	brands []string
	items  []itemSpec
}

type itemSpec struct {
	name  string
	price commerce.Money // the middle brand's price
}

// categorySpecs returns the catalogue: every brand of a category sells every item of it.
func categorySpecs() []categorySpec {
	return []categorySpec{
		{"Electronics", []string{"Voltra", "Nexo", "Pixelon"}, []itemSpec{
			{"Wireless Earbuds", 149990}, {"Smartwatch", 399990}, {"Bluetooth Speaker", 129990}, {"Power Bank", 69990},
			{"Phone Case", 24990}, {"USB-C Charger", 44990}, {"Laptop Stand", 89990},
		}},
		{"Fashion", []string{"Modaline", "Urbanist", "Linencraft"}, []itemSpec{
			{"Sneakers", 229990}, {"Denim Jacket", 189990}, {"Cotton T-Shirt", 34990}, {"Leather Belt", 59990},
			{"Wool Scarf", 49990}, {"Backpack", 119990}, {"Sunglasses", 89990},
		}},
		{"Home & Living", []string{"Evim", "Nordhaus", "Casa Verde"}, []itemSpec{
			{"Coffee Maker", 249990}, {"Bedding Set", 139990}, {"Table Lamp", 79990}, {"Mug Set", 39990},
			{"Throw Pillow", 29990}, {"Storage Basket", 34990}, {"Scented Candle", 19990},
		}},
		{"Beauty & Personal Care", []string{"Lumina", "Rosewell", "Dermalab"}, []itemSpec{
			{"Face Serum", 64990}, {"Moisturiser", 49990}, {"Shampoo", 22990}, {"Eau de Parfum", 179990},
			{"Lipstick", 34990}, {"Hair Dryer", 149990}, {"Sunscreen", 39990},
		}},
		{"Sports & Outdoors", []string{"Peakline", "Stride", "Aquafit"}, []itemSpec{
			{"Running Shoes", 279990}, {"Yoga Mat", 44990}, {"Dumbbell Set", 129990}, {"Water Bottle", 19990},
			{"Camping Tent", 349990}, {"Cycling Helmet", 109990}, {"Fitness Tracker", 189990},
		}},
		{"Books & Stationery", []string{"Kalem", "Paperwise", "Folio"}, []itemSpec{
			{"Notebook", 12990}, {"Fountain Pen", 59990}, {"Planner", 24990}, {"Novel Box Set", 49990},
			{"Cookbook", 39990}, {"Desk Organizer", 27990}, {"Watercolour Set", 34990},
		}},
		{"Toys & Games", []string{"Oyunbaz", "Brickly", "Funfair"}, []itemSpec{
			{"Building Blocks", 89990}, {"Board Game", 59990}, {"Jigsaw Puzzle", 29990}, {"Plush Bear", 34990},
			{"Remote Control Car", 119990}, {"Science Kit", 74990}, {"Card Game", 19990},
		}},
		{"Grocery & Gourmet", []string{"Anadolu", "Gourmet Hill", "Daily Harvest"}, []itemSpec{
			{"Olive Oil", 34990}, {"Turkish Coffee", 17990}, {"Hazelnut Spread", 15990}, {"Green Tea", 12990},
			{"Dried Apricots", 14990}, {"Flower Honey", 29990}, {"Spice Set", 24990},
		}},
	}
}

// categories returns the categories of the catalogue, without IDs.
func categories(specs []categorySpec) []commerce.Category {
	cs := make([]commerce.Category, len(specs))
	for i, s := range specs {
		cs[i] = commerce.Category{Name: s.name}
	}
	return cs
}

// products returns every brand × item of each category, priced 15% below to 30% above the
// item's price, and per category the indexes of its products. The categories must have IDs.
func products(specs []categorySpec, cs []commerce.Category) ([]commerce.Product, [][]int, error) {
	priceFactors := []int64{85, 100, 130} // percent, per brand
	var ps []commerce.Product
	byCategory := make([][]int, len(specs))
	for ci, s := range specs {
		for bi, brand := range s.brands {
			for _, item := range s.items {
				// Round down to whole lira, then end in ,90 like a shelf price.
				price := commerce.Money(int64(item.price)*priceFactors[bi%len(priceFactors)]/100/100*100 + 90)
				p, err := commerce.NewProduct(cs[ci].ID, brand+" "+item.name, price)
				if err != nil {
					return nil, nil, fmt.Errorf("product %s %s: %w", brand, item.name, err)
				}
				byCategory[ci] = append(byCategory[ci], len(ps))
				ps = append(ps, p)
			}
		}
	}
	return ps, byCategory, nil
}
