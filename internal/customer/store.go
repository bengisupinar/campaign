package customer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/platform/database"
)

// Store writes customers and their reference data to PostgreSQL. It holds only what seeding
// needs; reads and updates arrive with their first caller.
type Store struct{ db *gorm.DB }

// NewStore returns a store that runs its statements on db, which may be a transaction.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// The row types map the tables one to one. They stay unexported, so GORM never reaches the
// domain types.

type countryRow struct {
	Code string `gorm:"primaryKey"`
	Name string
}

func (countryRow) TableName() string { return "countries" }

type cityRow struct {
	ID          int64
	CountryCode string
	Name        string
}

func (cityRow) TableName() string { return "cities" }

type customerRow struct {
	ID                int64
	FirstName         string
	LastName          string
	Email             string
	Phone             *string // NULL when the customer has no phone
	CityID            int64
	PreferredLanguage string
	CreatedAt         time.Time `gorm:"autoCreateTime:false"`
}

func (customerRow) TableName() string { return "customers" }

type permissionRow struct {
	CustomerID int64  `gorm:"primaryKey;autoIncrement:false"`
	Channel    string `gorm:"primaryKey"`
	Granted    bool
	UpdatedAt  time.Time `gorm:"autoUpdateTime:false"`
}

func (permissionRow) TableName() string { return "notification_permissions" }

// CreateCountries inserts countries.
func (s *Store) CreateCountries(ctx context.Context, countries []Country) error {
	rows := make([]countryRow, len(countries))
	for i, c := range countries {
		rows[i] = countryRow{Code: string(c.Code), Name: c.Name}
	}
	if err := database.Insert(ctx, s.db, rows); err != nil {
		return fmt.Errorf("create countries: %w", err)
	}
	return nil
}

// CreateCities inserts cities and sets their IDs.
func (s *Store) CreateCities(ctx context.Context, cities []City) error {
	rows := make([]cityRow, len(cities))
	for i, c := range cities {
		rows[i] = cityRow{CountryCode: string(c.CountryCode), Name: c.Name}
	}
	if err := database.Insert(ctx, s.db, rows); err != nil {
		return fmt.Errorf("create cities: %w", err)
	}
	for i := range cities {
		cities[i].ID = rows[i].ID
	}
	return nil
}

// CreateCustomers inserts new customers with their permissions and sets their IDs.
func (s *Store) CreateCustomers(ctx context.Context, customers []*Customer) error {
	rows := make([]customerRow, len(customers))
	for i, c := range customers {
		if c.id != 0 {
			return errors.New("create customers: a customer is already saved")
		}
		p := c.profile
		rows[i] = customerRow{
			FirstName:         p.FirstName,
			LastName:          p.LastName,
			Email:             p.Email.String(),
			Phone:             nullable(p.Phone),
			CityID:            p.CityID,
			PreferredLanguage: string(p.Language),
			CreatedAt:         c.createdAt,
		}
	}
	if err := database.Insert(ctx, s.db, rows); err != nil {
		return fmt.Errorf("create customers: %w", err)
	}

	var permissions []permissionRow
	for i, c := range customers {
		c.id = rows[i].ID
		for _, p := range c.Permissions() {
			permissions = append(permissions, permissionRow{
				CustomerID: c.id,
				Channel:    string(p.Channel),
				Granted:    p.Granted,
				UpdatedAt:  p.UpdatedAt,
			})
		}
	}
	if err := database.Insert(ctx, s.db, permissions); err != nil {
		return fmt.Errorf("create permissions: %w", err)
	}
	return nil
}

// HasCustomers reports whether any customer exists.
func (s *Store) HasCustomers(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.WithContext(ctx).Raw("SELECT EXISTS (SELECT 1 FROM customers)").Scan(&exists).Error; err != nil {
		return false, fmt.Errorf("check for customers: %w", err)
	}
	return exists, nil
}

func nullable(p Phone) *string {
	if p.IsZero() {
		return nil
	}
	s := p.String()
	return &s
}
