package conn

import (
	"encoding/binary"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

var (
	messagePool = sync.Pool{
		New: func() interface{} {
			return &Message{
				Data: make([]byte, 0, 4096),
			}
		},
	}

	writeTaskPool = sync.Pool{
		New: func() interface{} {
			return &writeTask{
				data: make([]byte, 0, 4096),
			}
		},
	}

	byteSlicePool = sync.Pool{
		New: func() interface{} {
			b := make([]byte, 0, 64*1024)
			return &b
		},
	}
)

func AcquireMessage() *Message {
	return messagePool.Get().(*Message)
}

func ReleaseMessage(msg *Message) {
	msg.Type = 0
	msg.ClientID = ""
	msg.MessageID = ""
	msg.Timestamp = 0
	msg.Data = msg.Data[:0]
	msg.Route = ""
	msg.ContentType = ""
	msg.Compressed = false
	messagePool.Put(msg)
}

func AcquireWriteTask() *writeTask {
	return writeTaskPool.Get().(*writeTask)
}

func ReleaseWriteTask(task *writeTask) {
	task.messageType = 0
	task.data = task.data[:0]
	writeTaskPool.Put(task)
}

func AcquireByteSlice(size int) *[]byte {
	if size <= 64*1024 {
		return byteSlicePool.Get().(*[]byte)
	}
	b := make([]byte, 0, size)
	return &b
}

func ReleaseByteSlice(b *[]byte) {
	if cap(*b) <= 64*1024 {
		*b = (*b)[:0]
		byteSlicePool.Put(b)
	}
}

type MessageType int

const (
	MessageTypeText MessageType = iota
	MessageTypeBinary
	MessageTypeControl
)

const (
	ChunkSize       = 1024 * 1024 // 1MB per chunk
	ChunkHeaderSize = 13          // 1 byte type + 4 bytes index + 4 bytes hash + 4 bytes length
	MaxChunkIndex   = 65535
	ChunkTypeStart  = 0x01
	ChunkTypeData   = 0x02
	ChunkTypeEnd    = 0x03
)

type ChunkMeta struct {
	MessageID   string `json:"messageId"`
	TotalSize   int64  `json:"totalSize"`
	ChunkCount  int    `json:"chunkCount"`
	ContentType string `json:"contentType"`
	Compressed  bool   `json:"compressed"`
	Checksum    string `json:"checksum,omitempty"`
}

type Message struct {
	Type        MessageType `json:"type"`
	ClientID    string      `json:"clientId"`
	MessageID   string      `json:"messageId"`
	Timestamp   int64       `json:"timestamp"`
	Data        []byte      `json:"data"`
	Route       string      `json:"route,omitempty"`
	ContentType string      `json:"contentType,omitempty"`
	Compressed  bool        `json:"compressed,omitempty"`
}

type ChunkAssembler struct {
	mu      sync.Mutex
	buffers map[string]*chunkBuffer
	hashMap map[uint32]string
}

func NewChunkAssembler() *ChunkAssembler {
	return &ChunkAssembler{
		buffers: make(map[string]*chunkBuffer),
		hashMap: make(map[uint32]string),
	}
}

func (ca *ChunkAssembler) StartChunk(meta ChunkMeta) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	hash := hashMessageID(meta.MessageID)
	ca.buffers[meta.MessageID] = &chunkBuffer{
		meta:       meta,
		chunks:     make(map[int][]byte),
		receivedAt: time.Now(),
	}
	ca.hashMap[hash] = meta.MessageID
}

type chunkBuffer struct {
	meta       ChunkMeta
	chunks     map[int][]byte
	receivedAt time.Time
}

func (ca *ChunkAssembler) AddChunk(messageID string, index int, data []byte) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if buf, ok := ca.buffers[messageID]; ok {
		buf.chunks[index] = data
	}
}

