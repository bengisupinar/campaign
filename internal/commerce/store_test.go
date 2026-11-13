package commerce

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
)

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`.

func TestStoreCreatesCatalogueOrdersAndBaskets(t *testing.T) {
	db := databasetest.New(t)
	ctx := t.Context()
	store := NewStore(db)
	customerID := insertCustomer(t, db)

	categories := []Category{{Name: "Electronics"}, {Name: "Books"}}
	if err := store.CreateCategories(ctx, categories); err != nil {
		t.Fatalf("CreateCategories(): %v", err)
	}
	headphones := mustProduct(t, categories[0].ID, "Headphones", 149900)
	novel := mustProduct(t, categories[1].ID, "Novel", 29900)
	products := []Product{headphones, novel}
	if err := store.CreateProducts(ctx, products); err != nil {
		t.Fatalf("CreateProducts(): %v", err)
	}
	if products[0].ID == 0 || products[1].ID == 0 || products[0].ID == products[1].ID {
		t.Fatalf("product IDs = %d and %d, want two distinct IDs", products[0].ID, products[1].ID)
	}

	completed := mustOrder(t, customerID, placed, []OrderLine{{products[0].ID, 2, 149900}, {products[1].ID, 1, 29900}})
	refunded := mustOrder(t, customerID, placed.Add(24*time.Hour), []OrderLine{{products[1].ID, 1, 29900}})
	if err := refunded.Refund(placed.Add(10 * 24 * time.Hour)); err != nil {
		t.Fatalf("Refund(): %v", err)
	}
	if err := store.CreateOrders(ctx, []*Order{completed, refunded}); err != nil {
		t.Fatalf("CreateOrders(): %v", err)
	}
	if completed.ID() == 0 || refunded.ID() == 0 || completed.ID() == refunded.ID() {
		t.Fatalf("order IDs = %d and %d, want two distinct IDs", completed.ID(), refunded.ID())
	}

	basket, err := NewBasket(customerID)
	if err != nil {
		t.Fatalf("NewBasket(): %v", err)
	}
	if err := basket.Add(products[0].ID, 1, placed.Add(48*time.Hour)); err != nil {
		t.Fatalf("Add(): %v", err)
	}
	if err := store.CreateBaskets(ctx, []*Basket{basket}); err != nil {
		t.Fatalf("CreateBaskets(): %v", err)
	}

	var gotProducts []productRow
	mustFind(t, db.Order("id"), &gotProducts)
	wantProducts := []productRow{
		{ID: products[0].ID, CategoryID: categories[0].ID, Name: "Headphones", PriceMinor: 149900},
		{ID: products[1].ID, CategoryID: categories[1].ID, Name: "Novel", PriceMinor: 29900},
	}
	if !reflect.DeepEqual(gotProducts, wantProducts) {
		t.Errorf("products = %+v, want %+v", gotProducts, wantProducts)
	}

	var gotOrders []orderRow
	mustFind(t, db.Order("id"), &gotOrders)
	for i := range gotOrders {
		gotOrders[i].OrderedAt = gotOrders[i].OrderedAt.UTC()
		gotOrders[i].StatusChangedAt = gotOrders[i].StatusChangedAt.UTC()
	}
	wantOrders := []orderRow{
		{ID: completed.ID(), CustomerID: customerID, Status: "completed", OrderedAt: placed, StatusChangedAt: placed},
		{ID: refunded.ID(), CustomerID: customerID, Status: "refunded", OrderedAt: placed.Add(24 * time.Hour),
			StatusChangedAt: placed.Add(10 * 24 * time.Hour)},
	}
	if !reflect.DeepEqual(gotOrders, wantOrders) {
		t.Errorf("orders = %+v, want %+v", gotOrders, wantOrders)
	}

	for _, o := range []*Order{completed, refunded} {
		var total int64
		if err := db.Raw("SELECT sum(quantity * unit_price_minor) FROM order_items WHERE order_id = ?", o.ID()).Scan(&total).Error; err != nil {
			t.Fatalf("sum order lines: %v", err)
		}
		if Money(total) != o.Total() {
			t.Errorf("order %d lines sum to %d in the database, want Total() = %d", o.ID(), total, o.Total())
		}
	}

	var gotBasket []basketItemRow
	mustFind(t, db.Order("product_id"), &gotBasket)
	for i := range gotBasket {
		gotBasket[i].AddedAt = gotBasket[i].AddedAt.UTC()
	}
	wantBasket := []basketItemRow{{CustomerID: customerID, ProductID: products[0].ID, Quantity: 1, AddedAt: placed.Add(48 * time.Hour)}}
	if !reflect.DeepEqual(gotBasket, wantBasket) {
		t.Errorf("basket lines = %+v, want %+v", gotBasket, wantBasket)
	}

	if err := store.CreateOrders(ctx, []*Order{completed}); err == nil {
		t.Error("CreateOrders() saved an already saved order again")
	}
}

// insertCustomer inserts the reference data and customer that orders and baskets need, and
// returns the customer's ID.
func insertCustomer(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO countries (code, name) VALUES ('TR', 'Türkiye')`,
		`INSERT INTO cities (country_code, name) VALUES ('TR', 'İstanbul')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	var id int64
	err := db.Raw(`INSERT INTO customers (first_name, last_name, email, city_id, preferred_language)
		SELECT 'Ayşe', 'Yılmaz', 'ayse@example.com', id, 'tr' FROM cities RETURNING id`).Scan(&id).Error
	if err != nil || id == 0 {
		t.Fatalf("insert customer: id %d, %v", id, err)
	}
	return id
}

func mustProduct(t *testing.T, categoryID int64, name string, price Money) Product {
	t.Helper()
	p, err := NewProduct(categoryID, name, price)
	if err != nil {
		t.Fatalf("NewProduct(): %v", err)
	}
	return p
}

func mustOrder(t *testing.T, customerID int64, at time.Time, lines []OrderLine) *Order {
	t.Helper()
	o, err := NewOrder(customerID, at, lines)
	if err != nil {
		t.Fatalf("NewOrder(): %v", err)
	}
	return o
}

func mustFind(t *testing.T, query *gorm.DB, dest any) {
	t.Helper()
	if err := query.Find(dest).Error; err != nil {
		t.Fatalf("read rows: %v", err)
	}
}
