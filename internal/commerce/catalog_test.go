package commerce

import (
	"errors"
	"testing"
)

func TestNewProduct(t *testing.T) {
	tests := []struct {
		name       string
		categoryID int64
		in         string
		price      Money
		wantName   string // empty when the product is invalid
	}{
		{name: "valid", categoryID: 1, in: "Wireless Headphones", price: 149900, wantName: "Wireless Headphones"},
		{name: "name trimmed", categoryID: 1, in: "  Keyboard ", price: 89900, wantName: "Keyboard"},
		{name: "empty name", categoryID: 1, in: " ", price: 100},
		{name: "zero price", categoryID: 1, in: "Sticker", price: 0},
		{name: "negative price", categoryID: 1, in: "Sticker", price: -100},
		{name: "no category", in: "Mouse", price: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewProduct(tt.categoryID, tt.in, tt.price)
			if tt.wantName == "" {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("NewProduct() error = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewProduct(): %v", err)
			}
			want := Product{CategoryID: tt.categoryID, Name: tt.wantName, Price: tt.price}
			if got != want {
				t.Errorf("NewProduct() = %+v, want %+v", got, want)
			}
		})
	}
}
