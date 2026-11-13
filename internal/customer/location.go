package customer

// CountryCode is an ISO 3166-1 alpha-2 country code, such as TR, DE or GB.
type CountryCode string

// Country is reference data: a country customers live in.
type Country struct {
	Code CountryCode
	Name string
}

// City is reference data: a city in a country. Its name is unique within the country.
type City struct {
	ID          int64
	CountryCode CountryCode
	Name        string
}
