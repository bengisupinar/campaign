package seed

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/bengisupinar/campaign/internal/commerce"
	"github.com/bengisupinar/campaign/internal/customer"
)

const day = 24 * time.Hour

// persona is a kind of customer with its own shopping behaviour. Personas make sure every
// segment worth demonstrating exists in the data, which uniform randomness doesn't.
type persona int

const (
	personaNew persona = iota
	personaBrowser
	personaOccasional
	personaRegular
	personaLoyal
	personaLapsed
)

// personaSpec describes how a persona shops. Durations count back from now.
type personaSpec struct {
	share                      int // percent of customers
	signedUpMin, signedUpMax   time.Duration
	ordersMin, ordersMax       int
	history                    time.Duration // orders fall within this period; 0 means since signup
	lastOrderMin, lastOrderMax time.Duration // when the last order was placed; a zero maximum means any time
	basketShare                int           // percent of customers with an open basket
}

// personaSpecs returns the specs indexed by persona.
func personaSpecs() []personaSpec {
	const years = 365 * day
	return []personaSpec{
		personaNew:     {share: 8, signedUpMin: day, signedUpMax: 30 * day, ordersMax: 1, basketShare: 50},
		personaBrowser: {share: 17, signedUpMin: 30 * day, signedUpMax: 3 * years, basketShare: 45},
		personaOccasional: {share: 30, signedUpMin: 30 * day, signedUpMax: 3 * years,
			ordersMin: 1, ordersMax: 3, basketShare: 20},
		personaRegular: {share: 20, signedUpMin: 180 * day, signedUpMax: 3 * years,
			ordersMin: 4, ordersMax: 9, history: 540 * day, lastOrderMax: 60 * day, basketShare: 25},
		personaLoyal: {share: 10, signedUpMin: years, signedUpMax: 3 * years,
			ordersMin: 10, ordersMax: 15, history: 2 * years, lastOrderMax: 14 * day, basketShare: 30},
		personaLapsed: {share: 15, signedUpMin: years, signedUpMax: 3 * years,
			ordersMin: 2, ordersMax: 6, lastOrderMin: 180 * day, lastOrderMax: 540 * day, basketShare: 15},
	}
}

// shopper is what activity generation needs to know about a generated customer.
type shopper struct {
	id         int64 // known once the customer is saved
	persona    persona
	signedUp   time.Time
	favourites []int // category indexes
}

// generator makes every random choice from one seeded source, so the same seed and the same
// now always produce the same data. All times are whole seconds in UTC.
type generator struct {
	rng        *rand.Rand
	now        time.Time
	fold       *strings.Replacer
	lineCounts []weighted[int] // products per order
	quantities []weighted[int] // units per line
}

func newGenerator(seed uint64, now time.Time) *generator {
	return &generator{
		rng:        rand.New(rand.NewPCG(seed, seed)), //nolint:gosec // reproducible synthetic data, not a secret
		now:        now.UTC().Truncate(time.Second),
		fold:       newASCIIFolder(),
		lineCounts: []weighted[int]{{1, 45}, {2, 30}, {3, 15}, {4, 10}},
		quantities: []weighted[int]{{1, 80}, {2, 15}, {3, 5}},
	}
}

