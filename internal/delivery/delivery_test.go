package delivery

import (
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
)

func TestCampaignIsDeliveredEndToEnd(t *testing.T) {
	f := newFixture(t, 40)
	receipts, err := NewQueue(f.sqlDB, discard)
	check(t, err)
	providers := map[customer.Channel]provider{}
	for _, ch := range channels() {
		mock := newMockProvider(ch, f.db, receipts.client, now)
		mock.latency, mock.receiptDelay = 5*time.Millisecond, 100*time.Millisecond
		providers[ch] = mock
	}
	f.startWorker(t, providers)

	id := f.dispatch(t)

	waitFor(t, 30*time.Second, "the campaign to be sent", func() bool { return f.status(t, id) == campaign.StatusSent })
	waitFor(t, 30*time.Second, "every receipt", func() bool {
		s := f.statuses(t, id)
		return s[statusPending] == 0 && s[statusAccepted] == 0
	})
	s := f.statuses(t, id)
	if total := s[statusDelivered] + s[statusFailed] + s[statusSkipped]; total != 80 {
		t.Errorf("messages = %v, want 80 with an outcome", s)
	}
	if s[statusDelivered] < 70 {
		t.Errorf("messages = %v, want most delivered", s)
	}
	accepted := f.count(t, "SELECT count(*) FROM deliveries WHERE campaign_id = ? AND provider_message_id IS NOT NULL", id)
	if mocks := f.count(t, "SELECT count(*) FROM mock_provider_messages WHERE outcome = 'accepted'"); mocks != accepted {
		t.Errorf("the mocks accepted %d messages, the deliveries record %d", mocks, accepted)
	}
}
