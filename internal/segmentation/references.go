package segmentation

import (
	"context"
	"fmt"

	"github.com/bengisupinar/campaign/internal/campaign"
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
