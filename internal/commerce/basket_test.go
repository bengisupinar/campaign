package commerce

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestNewBasket(t *testing.T) {
	b, err := NewBasket(42)
	if err != nil {
		t.Fatalf("NewBasket(42): %v", err)
	}
	if b.CustomerID() != 42 || len(b.Items()) != 0 {
		t.Errorf("NewBasket(42) = customer %d with %d items, want customer 42 and no items", b.CustomerID(), len(b.Items()))
	}
	if _, err := NewBasket(0); !errors.Is(err, ErrInvalid) {
		t.Errorf("NewBasket(0) error = %v, want ErrInvalid", err)
	}
}

func TestBasketAdd(t *testing.T) {
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	second := first.Add(48 * time.Hour)
	type add struct {
		productID int64
		quantity  int
		at        time.Time
	}
	tests := []struct {
		name    string
		adds    []add
		want    []BasketItem
		wantErr bool
	}{
		{name: "one product", adds: []add{{7, 2, first}}, want: []BasketItem{{7, 2, first}}},
		{name: "same product again raises the quantity", adds: []add{{7, 2, first}, {7, 1, second}}, want: []BasketItem{{7, 3, second}}},
		{name: "an earlier add keeps the latest time", adds: []add{{7, 1, second}, {7, 1, first}}, want: []BasketItem{{7, 2, second}}},
		{name: "items ordered by product", adds: []add{{9, 1, first}, {3, 1, second}}, want: []BasketItem{{3, 1, second}, {9, 1, first}}},
		{name: "no product", adds: []add{{0, 1, first}}, wantErr: true},
		{name: "zero quantity", adds: []add{{7, 0, first}}, wantErr: true},
		{name: "negative quantity", adds: []add{{7, -1, first}}, wantErr: true},
		{name: "no time", adds: []add{{7, 1, time.Time{}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewBasket(42)
			if err != nil {
				t.Fatalf("NewBasket(): %v", err)
			}
			for _, a := range tt.adds {
				if err = b.Add(a.productID, a.quantity, a.at); err != nil {
					break
				}
			}
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Add() error = %v, want ErrInvalid", err)
				}
				if items := b.Items(); len(items) != 0 {
					t.Errorf("a rejected Add changed the basket: %v", items)
				}
				return
			}
			if err != nil {
				t.Fatalf("Add(): %v", err)
			}
			if got := b.Items(); !slices.Equal(got, tt.want) {
				t.Errorf("Items() = %v, want %v", got, tt.want)
			}
		})
	}
}
