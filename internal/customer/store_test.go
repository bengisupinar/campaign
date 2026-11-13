package customer

import (
	"reflect"
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
)

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`.

func TestStoreCreatesCustomersWithPermissions(t *testing.T) {
	db := databasetest.New(t)
	ctx := t.Context()
	store := NewStore(db)

	if err := store.CreateCountries(ctx, []Country{{Code: "TR", Name: "Türkiye"}, {Code: "DE", Name: "Germany"}}); err != nil {
		t.Fatalf("CreateCountries(): %v", err)
	}
	cities := []City{{CountryCode: "TR", Name: "İstanbul"}, {CountryCode: "DE", Name: "Berlin"}}
	if err := store.CreateCities(ctx, cities); err != nil {
		t.Fatalf("CreateCities(): %v", err)
	}
	if cities[0].ID == 0 || cities[1].ID == 0 || cities[0].ID == cities[1].ID {
		t.Fatalf("city IDs = %d and %d, want two distinct IDs", cities[0].ID, cities[1].ID)
	}

	ayse := validProfile(t)
	ayse.CityID = cities[0].ID
	withPhone := mustNew(t, ayse, signedUp)
	mustDo(t, withPhone.Grant(ChannelEmail, signedUp))
	mustDo(t, withPhone.Grant(ChannelSMS, signedUp))
	mustDo(t, withPhone.Revoke(ChannelSMS, signedUp.Add(72*time.Hour)))

	jonas := Profile{
		FirstName: "Jonas",
		LastName:  "Weber",
		Email:     mustEmail(t, "jonas.weber@example.net"),
		CityID:    cities[1].ID,
		Language:  LanguageGerman,
	}
	withoutPhone := mustNew(t, jonas, signedUp.Add(24*time.Hour))
	mustDo(t, withoutPhone.Revoke(ChannelEmail, signedUp.Add(24*time.Hour)))

	if err := store.CreateCustomers(ctx, []*Customer{withPhone, withoutPhone}); err != nil {
		t.Fatalf("CreateCustomers(): %v", err)
	}
	if withPhone.ID() == 0 || withoutPhone.ID() == 0 || withPhone.ID() == withoutPhone.ID() {
		t.Fatalf("customer IDs = %d and %d, want two distinct IDs", withPhone.ID(), withoutPhone.ID())
	}

	var customers []customerRow
	if err := db.Order("id").Find(&customers).Error; err != nil {
		t.Fatalf("read customers: %v", err)
	}
	phone := "+905321234567"
	wantCustomers := []customerRow{
		{ID: withPhone.ID(), FirstName: "Ayşe", LastName: "Yılmaz", Email: "ayse@example.com", Phone: &phone,
			CityID: cities[0].ID, PreferredLanguage: "tr", CreatedAt: signedUp},
		{ID: withoutPhone.ID(), FirstName: "Jonas", LastName: "Weber", Email: "jonas.weber@example.net",
			CityID: cities[1].ID, PreferredLanguage: "de", CreatedAt: signedUp.Add(24 * time.Hour)},
	}
	for i := range customers {
		customers[i].CreatedAt = customers[i].CreatedAt.UTC()
	}
	if !reflect.DeepEqual(customers, wantCustomers) {
		t.Errorf("customers = %+v, want %+v", customers, wantCustomers)
	}

	var permissions []permissionRow
	if err := db.Order("customer_id, channel").Find(&permissions).Error; err != nil {
		t.Fatalf("read permissions: %v", err)
	}
	wantPermissions := []permissionRow{
		{CustomerID: withPhone.ID(), Channel: "email", Granted: true, UpdatedAt: signedUp},
		{CustomerID: withPhone.ID(), Channel: "sms", Granted: false, UpdatedAt: signedUp.Add(72 * time.Hour)},
		{CustomerID: withoutPhone.ID(), Channel: "email", Granted: false, UpdatedAt: signedUp.Add(24 * time.Hour)},
	}
	for i := range permissions {
		permissions[i].UpdatedAt = permissions[i].UpdatedAt.UTC()
	}
	if !reflect.DeepEqual(permissions, wantPermissions) {
		t.Errorf("permissions = %+v, want %+v", permissions, wantPermissions)
	}

	if err := store.CreateCustomers(ctx, []*Customer{withPhone}); err == nil {
		t.Error("CreateCustomers() saved an already saved customer again")
	}
}

func TestStoreHasCustomers(t *testing.T) {
	db := databasetest.New(t)
	ctx := t.Context()
	store := NewStore(db)

	if has, err := store.HasCustomers(ctx); err != nil || has {
		t.Fatalf("HasCustomers() on an empty database = %v, %v; want false", has, err)
	}
	if err := store.CreateCountries(ctx, []Country{{Code: "TR", Name: "Türkiye"}}); err != nil {
		t.Fatalf("CreateCountries(): %v", err)
	}
	cities := []City{{CountryCode: "TR", Name: "İstanbul"}}
	if err := store.CreateCities(ctx, cities); err != nil {
		t.Fatalf("CreateCities(): %v", err)
	}
	p := validProfile(t)
	p.CityID = cities[0].ID
	if err := store.CreateCustomers(ctx, []*Customer{mustNew(t, p, signedUp)}); err != nil {
		t.Fatalf("CreateCustomers(): %v", err)
	}
	if has, err := store.HasCustomers(ctx); err != nil || !has {
		t.Fatalf("HasCustomers() after creating one = %v, %v; want true", has, err)
	}
}

func mustNew(t *testing.T, p Profile, createdAt time.Time) *Customer {
	t.Helper()
	c, err := New(p, createdAt)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return c
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
