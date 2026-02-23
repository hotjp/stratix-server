package metrics

import (
	"sync/atomic"
	"time"
)

type Metrics struct {
	connectionsTotal  int64
	connectionsActive int64
	messagesReceived  int64
	messagesSent      int64
	messagesFailed    int64
	bytesReceived     int64
	bytesSent         int64
	largeMessages     int64
	largeMessageBytes int64
	rateLimitExceeded int64
	startTime         time.Time
}

const LargeMessageThreshold = 1024 * 1024 // 1MB

func NewMetrics() *Metrics {
	return &Metrics{
		startTime: time.Now(),
	}
}

func (m *Metrics) IncConnectionsTotal() {
	atomic.AddInt64(&m.connectionsTotal, 1)
}

func (m *Metrics) AddConnectionsActive(delta int64) {
	atomic.AddInt64(&m.connectionsActive, delta)
}

func (m *Metrics) IncMessagesReceived() {
	atomic.AddInt64(&m.messagesReceived, 1)
}

func (m *Metrics) IncMessagesSent() {
	atomic.AddInt64(&m.messagesSent, 1)
}

func (m *Metrics) IncMessagesFailed() {
	atomic.AddInt64(&m.messagesFailed, 1)
}

func (m *Metrics) AddBytesReceived(delta int64) {
	atomic.AddInt64(&m.bytesReceived, delta)
}

func (m *Metrics) AddBytesSent(delta int64) {
	atomic.AddInt64(&m.bytesSent, delta)
}

func (m *Metrics) IncLargeMessages(size int64) {
	atomic.AddInt64(&m.largeMessages, 1)
	atomic.AddInt64(&m.largeMessageBytes, size)
}

func (m *Metrics) IncRateLimitExceeded() {
	atomic.AddInt64(&m.rateLimitExceeded, 1)
}

func (m *Metrics) GetStats() map[string]int64 {
	return map[string]int64{
		"connectionsTotal":  atomic.LoadInt64(&m.connectionsTotal),
		"connectionsActive": atomic.LoadInt64(&m.connectionsActive),
		"messagesReceived":  atomic.LoadInt64(&m.messagesReceived),
		"messagesSent":      atomic.LoadInt64(&m.messagesSent),
		"messagesFailed":    atomic.LoadInt64(&m.messagesFailed),
		"bytesReceived":     atomic.LoadInt64(&m.bytesReceived),
		"bytesSent":         atomic.LoadInt64(&m.bytesSent),
		"largeMessages":     atomic.LoadInt64(&m.largeMessages),
		"largeMessageBytes": atomic.LoadInt64(&m.largeMessageBytes),
		"rateLimitExceeded": atomic.LoadInt64(&m.rateLimitExceeded),
		"uptimeSeconds":     int64(time.Since(m.startTime).Seconds()),
	}
}
