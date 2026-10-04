package publicendpoint

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"

	"github.com/KanterLabs/helm/internal/store"
)

// Test email: a one-time address for one webhook, valid for 15 minutes.
// Mail sent to it from any mailbox travels the real path (the sender's
// provider → Cloudflare MX → Email Routing rule → Worker → public URL →
// Helm) and files a low-priority test ticket for that webhook. The
// webhook's own address, which Helm keeps only as a hash, is never needed.

const (
	emailTestLifetime = 15 * time.Minute
	maxEmailTests     = 50
)

// EmailTestDedupeKey keeps repeated email tests on one open ticket.
const EmailTestDedupeKey = "helm-email-test"

// Email test statuses.
const (
	EmailTestWaiting  = "waiting"
	EmailTestReceived = "received"
	EmailTestExpired  = "expired"
)

// EmailTest is a pending or finished test email.
type EmailTest struct {
	ID          string `json:"id"`
	WebhookID   string `json:"webhook_id"`
	Address     string `json:"address"`
	Subject     string `json:"subject"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	ReceivedAt  string `json:"received_at,omitempty"`
	Seconds     int    `json:"seconds,omitempty"`
	Sender      string `json:"sender,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	TicketKey   string `json:"ticket_key,omitempty"`
	TicketURL   string `json:"ticket_url,omitempty"`
	tag         string
	actorID     string
	created     time.Time
	expires     time.Time
}

type emailTests struct {
	mu    sync.Mutex
	items map[string]*EmailTest
}

func emailTestTag() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	buf := make([]byte, store.EmailTagLength)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable")
	}
	for index, value := range buf {
		buf[index] = alphabet[int(value)%len(alphabet)]
	}
	return string(buf)
}

func (t *EmailTest) snapshot(now time.Time) EmailTest {
	copied := *t
	if copied.Status == EmailTestWaiting && now.After(t.expires) {
		copied.Status = EmailTestExpired
	}
	return copied
}

// StartEmailTest creates a test address for a webhook of the active intake.
func (m *Manager) StartEmailTest(actorID, webhookID string, intake store.EmailIntake) EmailTest {
	now := time.Now()
	tag := emailTestTag()
	test := &EmailTest{
		ID: randomID(), WebhookID: webhookID, Address: intake.WebhookAddress(tag),
		Subject: "Helm email test " + strings.ToUpper(randomID()[:6]), Status: EmailTestWaiting,
		CreatedAt: now.UTC().Format(time.RFC3339), ExpiresAt: now.Add(emailTestLifetime).UTC().Format(time.RFC3339),
		tag: tag, actorID: actorID, created: now, expires: now.Add(emailTestLifetime),
	}
	m.tests.mu.Lock()
	defer m.tests.mu.Unlock()
	if m.tests.items == nil {
		m.tests.items = map[string]*EmailTest{}
	}
	for id, item := range m.tests.items {
		if now.Sub(item.expires) > time.Hour || len(m.tests.items) >= maxEmailTests {
			delete(m.tests.items, id)
		}
	}
	m.tests.items[test.ID] = test
	return test.snapshot(now)
}

// EmailTestStatus reports a test started by actorID for webhookID.
func (m *Manager) EmailTestStatus(actorID, webhookID, id string) (EmailTest, bool) {
	m.tests.mu.Lock()
	defer m.tests.mu.Unlock()
	test, ok := m.tests.items[id]
	if !ok || test.actorID != actorID || test.WebhookID != webhookID {
		return EmailTest{}, false
	}
	return test.snapshot(time.Now()), true
}

// MatchEmailTest finds the unexpired test owning an address tag.
func (m *Manager) MatchEmailTest(tag string) (testID, webhookID string, ok bool) {
	if m == nil {
		return "", "", false
	}
	m.tests.mu.Lock()
	defer m.tests.mu.Unlock()
	now := time.Now()
	for _, test := range m.tests.items {
		if test.tag == strings.ToLower(tag) && now.Before(test.expires) {
			return test.ID, test.WebhookID, true
		}
	}
	return "", "", false
}

// RecordEmailTest marks a test received; later arrivals keep the first.
func (m *Manager) RecordEmailTest(id, sender, disposition, ticketKey, ticketURL string) {
	m.tests.mu.Lock()
	defer m.tests.mu.Unlock()
	test, ok := m.tests.items[id]
	if !ok || test.Status == EmailTestReceived {
		return
	}
	now := time.Now()
	test.Status, test.ReceivedAt, test.Seconds = EmailTestReceived, now.UTC().Format(time.RFC3339), int(now.Sub(test.created).Seconds())
	test.Sender, test.Disposition, test.TicketKey, test.TicketURL = sender, disposition, ticketKey, ticketURL
}
