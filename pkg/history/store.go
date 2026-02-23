package history

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type StoredMessage struct {
	MessageID   string `json:"messageId"`
	ClientID    string `json:"clientId"`
	Data        []byte `json:"data"`
	Timestamp   int64  `json:"timestamp"`
	ContentType string `json:"contentType,omitempty"`
	Size        int64  `json:"size"`
}

type ClientHistory struct {
	Messages []*StoredMessage
	OldestID string
	NewestID string
}

type Store struct {
	messages     map[string]*ClientHistory
	maxPerClient int
	maxMsgSize   int64
	mu           sync.RWMutex
}

func NewStore(maxPerClient int, maxMsgSize int64) *Store {
	if maxPerClient <= 0 {
		maxPerClient = 500
	}
	if maxMsgSize <= 0 {
		maxMsgSize = 64 * 1024 // 64KB
	}
	return &Store{
		messages:     make(map[string]*ClientHistory),
		maxPerClient: maxPerClient,
		maxMsgSize:   maxMsgSize,
	}
}

func (s *Store) Add(clientID, messageID string, data []byte, contentType string) {
	size := int64(len(data))
	if size > s.maxMsgSize {
		log.Debug().Str("clientId", clientID).Int64("size", size).Msg("Message too large, storing metadata only")
		data = nil
	}

	msg := &StoredMessage{
		MessageID:   messageID,
		ClientID:    clientID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		ContentType: contentType,
		Size:        size,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	history, exists := s.messages[clientID]
	if !exists {
		history = &ClientHistory{
			Messages: make([]*StoredMessage, 0, s.maxPerClient),
		}
		s.messages[clientID] = history
	}

	if len(history.Messages) >= s.maxPerClient {
		copy(history.Messages, history.Messages[1:])
		history.Messages = history.Messages[:s.maxPerClient-1]
	}

	history.Messages = append(history.Messages, msg)
	history.NewestID = messageID
	if len(history.Messages) > 0 {
		history.OldestID = history.Messages[0].MessageID
	}
}

func (s *Store) Get(clientID string, lastMessageID string, limit int) []*StoredMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history, exists := s.messages[clientID]
	if !exists || len(history.Messages) == 0 {
		return nil
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > s.maxPerClient {
		limit = s.maxPerClient
	}

	if lastMessageID == "" {
		start := 0
		if len(history.Messages) > limit {
			start = len(history.Messages) - limit
		}
		result := make([]*StoredMessage, len(history.Messages)-start)
		copy(result, history.Messages[start:])
		return result
	}

	startIdx := -1
	for i, msg := range history.Messages {
		if msg.MessageID == lastMessageID {
			startIdx = i + 1
			break
		}
	}

	if startIdx == -1 || startIdx >= len(history.Messages) {
		return nil
	}

	endIdx := startIdx + limit
	if endIdx > len(history.Messages) {
		endIdx = len(history.Messages)
	}

	result := make([]*StoredMessage, endIdx-startIdx)
	copy(result, history.Messages[startIdx:endIdx])
	return result
}

func (s *Store) GetStats() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total int64
	for _, h := range s.messages {
		total += int64(len(h.Messages))
	}

	return map[string]int64{
		"totalClients":  int64(len(s.messages)),
		"totalMessages": total,
		"maxPerClient":  int64(s.maxPerClient),
	}
}

func (s *Store) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	threshold := time.Now().Add(-maxAge).UnixNano()
	for clientID, history := range s.messages {
		validStart := 0
		for i, msg := range history.Messages {
			if msg.Timestamp >= threshold {
				validStart = i
				break
			}
		}
		if validStart > 0 {
			history.Messages = history.Messages[validStart:]
			if len(history.Messages) > 0 {
				history.OldestID = history.Messages[0].MessageID
			}
		}
		if len(history.Messages) == 0 {
			delete(s.messages, clientID)
		}
	}
}