// customers generates n customers with their contact details, permissions and personas. The
// cities must have IDs.
func (g *generator) customers(n int, ms []market, cityChoices [][]weighted[int], cities []customer.City, categories int,
) ([]*customer.Customer, []shopper, error) {
	specs := personaSpecs()
	personaChoices := make([]weighted[persona], len(specs))
	for p, s := range specs {
		personaChoices[p] = weighted[persona]{value: persona(p), weight: s.share}
	}
	marketChoices := make([]weighted[int], len(ms))
	for i, m := range ms {
		marketChoices[i] = weighted[int]{value: i, weight: m.share}
	}

	customers := make([]*customer.Customer, 0, n)
	shoppers := make([]shopper, 0, n)
	for seq := 1; seq <= n; seq++ {
		mi := pick(g.rng, marketChoices)
		m := ms[mi]
		city := cities[pick(g.rng, cityChoices[mi])]
		language := pick(g.rng, m.languages)
		firsts, lasts := names(language)
		first, last := firsts[g.rng.IntN(len(firsts))], lasts[g.rng.IntN(len(lasts))]

		email, err := g.email(first, last, seq)
		if err != nil {
			return nil, nil, fmt.Errorf("customer %d: %w", seq, err)
		}
		var phone customer.Phone
		if g.chance(m.phoneShare) {
			if phone, err = g.phone(m, seq); err != nil {
				return nil, nil, fmt.Errorf("customer %d: %w", seq, err)
			}
		}

		p := pick(g.rng, personaChoices)
		signedUp := g.ago(specs[p].signedUpMin, specs[p].signedUpMax)
		c, err := customer.New(customer.Profile{
			FirstName: first,
			LastName:  last,
			Email:     email,
			Phone:     phone,
			CityID:    city.ID,
			Language:  language,
		}, signedUp)
		if err != nil {
			return nil, nil, fmt.Errorf("customer %d: %w", seq, err)
		}
		if err := g.permissions(c, signedUp, !phone.IsZero()); err != nil {
			return nil, nil, fmt.Errorf("customer %d: %w", seq, err)
		}

		customers = append(customers, c)
		shoppers = append(shoppers, shopper{
			persona:    p,
			signedUp:   signedUp,
			favourites: g.rng.Perm(categories)[:1+g.rng.IntN(2)],
		})
	}
	return customers, shoppers, nil
}

// permissions records how the customer answered for each channel they can be reached on: 85%
// were asked at signup; of those, 70% allowed email and 55% SMS, and a tenth of the
// allowances were withdrawn later. A customer who was never asked has no permission.
func (g *generator) permissions(c *customer.Customer, signedUp time.Time, hasPhone bool) error {
	channels := []struct {
		channel    customer.Channel
		grantShare int
		reachable  bool
	}{
		{customer.ChannelEmail, 70, true},
		{customer.ChannelSMS, 55, hasPhone},
	}
	for _, ch := range channels {
		if !ch.reachable || !g.chance(85) {
			continue
		}
		if !g.chance(ch.grantShare) {
			if err := c.Revoke(ch.channel, signedUp); err != nil { // declined when asked
				return err
			}
			continue
		}
		if err := c.Grant(ch.channel, signedUp); err != nil {
			return err
		}
		if g.chance(10) {
			if err := c.Revoke(ch.channel, g.between(signedUp, g.now)); err != nil {
				return err
			}
		}
	}
	return nil
}

// activity generates each shopper's orders and, for some, an open basket. The shoppers and
// products must have IDs.
func (g *generator) activity(shoppers []shopper, products []commerce.Product, byCategory [][]int,
) ([]*commerce.Order, []*commerce.Basket, error) {
	specs := personaSpecs()
	var orders []*commerce.Order
	var baskets []*commerce.Basket
	for _, s := range shoppers {
		spec := specs[s.persona]
		var lastOrder time.Time
		for _, at := range g.orderTimes(s, spec) {
			o, err := commerce.NewOrder(s.id, at, g.lines(s, products, byCategory))
			if err != nil {
				return nil, nil, fmt.Errorf("order of customer %d: %w", s.id, err)
			}
			if err := g.outcome(o, at); err != nil {
				return nil, nil, fmt.Errorf("order of customer %d: %w", s.id, err)
			}
			orders = append(orders, o)
			lastOrder = at
		}
		if g.chance(spec.basketShare) {
			b, err := g.basket(s, lastOrder, products, byCategory)
			if err != nil {
				return nil, nil, fmt.Errorf("basket of customer %d: %w", s.id, err)
			}
			baskets = append(baskets, b)
		}
	}
	return orders, baskets, nil
}

// orderTimes returns when the shopper ordered, oldest first. Every order is at least an hour
// after signup and an hour before now, which leaves room for a later basket.
func (g *generator) orderTimes(s shopper, spec personaSpec) []time.Time {
	n := spec.ordersMin + g.rng.IntN(spec.ordersMax-spec.ordersMin+1)
	if n == 0 {
		return nil
	}
	earliest, latest := s.signedUp.Add(time.Hour), g.now.Add(-time.Hour)
	if spec.history > 0 {
		earliest = later(earliest, g.now.Add(-spec.history))
	}
	times := make([]time.Time, 0, n)
	if spec.lastOrderMax > 0 {
		latest = g.between(later(earliest, g.now.Add(-spec.lastOrderMax)), earlier(latest, g.now.Add(-spec.lastOrderMin)))
		times = append(times, latest)
		n--
	}
	for range n {
		times = append(times, g.between(earliest, latest))
	}
	slices.SortFunc(times, time.Time.Compare)
	return times
}

