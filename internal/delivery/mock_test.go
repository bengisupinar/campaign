package delivery

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/customer"
)

func TestMockBehaviourDependsOnlyOnTheKey(t *testing.T) {
	shares := make(map[behaviour]int)
	for i := range 10000 {
		key := fmt.Sprintf("delivery-%d", i)
		b := behaviourOf(key)
		if behaviourOf(key) != b {
			t.Fatalf("behaviourOf(%q) changed between calls", key)
		}
		shares[b]++
	}
	// About 1 % rejected and slow, 2 % busy and undelivered, the rest delivered.
	for b, want := range map[behaviour]int{rejects: 100, slowOnce: 100, busyOnce: 200, undelivered: 200, delivers: 9400} {
		if got := shares[b]; got < want/2 || got > want*3/2 {
			t.Errorf("behaviour %d: %d of 10,000 keys, want about %d", b, got, want)
		}
	}
}

// keyFor finds an idempotency key with the given behaviour.
func keyFor(t *testing.T, b behaviour) string {
	t.Helper()
	for i := range 10000 {
		if key := fmt.Sprintf("delivery-%d", i); behaviourOf(key) == b {
			return key
		}
	}
	t.Fatalf("no key behaves like %d", b)
	return ""
}

// newTestMock returns an instant SMS mock provider on f's database.
func newTestMock(t *testing.T, f *fixture) *mockProvider {
	t.Helper()
	receipts, err := NewQueue(f.sqlDB, discard)
	check(t, err)
	p := newMockProvider(customer.ChannelSMS, f.db, receipts.client, now)
	p.latency = 0
	return p
}

// answer is one mock call's result, comparable across runs.
type answer struct {
	ID  string
	Err string
}

func call(p *mockProvider, key string, deadline time.Duration) answer {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	id, err := p.Send(ctx, message{IdempotencyKey: key, Channel: customer.ChannelSMS, To: "+905320000000", Body: "Merhaba!"})
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return answer{Err: "timeout"}
	case err != nil:
		return answer{Err: err.Error()}
	}
	return answer{ID: id}
}

func TestMockAnswersEachKeyOnceAcrossRestarts(t *testing.T) {
	f := newFixture(t, 0)
	key := keyFor(t, delivers)

	first := call(newTestMock(t, f), key, time.Second)
	again := call(newTestMock(t, f), key, time.Second) // a new instance: the provider restarted

	if first.ID == "" || again != first {
		t.Errorf("answers %+v then %+v, want the same acceptance", first, again)
	}
	var attempts int
	check(t, f.db.Raw("SELECT attempts FROM mock_provider_messages WHERE idempotency_key = ?", key).Scan(&attempts).Error)
	if attempts != 2 {
		t.Errorf("the mock counted %d attempts, want 2", attempts)
	}
	if n := f.count(t, "SELECT count(*) FROM river_job WHERE kind = 'delivery_receipt' AND args->>'provider_message_id' = ?", first.ID); n != 1 {
		t.Errorf("%d receipts scheduled, want 1", n)
	}
}

func TestMockScenariosAreReproducible(t *testing.T) {
	scenario := func() map[string][]answer {
		f := newFixture(t, 0)
		p := newTestMock(t, f)
		got := make(map[string][]answer)
		for name, b := range map[string]behaviour{
			"rejects": rejects, "busy once": busyOnce, "slow once": slowOnce, "undelivered": undelivered, "delivers": delivers,
		} {
			key := keyFor(t, b)
			got[name] = []answer{call(p, key, 200*time.Millisecond), call(p, key, 200*time.Millisecond)}
			var receipts []string
			check(t, f.db.Raw("SELECT args->>'delivered' FROM river_job WHERE kind = 'delivery_receipt' AND args->>'provider_message_id' = ?",
				"mock-"+key).Scan(&receipts).Error)
			for _, r := range receipts {
				got[name] = append(got[name], answer{ID: "receipt delivered=" + r})
			}
		}
		return got
	}

	first := scenario()
	want := map[string][]answer{
		"rejects":     {{Err: errRejected.Error()}, {Err: errRejected.Error()}},
		"busy once":   {{Err: errBusy.Error()}, {ID: "mock-" + keyFor(t, busyOnce)}, {ID: "receipt delivered=true"}},
		"slow once":   {{Err: "timeout"}, {ID: "mock-" + keyFor(t, slowOnce)}, {ID: "receipt delivered=true"}},
		"undelivered": {{ID: "mock-" + keyFor(t, undelivered)}, {ID: "mock-" + keyFor(t, undelivered)}, {ID: "receipt delivered=false"}},
		"delivers":    {{ID: "mock-" + keyFor(t, delivers)}, {ID: "mock-" + keyFor(t, delivers)}, {ID: "receipt delivered=true"}},
	}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("answers = %+v\nwant %+v", first, want)
	}
	if second := scenario(); !reflect.DeepEqual(second, first) {
		t.Errorf("a second run answered %+v, want the same as the first", second)
	}
}
