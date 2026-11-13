package commerce

import (
	"fmt"
	"strings"
)

// Category groups products, e.g. "Electronics". Categories are flat reference data.
type Category struct {
	ID   int64
	Name string
}

// Product is an item customers put in baskets and order. Its ID is assigned by the database.
type Product struct {
	ID         int64
	CategoryID int64
	Name       string
	Price      Money
}

// NewProduct returns a product in a category, with a trimmed, non-empty name and a price above
// zero.
func NewProduct(categoryID int64, name string, price Money) (Product, error) {
	name = strings.TrimSpace(name)
	switch {
	case categoryID <= 0:
		return Product{}, fmt.Errorf("%w category ID %d", ErrInvalid, categoryID)
	case name == "":
		return Product{}, fmt.Errorf("%w product name: empty", ErrInvalid)
	case price <= 0:
		return Product{}, fmt.Errorf("%w price %d: must be above zero", ErrInvalid, price)
	}
	return Product{CategoryID: categoryID, Name: name, Price: price}, nil
}