func (ca *ChunkAssembler) CompleteChunk(messageID, checksum string) (*Message, bool) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	buf, ok := ca.buffers[messageID]
	if !ok {
		return nil, false
	}

	for i := 0; i < buf.meta.ChunkCount; i++ {
		if _, ok := buf.chunks[i]; !ok {
			return nil, false
		}
	}

	data := make([]byte, 0, buf.meta.TotalSize)
	for i := 0; i < buf.meta.ChunkCount; i++ {
		data = append(data, buf.chunks[i]...)
	}

	delete(ca.buffers, messageID)
	hash := hashMessageID(messageID)
	delete(ca.hashMap, hash)

	return &Message{
		Type:        MessageTypeBinary,
		MessageID:   messageID,
		Data:        data,
		ContentType: buf.meta.ContentType,
		Compressed:  buf.meta.Compressed,
	}, true
}

func (ca *ChunkAssembler) GetMessageIDByHash(hash uint32) string {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	return ca.hashMap[hash]
}

func hashMessageID(messageID string) uint32 {
	h := uint32(0)
	for _, c := range messageID {
		h = h*31 + uint32(c)
	}
	return h
}

func (ca *ChunkAssembler) Cleanup(timeout time.Duration) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	now := time.Now()
	for id, buf := range ca.buffers {
		if now.Sub(buf.receivedAt) > timeout {
			delete(ca.buffers, id)
		}
	}
}

type Connection struct {
	ID              string
	WS              *websocket.Conn
	LastActive      time.Time
	mu              sync.RWMutex
	closed          bool
	closeOnce       sync.Once
	sendChan        chan *Message
	writeChan       chan *writeTask
	closeChan       chan struct{}
	readTimeout     time.Duration
	heartbeatPeriod time.Duration
	assembler       *ChunkAssembler
}

type writeTask struct {
	messageType int
	data        []byte
}

func NewConnection(id string, ws *websocket.Conn) *Connection {
	return &Connection{
		ID:              id,
		WS:              ws,
		LastActive:      time.Now(),
		sendChan:        make(chan *Message, 256),
		writeChan:       make(chan *writeTask, 256),
		closeChan:       make(chan struct{}),
		readTimeout:     90 * time.Second,
		heartbeatPeriod: 30 * time.Second,
		assembler:       NewChunkAssembler(),
	}
}

func NewConnectionWithTimeout(id string, ws *websocket.Conn, readTimeout, heartbeatPeriod time.Duration) *Connection {
	if readTimeout <= 0 {
		readTimeout = 90 * time.Second
	}
	if heartbeatPeriod <= 0 {
		heartbeatPeriod = 30 * time.Second
	}
	return &Connection{
		ID:              id,
		WS:              ws,
		LastActive:      time.Now(),
		sendChan:        make(chan *Message, 256),
		writeChan:       make(chan *writeTask, 256),
		closeChan:       make(chan struct{}),
		readTimeout:     readTimeout,
		heartbeatPeriod: heartbeatPeriod,
		assembler:       NewChunkAssembler(),
	}
}

