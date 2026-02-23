package buffer

import (
	"container/ring"
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"stratix-server/pkg/conn"
	"github.com/rs/zerolog/log"
)

type Buffer struct {
	ring      *ring.Ring
	file      *os.File
	filePath  string
	size      int
	mu        sync.Mutex
	flushChan chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
}

type BufferEntry struct {
	Message   *conn.Message `json:"message"`
	Timestamp int64         `json:"timestamp"`
	Attempts  int           `json:"attempts"`
}

func NewBuffer(size int, filePath string) (*Buffer, error) {
	r := ring.New(size)

	for i := 0; i < size; i++ {
		r.Value = (*BufferEntry)(nil)
		r = r.Next()
	}

	var file *os.File
	if filePath != "" {
		var err error
		file, err = os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Error().Err(err).Str("path", filePath).Msg("Failed to open buffer file")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	b := &Buffer{
		ring:      r,
		file:      file,
		filePath:  filePath,
		size:      size,
		flushChan: make(chan struct{}, 1),
		ctx:       ctx,
		cancel:    cancel,
	}

	go b.flushLoop()

	return b, nil
}

func (b *Buffer) Put(msg *conn.Message) {
	entry := &BufferEntry{
		Message:   msg,
		Timestamp: time.Now().UnixNano(),
		Attempts:  0,
	}

	b.mu.Lock()
	b.ring.Value = entry
	b.ring = b.ring.Next()
	b.mu.Unlock()

	// Trigger flush
	select {
	case b.flushChan <- struct{}{}:
	default:
	}
}

func (b *Buffer) GetAll() []*BufferEntry {
	entries := make([]*BufferEntry, 0, b.size)

	start := b.ring
	for i := 0; i < b.size; i++ {
		if start.Value != nil {
			if entry, ok := start.Value.(*BufferEntry); ok && entry != nil {
				entries = append(entries, entry)
			}
		}
		start = start.Next()
	}

	return entries
}

func (b *Buffer) getAllLocked() []*BufferEntry {
	entries := make([]*BufferEntry, 0, b.size)

	start := b.ring
	for i := 0; i < b.size; i++ {
		if start.Value != nil {
			if entry, ok := start.Value.(*BufferEntry); ok && entry != nil {
				entries = append(entries, entry)
			}
		}
		start = start.Next()
	}

	return entries
}

func (b *Buffer) flushLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.flushChan:
			b.flush()
		case <-ticker.C:
			b.flush()
		case <-b.ctx.Done():
			return
		}
	}
}

func (b *Buffer) flush() {
	if b.file == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	entries := b.getAllLocked()
	if len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			log.Error().Err(err).Msg("Failed to marshal buffer entry")
			continue
		}

		if _, err := b.file.Write(append(data, '\n')); err != nil {
			log.Error().Err(err).Msg("Failed to write to buffer file")
			continue
		}
	}

	b.file.Sync()
}

func (b *Buffer) Close() error {
	b.cancel()
	b.flush()
	if b.file != nil {
		return b.file.Close()
	}
	return nil
}
