package assistant

import (
	"crypto/rand"
	"sync"
	"time"
)

const (
	conversationTTL  = 2 * time.Hour // since its last turn ended
	maxConversations = 500
)

// store keeps conversations and dispatch requests in memory: a restart forgets them, and one
// API process serves them all. Its mutex guards everything in it, including which
// conversations are busy.
type store struct {
	mu            sync.Mutex
	conversations map[string]*conversation
	approvals     map[string]*approval
}

// conversation is one chat. While it is busy, one turn is answering a message in it: that turn
// alone uses its history and turns, and the conversation is not evicted.
type conversation struct {
	id       string
	busy     bool      // guarded by store.mu
	lastUsed time.Time // when its last turn ended; guarded by store.mu
	history  []message
	turns    int
}

// approval is a dispatch request awaiting a person's decision, bound to the campaign as it
// was when the model asked: its ID, its version (updated_at) and the requested time.
type approval struct {
	id             string
	conversationID string
	campaignID     int64
	version        time.Time
	scheduledAt    *time.Time
	created        time.Time
	closed         bool
}

func newStore() *store {
	return &store{conversations: make(map[string]*conversation), approvals: make(map[string]*approval)}
}

// open starts a turn in conversation id, or in a new conversation when id is empty, and marks
// it busy until release: a conversation answers one message at a time and is not evicted
// meanwhile. A new conversation takes the place of the idle one unused the longest when the
// store is full, and is refused when every conversation is busy.
func (s *store) open(id string, now time.Time) (*conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evict(now)
	c, ok := s.conversations[id]
	switch {
	case id == "":
		if len(s.conversations) >= maxConversations {
			oldest := s.oldestIdle()
			if oldest == nil {
				return nil, ErrTooManyConversations
			}
			s.drop(oldest.id)
		}
		c = &conversation{id: rand.Text(), lastUsed: now}
		s.conversations[c.id] = c
	case !ok:
		return nil, ErrConversationNotFound
	case c.busy:
		return nil, ErrConversationBusy
	}
	c.busy = true
	return c, nil
}

// release ends the turn open started: the conversation is idle again, and its time to live
// starts over.
func (s *store) release(c *conversation, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.busy = false
	c.lastUsed = now
}

// addApproval records a dispatch request and returns its ID. An earlier open request of the
// same conversation for the same campaign is closed: only the latest can be approved.
func (s *store) addApproval(a approval) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, other := range s.approvals {
		if other.conversationID == a.conversationID && other.campaignID == a.campaignID {
			other.closed = true
		}
	}
	a.id = rand.Text()
	s.approvals[a.id] = &a
	return a.id
}

// closeApproval closes an open dispatch request and returns it, so it is decided once.
func (s *store) closeApproval(id string, now time.Time) (approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evict(now)
	a, ok := s.approvals[id]
	if !ok {
		return approval{}, ErrApprovalNotFound
	}
	if a.closed {
		return approval{}, ErrApprovalClosed
	}
	a.closed = true
	return *a, nil
}

// evict forgets idle conversations unused for conversationTTL, with their dispatch requests,
// and dispatch requests older than that. A busy conversation stays.
func (s *store) evict(now time.Time) {
	for id, c := range s.conversations {
		if !c.busy && now.Sub(c.lastUsed) > conversationTTL {
			s.drop(id)
		}
	}
	for id, a := range s.approvals {
		if now.Sub(a.created) > conversationTTL {
			delete(s.approvals, id)
		}
	}
}

func (s *store) drop(conversationID string) {
	delete(s.conversations, conversationID)
	for id, a := range s.approvals {
		if a.conversationID == conversationID {
			delete(s.approvals, id)
		}
	}
}

// oldestIdle returns the idle conversation unused the longest, or nil when all are busy.
func (s *store) oldestIdle() *conversation {
	var oldest *conversation
	for _, c := range s.conversations {
		if !c.busy && (oldest == nil || c.lastUsed.Before(oldest.lastUsed)) {
			oldest = c
		}
	}
	return oldest
}
