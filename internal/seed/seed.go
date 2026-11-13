// Package seed fills an empty database with synthetic customers in Türkiye, Germany and the
// United Kingdom, with their permissions, baskets and orders, and the product catalogue.
//
// The data is deterministic: the same seed and the same reference time always produce the
// same rows, and every timestamp is relative to that time, so "the last 30 days" always has
// activity. Seeding is idempotent: a database that already has customers is left alone.
package seed

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/bengisupinar/campaign/internal/commerce"
	"github.com/bengisupinar/campaign/internal/customer"
)

const (
	maxCustomers = 100_000

	// lockKey identifies the PostgreSQL advisory lock that serialises concurrent runs.
	lockKey int64 = 4_224_870_516
)

// Options configures a seeding run.
type Options struct {
	Customers int    // how many customers to create
	Seed      uint64 // the same seed and reference time give the same data
}

// DefaultOptions returns the dataset the application ships with: 5,000 customers.
func DefaultOptions() Options { return Options{Customers: 5000, Seed: 20261114} }

// Result reports what a run inserted.
type Result struct {
	Skipped     bool // the database already had customers, so nothing was inserted
	Customers   int
	Permissions int
	Products    int
	Orders      int
	OrderLines  int
	BasketLines int
}

// Run fills the database with data generated from opts relative to now, in one transaction.
// It does nothing when customers already exist, so it can run on every start, and concurrent
// runs seed once.
func Run(ctx context.Context, db *gorm.DB, opts Options, now time.Time) (Result, error) {
	if opts.Customers < 1 || opts.Customers > maxCustomers {
		return Result{}, fmt.Errorf("seed: customers must be between 1 and %d", maxCustomers)
	}
	// Slow-query warnings would print bulk inserts with thousands of placeholders.
	quiet := db.Session(&gorm.Session{Logger: db.Logger.LogMode(gormlogger.Error)})

	var result Result
	err := quiet.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockKey).Error; err != nil {
			return fmt.Errorf("lock: %w", err)
		}
		customerStore := customer.NewStore(tx)
		seeded, err := customerStore.HasCustomers(ctx)
		if err != nil {
			return err
		}
		if seeded {
			result.Skipped = true
			return nil
		}

		commerceStore := commerce.NewStore(tx)
		d, err := build(newGenerator(opts.Seed, now), opts.Customers, sink{
			countries:  func(cs []customer.Country) error { return customerStore.CreateCountries(ctx, cs) },
			cities:     func(cs []customer.City) error { return customerStore.CreateCities(ctx, cs) },
			categories: func(cs []commerce.Category) error { return commerceStore.CreateCategories(ctx, cs) },
			products:   func(ps []commerce.Product) error { return commerceStore.CreateProducts(ctx, ps) },
			customers: func(cs []*customer.Customer) ([]int64, error) {
				if err := customerStore.CreateCustomers(ctx, cs); err != nil {
					return nil, err
				}
				ids := make([]int64, len(cs))
				for i, c := range cs {
					ids[i] = c.ID()
				}
				return ids, nil
			},
			orders:  func(os []*commerce.Order) error { return commerceStore.CreateOrders(ctx, os) },
			baskets: func(bs []*commerce.Basket) error { return commerceStore.CreateBaskets(ctx, bs) },
		})
		if err != nil {
			return err
		}
		result = summarize(d)
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("seed: %w", err)
	}
	return result, nil
}

// sink receives each batch as soon as it is generated and must give it the IDs later batches
// refer to. Run writes the batches to the database; tests number them in memory.
type sink struct {
	countries  func([]customer.Country) error
	cities     func([]customer.City) error // sets the IDs
	categories func([]commerce.Category) error
	products   func([]commerce.Product) error
	customers  func([]*customer.Customer) ([]int64, error) // returns the IDs
	orders     func([]*commerce.Order) error
	baskets    func([]*commerce.Basket) error
}

// dataset is everything one run generates.
type dataset struct {
	countries  []customer.Country
	cities     []customer.City
	categories []commerce.Category
	products   []commerce.Product
	customers  []*customer.Customer
	shoppers   []shopper
	orders     []*commerce.Order
	baskets    []*commerce.Basket
}

// build generates the dataset in dependency order, handing each batch to out before the
// batches that refer to it.
func build(g *generator, customers int, out sink) (dataset, error) {
	var d dataset
	ms := markets()
	var cityChoices [][]weighted[int]
	d.countries, d.cities, cityChoices = geography(ms)
	if err := out.countries(d.countries); err != nil {
		return dataset{}, err
	}
	if err := out.cities(d.cities); err != nil {
		return dataset{}, err
	}

	specs := categorySpecs()
	d.categories = categories(specs)
	if err := out.categories(d.categories); err != nil {
		return dataset{}, err
	}
	var byCategory [][]int
	var err error
	if d.products, byCategory, err = products(specs, d.categories); err != nil {
		return dataset{}, err
	}
	if err := out.products(d.products); err != nil {
		return dataset{}, err
	}

	if d.customers, d.shoppers, err = g.customers(customers, ms, cityChoices, d.cities, len(d.categories)); err != nil {
		return dataset{}, err
	}
	ids, err := out.customers(d.customers)
	if err != nil {
		return dataset{}, err
	}
	for i := range d.shoppers {
		d.shoppers[i].id = ids[i]
	}

	if d.orders, d.baskets, err = g.activity(d.shoppers, d.products, byCategory); err != nil {
		return dataset{}, err
	}
	if err := out.orders(d.orders); err != nil {
		return dataset{}, err
	}
	if err := out.baskets(d.baskets); err != nil {
		return dataset{}, err
	}
	return d, nil
}

func summarize(d dataset) Result {
	r := Result{Customers: len(d.customers), Products: len(d.products), Orders: len(d.orders)}
	for _, c := range d.customers {
		r.Permissions += len(c.Permissions())
	}
	for _, o := range d.orders {
		r.OrderLines += len(o.Lines())
	}
	for _, b := range d.baskets {
		r.BasketLines += len(b.Items())
	}
	return r
}
