package commerce

import (
	"fmt"
	"slices"
	"time"
)

// OrderStatus is where an order is in its lifecycle. The orders table's CHECK constraint
// lists the same values.
type OrderStatus string

// Order statuses. A new order is completed; it can then be cancelled or refunded, and both
// are final.
const (
	// StatusCompleted marks a purchase. Only completed orders count towards purchase history
	// and spend.
	StatusCompleted OrderStatus = "completed"
	// StatusCancelled marks an order cancelled after it was placed.
	StatusCancelled OrderStatus = "cancelled"
	// StatusRefunded marks an order refunded in full; partial refunds are not supported.
	StatusRefunded OrderStatus = "refunded"
)

// OrderLine is a product in an order, with the unit price paid for it.
type OrderLine struct {
	ProductID int64
	Quantity  int
	UnitPrice Money
}

// Order is the aggregate root of a customer's purchase. Its lines never change; only its
// status does.
type Order struct {
	id              int64
	customerID      int64
	orderedAt       time.Time
	lines           []OrderLine
	status          OrderStatus
	statusChangedAt time.Time
}

// NewOrder returns a completed order a customer placed at orderedAt. It needs at least one
// line, one line per product, quantities above zero and unit prices of zero or more.
func NewOrder(customerID int64, orderedAt time.Time, lines []OrderLine) (*Order, error) {
	switch {
	case customerID <= 0:
		return nil, fmt.Errorf("%w customer ID %d", ErrInvalid, customerID)
	case orderedAt.IsZero():
		return nil, fmt.Errorf("%w order time: missing", ErrInvalid)
	case len(lines) == 0:
		return nil, fmt.Errorf("%w order: no lines", ErrInvalid)
	}
	seen := make(map[int64]bool, len(lines))
	for _, l := range lines {
		switch {
		case l.ProductID <= 0:
			return nil, fmt.Errorf("%w product ID %d", ErrInvalid, l.ProductID)
		case l.Quantity <= 0:
			return nil, fmt.Errorf("%w quantity %d: must be above zero", ErrInvalid, l.Quantity)
		case l.UnitPrice < 0:
			return nil, fmt.Errorf("%w unit price %d: must not be negative", ErrInvalid, l.UnitPrice)
		case seen[l.ProductID]:
			return nil, fmt.Errorf("%w order: product %d is in more than one line", ErrInvalid, l.ProductID)
		}
		seen[l.ProductID] = true
	}
	return &Order{
		customerID:      customerID,
		orderedAt:       orderedAt,
		lines:           slices.Clone(lines),
		status:          StatusCompleted,
		statusChangedAt: orderedAt,
	}, nil
}

// ID returns the order's ID, which the database assigns when the order is first saved.
func (o *Order) ID() int64 { return o.id }

// CustomerID returns the ID of the customer who placed the order.
func (o *Order) CustomerID() int64 { return o.customerID }

// OrderedAt returns when the order was placed.
func (o *Order) OrderedAt() time.Time { return o.orderedAt }

// Lines returns a copy of the order's lines.
func (o *Order) Lines() []OrderLine { return slices.Clone(o.lines) }

// Status returns the order's current status.
func (o *Order) Status() OrderStatus { return o.status }

// StatusChangedAt returns when the current status began: the order time for a completed
// order.
func (o *Order) StatusChangedAt() time.Time { return o.statusChangedAt }

// Total returns the sum of quantity × unit price over the order's lines.
func (o *Order) Total() Money {
	var total Money
	for _, l := range o.lines {
		total += Money(l.Quantity) * l.UnitPrice
	}
	return total
}

// Cancel marks a completed order as cancelled at time at.
func (o *Order) Cancel(at time.Time) error { return o.changeStatus(StatusCancelled, at) }

// Refund marks a completed order as refunded in full at time at.
func (o *Order) Refund(at time.Time) error { return o.changeStatus(StatusRefunded, at) }

// changeStatus moves a completed order to a final status. Cancelled and refunded orders never
// change again, and no change can predate the order.
func (o *Order) changeStatus(to OrderStatus, at time.Time) error {
	if o.status != StatusCompleted {
		return fmt.Errorf("%w: a %s order cannot become %s", ErrStatusTransition, o.status, to)
	}
	if at.Before(o.orderedAt) {
		return fmt.Errorf("%w status change time: before the order was placed", ErrInvalid)
	}
	o.status, o.statusChangedAt = to, at
	return nil
}
