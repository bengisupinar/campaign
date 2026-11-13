package commerce

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

// Basket is a customer's open basket. Each customer has one, identified by the customer's ID,
// so a basket has no ID of its own. Whether it is abandoned is derived from when its items
// were added and whether the customer ordered since.
type Basket struct {
	customerID int64
	items      map[int64]BasketItem // keyed by product ID
}

// BasketItem is a product in a basket. AddedAt is when the product was last added.
type BasketItem struct {
	ProductID int64
	Quantity  int
	AddedAt   time.Time
}

// NewBasket returns an empty basket for a customer.
func NewBasket(customerID int64) (*Basket, error) {
	if customerID <= 0 {
		return nil, fmt.Errorf("%w customer ID %d", ErrInvalid, customerID)
	}
	return &Basket{customerID: customerID, items: make(map[int64]BasketItem)}, nil
}

// CustomerID returns the ID of the customer the basket belongs to.
func (b *Basket) CustomerID() int64 { return b.customerID }

// Add puts quantity units of a product in the basket at time at. Adding a product that is
// already in the basket raises its quantity, so each product has one line.
func (b *Basket) Add(productID int64, quantity int, at time.Time) error {
	switch {
	case productID <= 0:
		return fmt.Errorf("%w product ID %d", ErrInvalid, productID)
	case quantity <= 0:
		return fmt.Errorf("%w quantity %d: must be above zero", ErrInvalid, quantity)
	case at.IsZero():
		return fmt.Errorf("%w time: missing", ErrInvalid)
	}
	item := b.items[productID]
	item.ProductID = productID
	item.Quantity += quantity
	if at.After(item.AddedAt) {
		item.AddedAt = at
	}
	b.items[productID] = item
	return nil
}

// Items returns the basket's lines, ordered by product ID.
func (b *Basket) Items() []BasketItem {
	items := make([]BasketItem, 0, len(b.items))
	for _, id := range slices.Sorted(maps.Keys(b.items)) {
		items = append(items, b.items[id])
	}
	return items
}
