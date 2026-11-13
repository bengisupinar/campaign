package commerce

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/platform/database"
)

// Store writes the catalogue, orders and baskets to PostgreSQL. It holds only what seeding
// needs; reads and updates arrive with their first caller.
type Store struct{ db *gorm.DB }

// NewStore returns a store that runs its statements on db, which may be a transaction.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// The row types map the tables one to one. They stay unexported, so GORM never reaches the
// domain types.

type categoryRow struct {
	ID   int64
	Name string
}

func (categoryRow) TableName() string { return "categories" }

type productRow struct {
	ID         int64
	CategoryID int64
	Name       string
	PriceMinor int64
}

func (productRow) TableName() string { return "products" }

type orderRow struct {
	ID              int64
	CustomerID      int64
	Status          string
	OrderedAt       time.Time
	StatusChangedAt time.Time
}

func (orderRow) TableName() string { return "orders" }

type orderItemRow struct {
	OrderID        int64 `gorm:"primaryKey;autoIncrement:false"`
	ProductID      int64 `gorm:"primaryKey;autoIncrement:false"`
	Quantity       int
	UnitPriceMinor int64
}

func (orderItemRow) TableName() string { return "order_items" }

type basketItemRow struct {
	CustomerID int64 `gorm:"primaryKey;autoIncrement:false"`
	ProductID  int64 `gorm:"primaryKey;autoIncrement:false"`
	Quantity   int
	AddedAt    time.Time
}

func (basketItemRow) TableName() string { return "basket_items" }

// CreateCategories inserts categories and sets their IDs.
func (s *Store) CreateCategories(ctx context.Context, categories []Category) error {
	rows := make([]categoryRow, len(categories))
	for i, c := range categories {
		rows[i] = categoryRow{Name: c.Name}
	}
	if err := database.Insert(ctx, s.db, rows); err != nil {
		return fmt.Errorf("create categories: %w", err)
	}
	for i := range categories {
		categories[i].ID = rows[i].ID
	}
	return nil
}

// CreateProducts inserts products and sets their IDs.
func (s *Store) CreateProducts(ctx context.Context, products []Product) error {
	rows := make([]productRow, len(products))
	for i, p := range products {
		rows[i] = productRow{CategoryID: p.CategoryID, Name: p.Name, PriceMinor: int64(p.Price)}
	}
	if err := database.Insert(ctx, s.db, rows); err != nil {
		return fmt.Errorf("create products: %w", err)
	}
	for i := range products {
		products[i].ID = rows[i].ID
	}
	return nil
}

// CreateOrders inserts new orders with their lines and sets their IDs.
func (s *Store) CreateOrders(ctx context.Context, orders []*Order) error {
	rows := make([]orderRow, len(orders))
	for i, o := range orders {
		if o.id != 0 {
			return errors.New("create orders: an order is already saved")
		}
		rows[i] = orderRow{
			CustomerID:      o.customerID,
			Status:          string(o.status),
			OrderedAt:       o.orderedAt,
			StatusChangedAt: o.statusChangedAt,
		}
	}
	if err := database.Insert(ctx, s.db, rows); err != nil {
		return fmt.Errorf("create orders: %w", err)
	}

	var items []orderItemRow
	for i, o := range orders {
		o.id = rows[i].ID
		for _, l := range o.lines {
			items = append(items, orderItemRow{
				OrderID:        o.id,
				ProductID:      l.ProductID,
				Quantity:       l.Quantity,
				UnitPriceMinor: int64(l.UnitPrice),
			})
		}
	}
	if err := database.Insert(ctx, s.db, items); err != nil {
		return fmt.Errorf("create order lines: %w", err)
	}
	return nil
}

// CreateBaskets inserts the lines of open baskets.
func (s *Store) CreateBaskets(ctx context.Context, baskets []*Basket) error {
	var rows []basketItemRow
	for _, b := range baskets {
		for _, item := range b.Items() {
			rows = append(rows, basketItemRow{
				CustomerID: b.customerID,
				ProductID:  item.ProductID,
				Quantity:   item.Quantity,
				AddedAt:    item.AddedAt,
			})
		}
	}
	if err := database.Insert(ctx, s.db, rows); err != nil {
		return fmt.Errorf("create basket lines: %w", err)
	}
	return nil
}
