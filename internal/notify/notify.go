package notify

import (
	"sync"
	"time"
)

// Message represents a sent notification.
type Message struct {
	To     string    `json:"to"`
	Body   string    `json:"body"`
	SentAt time.Time `json:"sent_at"`
}

// Notifier is the interface for sending out-of-band notifications such as SMS.
type Notifier interface {
	Send(to, body string) error
	Messages() []Message
}

// SMSNotifier is an in-memory SMS notifier suitable for development and
// testing.  In production this would be replaced by a real SMS gateway client.
type SMSNotifier struct {
	mu       sync.Mutex
	messages []Message
}

// NewSMSNotifier creates a new SMSNotifier.
func NewSMSNotifier() *SMSNotifier {
	return &SMSNotifier{}
}

// Send records an SMS message.
func (n *SMSNotifier) Send(to, body string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.messages = append(n.messages, Message{
		To:     to,
		Body:   body,
		SentAt: time.Now(),
	})
	return nil
}

// Messages returns a copy of all sent messages.
func (n *SMSNotifier) Messages() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := make([]Message, len(n.messages))
	copy(cp, n.messages)
	return cp
}