func (c *Connection) StartRead(handler func(*Message)) {
	go func() {
		c.WS.SetReadDeadline(time.Now().Add(c.readTimeout))

		c.WS.SetPingHandler(func(appData string) error {
			if len(appData) > 125 {
				log.Warn().Str("connId", c.ID).Msg("Oversized ping frame rejected")
				return nil
			}
			c.WS.SetReadDeadline(time.Now().Add(c.readTimeout))
			c.mu.Lock()
			c.LastActive = time.Now()
			c.mu.Unlock()
			return c.WS.WriteMessage(websocket.PongMessage, []byte(appData))
		})

		c.WS.SetPongHandler(func(string) error {
			c.WS.SetReadDeadline(time.Now().Add(c.readTimeout))
			c.mu.Lock()
			c.LastActive = time.Now()
			c.mu.Unlock()
			return nil
		})

		c.WS.SetCloseHandler(func(code int, text string) error {
			if len(text) > 123 {
				text = text[:123]
			}
			log.Debug().Str("connId", c.ID).Int("code", code).Msg("Received close frame")
			return nil
		})

		for {
			messageType, data, err := c.WS.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
					log.Error().Err(err).Str("connId", c.ID).Msg("Unexpected close error")
				} else if netErr, ok := err.(*websocket.CloseError); ok {
					log.Debug().Str("connId", c.ID).Int("code", netErr.Code).Msg("Connection closed")
				} else {
					log.Error().Err(err).Str("connId", c.ID).Msg("Read error")
				}
				c.Close()
				return
			}

			c.WS.SetReadDeadline(time.Now().Add(c.readTimeout))

			if messageType == websocket.BinaryMessage {
				msg := c.handleBinaryData(data, handler)
				if msg != nil {
					msg.ClientID = c.ID
					msg.Timestamp = time.Now().UnixNano()
					c.mu.Lock()
					c.LastActive = time.Now()
					c.mu.Unlock()
					handler(msg)
				}
			} else {
				var msg Message
				if err := json.Unmarshal(data, &msg); err != nil {
					log.Error().Err(err).Str("connId", c.ID).Msg("Invalid message format")
					continue
				}

				msg.ClientID = c.ID
				msg.Timestamp = time.Now().UnixNano()
				c.mu.Lock()
				c.LastActive = time.Now()
				c.mu.Unlock()
				handler(&msg)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.assembler.Cleanup(10 * time.Minute)
			case <-c.closeChan:
				return
			}
		}
	}()
}

func (c *Connection) handleBinaryData(data []byte, handler func(*Message)) *Message {
	if len(data) < 1 {
		return nil
	}

	chunkType := data[0]

	switch chunkType {
	case ChunkTypeStart:
		var meta ChunkMeta
		if err := json.Unmarshal(data[1:], &meta); err != nil {
			log.Error().Err(err).Str("connId", c.ID).Msg("Invalid chunk start")
			return nil
		}
		c.assembler.StartChunk(meta)
		log.Debug().Str("messageId", meta.MessageID).Int("chunks", meta.ChunkCount).Msg("Chunk transfer started")
		return nil

	case ChunkTypeData:
		if len(data) < ChunkHeaderSize {
			log.Error().Str("connId", c.ID).Msg("Invalid chunk data header")
			return nil
		}
		index := int(binary.BigEndian.Uint32(data[1:5]))
		messageIDHash := binary.BigEndian.Uint32(data[5:9])
		chunkData := data[ChunkHeaderSize:]

		messageID := c.findMessageIDByHash(messageIDHash)
		if messageID == "" {
			log.Warn().Uint32("hash", messageIDHash).Msg("Unknown messageID hash")
			return nil
		}
		c.assembler.AddChunk(messageID, index, chunkData)
		return nil

	case ChunkTypeEnd:
		var end struct {
			MessageID string `json:"messageId"`
			Checksum  string `json:"checksum"`
		}
		if err := json.Unmarshal(data[1:], &end); err != nil {
			log.Error().Err(err).Str("connId", c.ID).Msg("Invalid chunk end")
			return nil
		}
		msg, ok := c.assembler.CompleteChunk(end.MessageID, end.Checksum)
		if !ok {
			log.Warn().Str("messageId", end.MessageID).Msg("Chunk assembly incomplete")
			return nil
		}
		log.Debug().Str("messageId", end.MessageID).Int("size", len(msg.Data)).Msg("Chunk transfer complete")
		return msg

	default:
		return &Message{
			Type: MessageTypeBinary,
			Data: data,
		}
	}
}

func (c *Connection) findMessageIDByHash(hash uint32) string {
	return c.assembler.GetMessageIDByHash(hash)
}

