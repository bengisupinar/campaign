package customer

import "time"

// Channel is a channel marketing messages are sent through.
type Channel string

// Supported channels. The notification_permissions table's CHECK constraint lists the same
// values.
const (
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
)

// Valid reports whether ch is a supported channel.
func (ch Channel) Valid() bool { return ch == ChannelEmail || ch == ChannelSMS }

// Permission records whether a customer allows marketing messages on a channel, and since
// when.
type Permission struct {
	Channel   Channel
	Granted   bool
	UpdatedAt time.Time
}
