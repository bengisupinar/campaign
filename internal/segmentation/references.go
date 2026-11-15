package segmentation

import (
	"context"
	"fmt"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
)

// existingNames lists, with their kind, which of the given country codes, city names and
// category names exist. Its parameters come in pairs: a kind, then the names to look up.
const existingNames = `SELECT ? AS kind, code AS name FROM countries WHERE code IN ?
	UNION ALL SELECT ?, name FROM cities WHERE name IN ?
	UNION ALL SELECT ?, name FROM categories WHERE name IN ?`

// known is a name found in the reference data.
type known struct {
	Kind campaign.ReferenceKind
	Name string
}

// CheckReferences reports every country, city and category the segment names that doesn't
// exist, at the field where it appears and in the segment's order. Names match exactly, case
// included. A segment with problems of its own gets no report, since its names may not even be
// well formed; Segment.Problems lists those. It runs at most one query.
func (s *Service) CheckReferences(ctx context.Context, seg campaign.Segment) ([]campaign.Problem, error) {
	refs := seg.References()
	if len(refs) == 0 || len(seg.Problems()) > 0 {
		return nil, nil
	}
	names := make(map[campaign.ReferenceKind][]string)
	for _, r := range refs {
		names[r.Kind] = append(names[r.Kind], r.Name)
	}
	var found []known
	err := s.db.WithContext(ctx).Raw(existingNames,
		campaign.ReferenceCountry, names[campaign.ReferenceCountry],
		campaign.ReferenceCity, names[campaign.ReferenceCity],
		campaign.ReferenceCategory, names[campaign.ReferenceCategory],
	).Scan(&found).Error
	if err != nil {
		return nil, fmt.Errorf("check segment references: %w", err)
	}
	exists := make(map[known]bool, len(found))
	for _, k := range found {
		exists[k] = true
	}

	var problems []campaign.Problem
	for _, r := range refs {
		if !exists[known{r.Kind, r.Name}] {
			problems = append(problems, campaign.Problem{Field: r.Field, Code: campaign.ProblemUnknown, Detail: r.Name})
		}
	}
	return problems, nil
}

// Options are what a segment's conditions can name: the countries, cities and categories in
// the reference data, and the languages customers speak. The composer offers them as choices.
type Options struct {
	Countries  []CountryOption     `json:"countries"`  // by code
	Cities     []CityOption        `json:"cities"`     // by country, then name
	Categories []string            `json:"categories"` // by name
	Languages  []customer.Language `json:"languages"`
}

// CountryOption is a country, which a segment names by its code.
type CountryOption struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// CityOption is a city a segment can name, and the country it is in.
type CityOption struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

// Options returns what a segment's conditions can name, in three short queries.
func (s *Service) Options(ctx context.Context) (Options, error) {
	opts := Options{Countries: []CountryOption{}, Cities: []CityOption{}, Categories: []string{}, Languages: customer.Languages()}
	db := s.db.WithContext(ctx)
	if err := db.Raw("SELECT code, name FROM countries ORDER BY code").Scan(&opts.Countries).Error; err != nil {
		return Options{}, fmt.Errorf("list countries: %w", err)
	}
	if err := db.Raw("SELECT name, country_code AS country FROM cities ORDER BY country_code, name").Scan(&opts.Cities).Error; err != nil {
		return Options{}, fmt.Errorf("list cities: %w", err)
	}
	if err := db.Raw("SELECT name FROM categories ORDER BY name").Scan(&opts.Categories).Error; err != nil {
		return Options{}, fmt.Errorf("list categories: %w", err)
	}
	return opts, nil
}