func (c *Connection) CreateChunks(msg *Message) [][]byte {
	if len(msg.Data) <= ChunkSize {
		return nil
	}

	dataLen := int64(len(msg.Data))
	chunkCount := int((dataLen + ChunkSize - 1) / ChunkSize)
	chunks := make([][]byte, 0, chunkCount+2)

	meta := ChunkMeta{
		MessageID:   msg.MessageID,
		TotalSize:   dataLen,
		ChunkCount:  chunkCount,
		ContentType: msg.ContentType,
		Compressed:  msg.Compressed,
	}
	startData, _ := json.Marshal(meta)
	startChunk := make([]byte, 1+len(startData))
	startChunk[0] = ChunkTypeStart
	copy(startChunk[1:], startData)
	chunks = append(chunks, startChunk)

	messageIDHash := hashMessageID(msg.MessageID)
	for i := 0; i < chunkCount; i++ {
		start := int64(i) * ChunkSize
		end := start + ChunkSize
		if end > dataLen {
			end = dataLen
		}
		chunkData := msg.Data[start:end]

		chunk := make([]byte, ChunkHeaderSize+len(chunkData))
		chunk[0] = ChunkTypeData
		binary.BigEndian.PutUint32(chunk[1:5], uint32(i))
		binary.BigEndian.PutUint32(chunk[5:9], messageIDHash)
		binary.BigEndian.PutUint32(chunk[9:13], uint32(len(chunkData)))
		copy(chunk[ChunkHeaderSize:], chunkData)
		chunks = append(chunks, chunk)
	}

	endData, _ := json.Marshal(struct {
		MessageID string `json:"messageId"`
		Checksum  string `json:"checksum"`
	}{
		MessageID: msg.MessageID,
		Checksum:  "",
	})
	endChunk := make([]byte, 1+len(endData))
	endChunk[0] = ChunkTypeEnd
	copy(endChunk[1:], endData)
	chunks = append(chunks, endChunk)

	return chunks
}

func (c *Connection) SendLargeMessage(msg *Message) {
	chunks := c.CreateChunks(msg)
	if chunks == nil {
		c.Send(msg)
		return
	}

	log.Info().Str("messageId", msg.MessageID).Int("chunks", len(chunks)).Int64("size", int64(len(msg.Data))).Msg("Sending large message in chunks")

	for _, chunk := range chunks {
		select {
		case c.writeChan <- &writeTask{messageType: websocket.BinaryMessage, data: chunk}:
		default:
			log.Warn().Str("connId", c.ID).Msg("Write buffer full during chunk transfer")
			return
		}
	}
}

func (c *Connection) StartWrite() {
	go func() {
		ticker := time.NewTicker(c.heartbeatPeriod)
		defer ticker.Stop()

		for {
			select {
			case task := <-c.writeChan:
				c.WS.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if err := c.WS.WriteMessage(task.messageType, task.data); err != nil {
					log.Error().Err(err).Str("connId", c.ID).Msg("Write error")
					return
				}

			case <-ticker.C:
				c.WS.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := c.WS.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Error().Err(err).Str("connId", c.ID).Msg("Ping error")
					return
				}

			case <-c.closeChan:
				return
			}
		}
	}()
}

func (c *Connection) Send(msg *Message) {
	var data []byte
	var err error
	var messageType int

	if msg.Type == MessageTypeBinary {
		messageType = websocket.BinaryMessage
		data = msg.Data
	} else {
		messageType = websocket.TextMessage
		data, err = json.Marshal(msg)
		if err != nil {
			log.Error().Err(err).Str("connId", c.ID).Msg("Marshal message error")
			return
		}
	}

	select {
	case c.writeChan <- &writeTask{messageType: messageType, data: data}:
	default:
		log.Warn().Str("connId", c.ID).Msg("Send buffer full")
	}
}

func (c *Connection) SendBinary(data []byte, contentType string) {
	select {
	case c.writeChan <- &writeTask{messageType: websocket.BinaryMessage, data: data}:
	default:
		log.Warn().Str("connId", c.ID).Msg("Send buffer full")
	}
}

func (c *Connection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		close(c.closeChan)
		err = c.WS.Close()
	})
	return err
}

func (c *Connection) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

func (c *Connection) CloseChan() <-chan struct{} {
	return c.closeChan
}