// outcome cancels 6% of orders within three days and refunds 4% within a month, unless that
// would be in the future.
func (g *generator) outcome(o *commerce.Order, at time.Time) error {
	switch r := g.rng.IntN(100); {
	case r < 6:
		if t := at.Add(g.duration(time.Hour, 3*day)); !t.After(g.now) {
			return o.Cancel(t)
		}
	case r < 10:
		if t := at.Add(g.duration(7*day, 30*day)); !t.After(g.now) {
			return o.Refund(t)
		}
	}
	return nil
}

// lines returns 1 to 4 order lines at the products' current prices.
func (g *generator) lines(s shopper, products []commerce.Product, byCategory [][]int) []commerce.OrderLine {
	picks := g.productPicks(s, pick(g.rng, g.lineCounts), products, byCategory)
	lines := make([]commerce.OrderLine, len(picks))
	for i, p := range picks {
		lines[i] = commerce.OrderLine{ProductID: p.ID, Quantity: pick(g.rng, g.quantities), UnitPrice: p.Price}
	}
	return lines
}

// basket opens a basket after the shopper's signup and last order. 70% of baskets were filled
// within the last 30 days, the rest before, when the shopper's history leaves room for it.
func (g *generator) basket(s shopper, lastOrder time.Time, products []commerce.Product, byCategory [][]int,
) (*commerce.Basket, error) {
	start := later(s.signedUp, lastOrder).Add(time.Minute)
	recent := later(start, g.now.Add(-30*day))
	at := g.between(recent, g.now)
	if olderEnd := g.now.Add(-31 * day); !g.chance(70) && start.Before(olderEnd) {
		at = g.between(later(start, g.now.Add(-120*day)), olderEnd)
	}
	b, err := commerce.NewBasket(s.id)
	if err != nil {
		return nil, err
	}
	for _, p := range g.productPicks(s, 1+g.rng.IntN(4), products, byCategory) {
		added := earlier(at.Add(g.duration(0, 2*time.Hour)), g.now)
		if err := b.Add(p.ID, pick(g.rng, g.quantities), added); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// productPicks returns n distinct products, 70% of them from the shopper's favourite
// categories.
func (g *generator) productPicks(s shopper, n int, products []commerce.Product, byCategory [][]int) []commerce.Product {
	picks := make([]commerce.Product, 0, n)
	for len(picks) < n {
		i := g.rng.IntN(len(products))
		if g.chance(70) {
			favourite := byCategory[s.favourites[g.rng.IntN(len(s.favourites))]]
			i = favourite[g.rng.IntN(len(favourite))]
		}
		if !slices.ContainsFunc(picks, func(p commerce.Product) bool { return p.ID == products[i].ID }) {
			picks = append(picks, products[i])
		}
	}
	return picks
}

// chance reports true percent times out of a hundred.
func (g *generator) chance(percent int) bool { return g.rng.IntN(100) < percent }

// duration returns a random whole number of seconds between lo and hi.
func (g *generator) duration(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(g.rng.Int64N(int64((hi-lo)/time.Second)+1))*time.Second
}

// ago returns a random time between lo and hi before now.
func (g *generator) ago(lo, hi time.Duration) time.Time { return g.now.Add(-g.duration(lo, hi)) }

// between returns a random time from from to to, or from when to is not after it.
func (g *generator) between(from, to time.Time) time.Time {
	return from.Add(g.duration(0, to.Sub(from)))
}

// pick returns one of the items, each with a chance proportional to its weight.
func pick[T any](rng *rand.Rand, items []weighted[T]) T {
	total := 0
	for _, it := range items {
		total += it.weight
	}
	r := rng.IntN(total)
	for _, it := range items {
		if r < it.weight {
			return it.value
		}
		r -= it.weight
	}
	return items[len(items)-1].value
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
