package segmentation

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/commerce"
)

// productsInCategories selects the IDs of the products in the categories its ? names.
const productsInCategories = "(SELECT id FROM products WHERE category_id IN (SELECT id FROM categories WHERE name IN ?))"

// expr is compiled SQL: text with ? placeholders and the arguments that fill them, in order.
// Values from a segment only ever travel as arguments, never as SQL text.
type expr struct {
	sql  string
	args []any
}

// compileSegment returns the condition a customer c must meet to be in the segment. Windows
// such as "the last 30 days" end at asOf; conditions without a window read all current data.
// An empty segment matches every customer. The segment must be valid.
func compileSegment(s campaign.Segment, asOf time.Time) (expr, error) {
	if len(s.Conditions) == 0 {
		return expr{sql: "TRUE"}, nil
	}
	return compiler{asOf: asOf}.group(s.Match, s.Conditions)
}

type compiler struct {
	asOf time.Time
}

// group joins conditions with AND (match all) or OR (match any), in parentheses.
func (c compiler) group(m campaign.Match, conditions []campaign.Condition) (expr, error) {
	op := " AND "
	if m == campaign.MatchAny {
		op = " OR "
	}
	parts := make([]string, len(conditions))
	var args []any
	for i, cond := range conditions {
		e, err := c.condition(cond)
		if err != nil {
			return expr{}, err
		}
		parts[i] = e.sql
		args = append(args, e.args...)
	}
	return expr{"(" + strings.Join(parts, op) + ")", args}, nil
}

// condition compiles one condition. Each type has a fixed SQL shape, and the segment supplies
// only its arguments.
func (c compiler) condition(cond campaign.Condition) (expr, error) {
	var e expr
	switch cond.Type {
	case campaign.ConditionCountry:
		e = expr{"c.city_id IN (SELECT id FROM cities WHERE country_code IN ?)", []any{cond.In}}
	case campaign.ConditionCity:
		e = expr{"c.city_id IN (SELECT id FROM cities WHERE name IN ?)", []any{cond.In}}
	case campaign.ConditionLanguage:
		e = expr{"c.preferred_language IN ?", []any{cond.In}}
	case campaign.ConditionSignedUp:
		e = c.within("c.created_at", cond.WithinDays)
	case campaign.ConditionBasket:
		// One basket line must meet both the window and the categories.
		line := and(expr{sql: "b.customer_id = c.id"}, c.within("b.added_at", cond.WithinDays))
		if cond.Categories != nil {
			line = and(line, expr{"b.product_id IN " + productsInCategories, []any{cond.Categories}})
		}
		e = expr{"EXISTS (SELECT 1 FROM basket_items b WHERE " + line.sql + ")", line.args}
	case campaign.ConditionPurchased:
		// One completed order must meet both the window and the categories.
		order := c.completedOrders(cond.WithinDays)
		if cond.Categories != nil {
			order = and(order, expr{"EXISTS (SELECT 1 FROM order_items i WHERE i.order_id = o.id AND " +
				"i.product_id IN " + productsInCategories + ")", []any{cond.Categories}})
		}
		e = expr{"EXISTS (SELECT 1 FROM orders o WHERE " + order.sql + ")", order.args}
	case campaign.ConditionOrderCount:
		orders := c.completedOrders(cond.WithinDays)
		e = between(expr{"(SELECT count(*) FROM orders o WHERE " + orders.sql + ")", orders.args}, cond.Min, cond.Max)
	case campaign.ConditionTotalSpent:
		// The sum is in kuruş, like the prices paid and the bounds.
		orders := c.completedOrders(cond.WithinDays)
		e = between(expr{"(SELECT COALESCE(sum(i.quantity * i.unit_price_minor), 0) FROM orders o " +
			"JOIN order_items i ON i.order_id = o.id WHERE " + orders.sql + ")", orders.args}, cond.Min, cond.Max)
	case campaign.ConditionGroup:
		var err error
		if e, err = c.group(cond.Match, cond.Conditions); err != nil {
			return expr{}, err
		}
	default:
		return expr{}, fmt.Errorf("compile segment: unsupported condition type %q", cond.Type)
	}
	if cond.Negate {
		e.sql = "NOT (" + e.sql + ")" // safe: every condition is TRUE or FALSE, never NULL
	}
	return e, nil
}

// completedOrders matches customer c's completed orders o, within the window if there is one.
// Cancelled and refunded orders never count.
func (c compiler) completedOrders(days *int) expr {
	return and(expr{"o.customer_id = c.id AND o.status = ?", []any{string(commerce.StatusCompleted)}},
		c.within("o.ordered_at", days))
}

// within matches a column in the days before asOf, and anything when there is no window.
func (c compiler) within(column string, days *int) expr {
	if days == nil {
		return expr{}
	}
	return expr{column + " BETWEEN ? AND ?", []any{c.asOf.Add(-time.Duration(*days) * 24 * time.Hour), c.asOf}}
}

// and joins two conditions with AND, leaving out an empty one.
func and(a, b expr) expr {
	switch {
	case b.sql == "":
		return a
	case a.sql == "":
		return b
	}
	return expr{a.sql + " AND " + b.sql, slices.Concat(a.args, b.args)}
}

// between compares a value with inclusive bounds, at least one of which is set.
func between(value expr, lo, hi *int) expr {
	switch {
	case lo != nil && hi != nil:
		return expr{value.sql + " BETWEEN ? AND ?", slices.Concat(value.args, []any{*lo, *hi})}
	case lo != nil:
		return expr{value.sql + " >= ?", slices.Concat(value.args, []any{*lo})}
	default:
		return expr{value.sql + " <= ?", slices.Concat(value.args, []any{*hi})}
	}
}
