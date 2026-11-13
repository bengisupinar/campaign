package commerce

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// placed is when the test orders were placed.
var placed = time.Date(2026, 9, 15, 18, 30, 0, 0, time.UTC)

func TestNewOrder(t *testing.T) {
	tests := []struct {
		name       string
		customerID int64
		orderedAt  time.Time
		lines      []OrderLine
		wantTotal  Money
		wantErr    bool
	}{
		{name: "one line", customerID: 42, orderedAt: placed, lines: []OrderLine{{1, 2, 149900}}, wantTotal: 299800},
		{name: "several lines", customerID: 42, orderedAt: placed, lines: []OrderLine{{1, 1, 149900}, {2, 3, 89900}}, wantTotal: 419600},
		{name: "free line", customerID: 42, orderedAt: placed, lines: []OrderLine{{1, 1, 0}}, wantTotal: 0},
		{name: "no customer", orderedAt: placed, lines: []OrderLine{{1, 1, 100}}, wantErr: true},
		{name: "no order time", customerID: 42, lines: []OrderLine{{1, 1, 100}}, wantErr: true},
		{name: "no lines", customerID: 42, orderedAt: placed, wantErr: true},
		{name: "no product", customerID: 42, orderedAt: placed, lines: []OrderLine{{0, 1, 100}}, wantErr: true},
		{name: "zero quantity", customerID: 42, orderedAt: placed, lines: []OrderLine{{1, 0, 100}}, wantErr: true},
		{name: "negative unit price", customerID: 42, orderedAt: placed, lines: []OrderLine{{1, 1, -1}}, wantErr: true},
		{name: "product in two lines", customerID: 42, orderedAt: placed, lines: []OrderLine{{1, 1, 100}, {1, 2, 100}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := NewOrder(tt.customerID, tt.orderedAt, tt.lines)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("NewOrder() error = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewOrder(): %v", err)
			}
			if o.Status() != StatusCompleted || !o.StatusChangedAt().Equal(tt.orderedAt) {
				t.Errorf("new order is %s since %v, want completed since %v", o.Status(), o.StatusChangedAt(), tt.orderedAt)
			}
			if o.CustomerID() != tt.customerID || !o.OrderedAt().Equal(tt.orderedAt) || o.ID() != 0 {
				t.Errorf("order = customer %d at %v with ID %d, want customer %d at %v with ID 0",
					o.CustomerID(), o.OrderedAt(), o.ID(), tt.customerID, tt.orderedAt)
			}
			if got := o.Lines(); !slices.Equal(got, tt.lines) {
				t.Errorf("Lines() = %v, want %v", got, tt.lines)
			}
			if got := o.Total(); got != tt.wantTotal {
				t.Errorf("Total() = %d, want %d", got, tt.wantTotal)
			}
		})
	}
}

func TestOrderStatusChanges(t *testing.T) {
	later := placed.Add(72 * time.Hour)
	cancel, refund := (*Order).Cancel, (*Order).Refund
	tests := []struct {
		name       string
		before     func(*Order, time.Time) error // an earlier change, if any
		change     func(*Order, time.Time) error
		at         time.Time
		wantStatus OrderStatus
		wantErr    error
	}{
		{name: "cancel a completed order", change: cancel, at: later, wantStatus: StatusCancelled},
		{name: "refund a completed order", change: refund, at: later, wantStatus: StatusRefunded},
		{name: "change at the order time", change: refund, at: placed, wantStatus: StatusRefunded},
		{name: "refund a cancelled order", before: cancel, change: refund, at: later, wantStatus: StatusCancelled, wantErr: ErrStatusTransition},
		{name: "cancel a cancelled order", before: cancel, change: cancel, at: later, wantStatus: StatusCancelled, wantErr: ErrStatusTransition},
		{name: "cancel a refunded order", before: refund, change: cancel, at: later, wantStatus: StatusRefunded, wantErr: ErrStatusTransition},
		{name: "refund a refunded order", before: refund, change: refund, at: later, wantStatus: StatusRefunded, wantErr: ErrStatusTransition},
		{name: "change before the order", change: cancel, at: placed.Add(-time.Minute), wantStatus: StatusCompleted, wantErr: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := NewOrder(42, placed, []OrderLine{{1, 1, 100}})
			if err != nil {
				t.Fatalf("NewOrder(): %v", err)
			}
			if tt.before != nil {
				if err := tt.before(o, placed.Add(time.Hour)); err != nil {
					t.Fatalf("earlier status change: %v", err)
				}
			}
			wantChangedAt := o.StatusChangedAt() // unchanged when the change is rejected

			err = tt.change(o, tt.at)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("status change error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil {
				wantChangedAt = tt.at
			}
			if o.Status() != tt.wantStatus || !o.StatusChangedAt().Equal(wantChangedAt) {
				t.Errorf("order is %s since %v, want %s since %v", o.Status(), o.StatusChangedAt(), tt.wantStatus, wantChangedAt)
			}
		})
	}
}

func TestOrderLinesCannotChangeFromOutside(t *testing.T) {
	lines := []OrderLine{{1, 1, 100}}
	o, err := NewOrder(42, placed, lines)
	if err != nil {
		t.Fatalf("NewOrder(): %v", err)
	}

	lines[0].Quantity = 99
	o.Lines()[0].Quantity = 50

	if got := o.Lines()[0].Quantity; got != 1 {
		t.Errorf("quantity = %d after changing the caller's slices, want 1", got)
	}
	if got := o.Total(); got != 100 {
		t.Errorf("Total() = %d, want 100", got)
	}
}
