package migrations_test

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`. Each test works in a throwaway database.

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
	"github.com/bengisupinar/campaign/migrations"
)

func TestSchemaConstraints(t *testing.T) {
	db := newTestDatabase(t)
	insertValidRows(t, db)
	// Campaign 2 is being sent; its SMS to customer 1 was accepted.
	execute(t, db, `INSERT INTO campaigns (name, status, segment, sms_body, created_at, updated_at, started_at)
		VALUES ('Sending', 'sending', '{}', 'Hi', now(), now(), now())`)
	execute(t, db, insertDelivery+`(2, 1, 'sms', 'accepted', 'p-1', NULL, now(), now())`)

	// Each statement breaks exactly one constraint, by colliding with or referring to the
	// valid rows.
	tests := []struct {
		name       string
		statement  string
		constraint string
	}{
		{"country codes are unique", `INSERT INTO countries (code, name) VALUES ('TR', 'Turkey')`, "countries_pkey"},
		{"country codes are two capital letters", `INSERT INTO countries (code, name) VALUES ('de', 'Germany')`, "countries_code_check"},
		{"country names are unique", `INSERT INTO countries (code, name) VALUES ('DE', 'Türkiye')`, "countries_name_key"},
		{"city names are unique within a country", `INSERT INTO cities (country_code, name) VALUES ('TR', 'İstanbul')`, "cities_country_code_name_key"},
		{"cities belong to a known country", `INSERT INTO cities (country_code, name) VALUES ('DE', 'Berlin')`, "cities_country_code_fkey"},

		{"emails are unique", `INSERT INTO customers (first_name, last_name, email, phone, city_id, preferred_language)
			VALUES ('Can', 'Demir', 'ayse@example.com', NULL, 1, 'tr')`, "customers_email_key"},
		{"phones are unique", `INSERT INTO customers (first_name, last_name, email, phone, city_id, preferred_language)
			VALUES ('Can', 'Demir', 'can@example.com', '+905321234567', 1, 'tr')`, "customers_phone_key"},
		{"phones are E.164", `INSERT INTO customers (first_name, last_name, email, phone, city_id, preferred_language)
			VALUES ('Can', 'Demir', 'can@example.com', '05321234567', 1, 'tr')`, "customers_phone_check"},
		{"languages are supported", `INSERT INTO customers (first_name, last_name, email, phone, city_id, preferred_language)
			VALUES ('Can', 'Demir', 'can@example.com', NULL, 1, 'fr')`, "customers_preferred_language_check"},
		{"customers live in a known city", `INSERT INTO customers (first_name, last_name, email, phone, city_id, preferred_language)
			VALUES ('Can', 'Demir', 'can@example.com', NULL, 999, 'tr')`, "customers_city_id_fkey"},
		{"customers with orders cannot be deleted", `DELETE FROM customers WHERE id = 1`, "orders_customer_id_fkey"},

		{"one permission per channel", `INSERT INTO notification_permissions (customer_id, channel, granted, updated_at)
			VALUES (1, 'sms', false, now())`, "notification_permissions_pkey"},
		{"channels are sms or email", `INSERT INTO notification_permissions (customer_id, channel, granted, updated_at)
			VALUES (1, 'push', true, now())`, "notification_permissions_channel_check"},
		{"permissions belong to a known customer", `INSERT INTO notification_permissions (customer_id, channel, granted, updated_at)
			VALUES (999, 'email', true, now())`, "notification_permissions_customer_id_fkey"},

		{"category names are unique", `INSERT INTO categories (name) VALUES ('Electronics')`, "categories_name_key"},
		{"product names are unique", `INSERT INTO products (category_id, name, price_minor) VALUES (1, 'Headphones', 100)`, "products_name_key"},
		{"product prices are above zero", `INSERT INTO products (category_id, name, price_minor) VALUES (1, 'Mouse', 0)`, "products_price_minor_check"},
		{"products belong to a known category", `INSERT INTO products (category_id, name, price_minor) VALUES (999, 'Mouse', 100)`, "products_category_id_fkey"},

		{"one basket line per product", `INSERT INTO basket_items (customer_id, product_id, quantity, added_at)
			VALUES (1, 1, 2, now())`, "basket_items_pkey"},
		{"basket quantities are above zero", `INSERT INTO basket_items (customer_id, product_id, quantity, added_at)
			VALUES (1, 2, 0, now())`, "basket_items_quantity_check"},
		{"baskets belong to a known customer", `INSERT INTO basket_items (customer_id, product_id, quantity, added_at)
			VALUES (999, 2, 1, now())`, "basket_items_customer_id_fkey"},
		{"basket lines hold a known product", `INSERT INTO basket_items (customer_id, product_id, quantity, added_at)
			VALUES (1, 999, 1, now())`, "basket_items_product_id_fkey"},

		{"order statuses are known", `INSERT INTO orders (customer_id, status, ordered_at, status_changed_at)
			VALUES (1, 'shipped', now(), now())`, "orders_status_check"},
		{"status changes never predate the order", `INSERT INTO orders (customer_id, status, ordered_at, status_changed_at)
			VALUES (1, 'cancelled', now(), now() - interval '1 day')`, "orders_status_changed_at_check"},
		{"orders belong to a known customer", `INSERT INTO orders (customer_id, status, ordered_at, status_changed_at)
			VALUES (999, 'completed', now(), now())`, "orders_customer_id_fkey"},

		{"one order line per product", `INSERT INTO order_items (order_id, product_id, quantity, unit_price_minor)
			VALUES (1, 1, 2, 149900)`, "order_items_pkey"},
		{"order quantities are above zero", `INSERT INTO order_items (order_id, product_id, quantity, unit_price_minor)
			VALUES (1, 2, 0, 89900)`, "order_items_quantity_check"},
		{"unit prices are not negative", `INSERT INTO order_items (order_id, product_id, quantity, unit_price_minor)
			VALUES (1, 2, 1, -1)`, "order_items_unit_price_minor_check"},
		{"order lines belong to a known order", `INSERT INTO order_items (order_id, product_id, quantity, unit_price_minor)
			VALUES (999, 2, 1, 89900)`, "order_items_order_id_fkey"},
		{"order lines hold a known product", `INSERT INTO order_items (order_id, product_id, quantity, unit_price_minor)
			VALUES (1, 999, 1, 89900)`, "order_items_product_id_fkey"},

		{"campaign names are 1 to 100 characters", insertCampaign +
			`('', 'draft', '{}', 'Hi', NULL, NULL, now(), now())`, "campaigns_name_check"},
		{"campaign statuses are known", insertCampaign +
			`('Sale', 'done', '{}', 'Hi', NULL, NULL, now(), now())`, "campaigns_status_check"},
		{"scheduled campaigns have a time", insertCampaign +
			`('Sale', 'scheduled', '{}', 'Hi', NULL, NULL, now(), now())`, "campaigns_scheduled_at_check"},
		{"sending campaigns have started", insertCampaign +
			`('Sale', 'sending', '{}', 'Hi', NULL, NULL, now(), now())`, "campaigns_started_at_check"},
		{"sent campaigns have finished", `INSERT INTO campaigns (name, status, segment, sms_body, created_at, updated_at, started_at)
			VALUES ('Sale', 'sent', '{}', 'Hi', now(), now(), now())`, "campaigns_finished_at_check"},

		{"one message per campaign, customer and channel", insertDelivery +
			`(2, 1, 'sms', 'pending', NULL, NULL, now(), now())`, "deliveries_campaign_id_customer_id_channel_key"},
		{"messages go out by email or SMS", insertDelivery +
			`(2, 1, 'push', 'pending', NULL, NULL, now(), now())`, "deliveries_channel_check"},
		{"message statuses are known", insertDelivery +
			`(2, 1, 'email', 'sent', NULL, NULL, now(), now())`, "deliveries_status_check"},
		{"accepted messages have a provider ID", insertDelivery +
			`(2, 1, 'email', 'accepted', NULL, NULL, now(), now())`, "deliveries_accepted_check"},
		{"failed messages have a reason", insertDelivery +
			`(2, 1, 'email', 'failed', NULL, NULL, now(), now())`, "deliveries_reason_check"},
		{"provider IDs are unique", insertDelivery +
			`(2, 1, 'email', 'accepted', 'p-1', NULL, now(), now())`, "deliveries_provider_message_id_key"},
		{"messages belong to a known campaign", insertDelivery +
			`(999, 1, 'email', 'pending', NULL, NULL, now(), now())`, "deliveries_campaign_id_fkey"},
		{"the mock's acceptances have a provider ID", `INSERT INTO mock_provider_messages
			(idempotency_key, channel, outcome, provider_message_id, attempts, created_at, updated_at)
			VALUES ('delivery-1', 'sms', 'accepted', NULL, 1, now(), now())`, "mock_provider_messages_accepted_check"},
		{"segments are JSON objects", insertCampaign +
			`('Sale', 'draft', '[]', 'Hi', NULL, NULL, now(), now())`, "campaigns_segment_check"},
		{"campaigns use a channel", insertCampaign +
			`('Sale', 'draft', '{}', NULL, NULL, NULL, now(), now())`, "campaigns_channel_check"},
		{"SMS bodies fit in 160 characters", insertCampaign +
			`('Sale', 'draft', '{}', repeat('ş', 161), NULL, NULL, now(), now())`, "campaigns_sms_body_check"},
		{"email subjects fit in 80 characters", insertCampaign +
			`('Sale', 'draft', '{}', NULL, repeat('ı', 81), 'Hello', now(), now())`, "campaigns_email_subject_check"},
		{"emails have a subject and a body", insertCampaign +
			`('Sale', 'draft', '{}', NULL, 'Hi', NULL, now(), now())`, "campaigns_email_check"},
		{"updates never predate creation", insertCampaign +
			`('Sale', 'draft', '{}', 'Hi', NULL, NULL, now(), now() - interval '1 day')`, "campaigns_updated_at_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := db.ExecContext(t.Context(), tt.statement)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("error = %v, want a violation of %s", err, tt.constraint)
			}
			if pgErr.ConstraintName != tt.constraint {
				t.Errorf("violated %q (%s), want %q", pgErr.ConstraintName, pgErr.Message, tt.constraint)
			}
		})
	}
}

func TestCascadingDeletes(t *testing.T) {
	db := newTestDatabase(t)
	insertValidRows(t, db)

	// Order lines belong to their order.
	execute(t, db, `DELETE FROM orders WHERE id = 1`)
	if n := count(t, db, `SELECT count(*) FROM order_items WHERE order_id = 1`); n != 0 {
		t.Errorf("deleting an order left %d of its lines", n)
	}

	// Without orders, a customer can be deleted, and their permissions and basket go too.
	execute(t, db, `DELETE FROM customers WHERE id = 1`)
	for _, query := range []string{
		`SELECT count(*) FROM notification_permissions WHERE customer_id = 1`,
		`SELECT count(*) FROM basket_items WHERE customer_id = 1`,
	} {
		if n := count(t, db, query); n != 0 {
			t.Errorf("deleting a customer left %d rows: %s", n, query)
		}
	}
}

func TestMigrationsRoundTrip(t *testing.T) {
	db := newTestDatabase(t)
	ctx := t.Context()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	sources := provider.ListSources()
	latest := sources[len(sources)-1].Version

	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("migrate down to 0: %v", err)
	}
	if v := dbVersion(t, provider); v != 0 {
		t.Errorf("version after migrating down = %d, want 0", v)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	if v := dbVersion(t, provider); v != latest {
		t.Errorf("version after migrating up = %d, want %d", v, latest)
	}
}

// insertCampaign and insertDelivery start INSERTs; the cases add the values.
const (
	insertCampaign = `INSERT INTO campaigns
	(name, status, segment, sms_body, email_subject, email_body, created_at, updated_at) VALUES `
	insertDelivery = `INSERT INTO deliveries
	(campaign_id, customer_id, channel, status, provider_message_id, error, created_at, updated_at) VALUES `
)

// insertValidRows inserts one valid row per table (two products), all with ID 1 (products 1
// and 2) in a fresh database.
func insertValidRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO countries (code, name) VALUES ('TR', 'Türkiye')`,
		`INSERT INTO cities (country_code, name) VALUES ('TR', 'İstanbul')`,
		`INSERT INTO customers (first_name, last_name, email, phone, city_id, preferred_language)
			VALUES ('Ayşe', 'Yılmaz', 'ayse@example.com', '+905321234567', 1, 'tr')`,
		`INSERT INTO notification_permissions (customer_id, channel, granted, updated_at) VALUES (1, 'sms', true, now())`,
		`INSERT INTO categories (name) VALUES ('Electronics')`,
		`INSERT INTO products (category_id, name, price_minor) VALUES (1, 'Headphones', 149900), (1, 'Keyboard', 89900)`,
		`INSERT INTO basket_items (customer_id, product_id, quantity, added_at) VALUES (1, 1, 1, now())`,
		insertCampaign + `('Spring sale', 'draft', '{"version": 1, "match": "all", "conditions": []}',
			'Hi {{first_name}}', NULL, NULL, now(), now())`,
		`INSERT INTO orders (customer_id, status, ordered_at, status_changed_at) VALUES (1, 'completed', now(), now())`,
		`INSERT INTO order_items (order_id, product_id, quantity, unit_price_minor) VALUES (1, 1, 1, 149900)`,
	} {
		execute(t, db, statement)
	}
}

// newTestDatabase returns a throwaway database with every migration applied.
func newTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := databasetest.New(t).DB()
	if err != nil {
		t.Fatalf("database handle: %v", err)
	}
	return db
}

func execute(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func count(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func dbVersion(t *testing.T, provider *goose.Provider) int64 {
	t.Helper()
	v, err := provider.GetDBVersion(t.Context())
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	return v
}
