// Package commerce is the Commerce bounded context: what customers buy or intend to buy.
//
// Baskets and orders refer to customers by ID only. Amounts are Money in kuruş (TRY). The
// types here carry no persistence concerns.
package commerce

import "errors"

var (
	// ErrInvalid wraps every validation error returned by this package.
	ErrInvalid = errors.New("invalid")
	// ErrStatusTransition reports an order status change the lifecycle doesn't allow, such as
	// refunding a cancelled order.
	ErrStatusTransition = errors.New("order status change not allowed")
)
