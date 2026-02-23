package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"stratix-server/config"
	"stratix-server/pkg/buffer"
	"stratix-server/pkg/conn"
	"stratix-server/pkg/history"
	"stratix-server/pkg/memlimit"
	"stratix-server/pkg/metrics"
	"stratix-server/pkg/ratelimit"
	"stratix-server/pkg/router"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type OpenClawConnection struct {
	GatewayURL string
	WS         *websocket.Conn
	Active     bool
	mu         sync.RWMutex
	writeChan  chan []byte
	closeChan  chan struct{}
}

const OpenClawWriteTimeout = 30 * time.Second
const OpenClawWriteBufferSize = 10000

type Gateway struct {
	config            *config.Config
	router            *router.Router
	buffer            *buffer.Buffer
	metrics           *metrics.Metrics
	memLimiter        *memlimit.Limiter
	rateLimiter       *ratelimit.Limiter
	historyStore      *history.Store
	clients           map[string]*conn.Connection
	openclawClients   map[string]*OpenClawConnection
	clientsMu         sync.RWMutex
	openclawMu        sync.Mutex
	server            *http.Server
	upgrader          websocket.Upgrader
	ctx               context.Context
	cancel            context.CancelFunc
	openclawConnected bool
}

func NewGateway(cfg *config.Config) (*Gateway, error) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	buf, err := buffer.NewBuffer(cfg.Buffer.Size, cfg.Buffer.PersistFile)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	memLimiter := memlimit.NewLimiter(cfg.Memory.MaxMemoryMB)

	var rateLimiter *ratelimit.Limiter
	if cfg.Gateway.RateLimit.Enabled {
		rateLimiter = ratelimit.NewLimiter(
			cfg.Gateway.RateLimit.RequestsPerSecond,
			cfg.Gateway.RateLimit.BurstSize,
		)
	}

	var historyStore *history.Store
	if cfg.Gateway.History.Enabled {
		historyStore = history.NewStore(
			cfg.Gateway.History.MaxPerClient,
			int64(cfg.Gateway.History.MaxMsgSize)*1024,
		)
	}

	gw := &Gateway{
		config:          cfg,
		router:          router.NewRouter(cfg.Routes),
		buffer:          buf,
		metrics:         metrics.NewMetrics(),
		memLimiter:      memLimiter,
		rateLimiter:     rateLimiter,
		historyStore:    historyStore,
		clients:         make(map[string]*conn.Connection),
		openclawClients: make(map[string]*OpenClawConnection),
		ctx:             ctx,
		cancel:          cancel,
		upgrader: websocket.Upgrader{
			ReadBufferSize:    cfg.Gateway.ReadBufferSize,
			WriteBufferSize:   cfg.Gateway.WriteBufferSize,
			HandshakeTimeout:  10 * time.Second,
			EnableCompression: cfg.Gateway.Security.EnableCompression,
			CheckOrigin: func(r *http.Request) bool {
				return checkOrigin(cfg)(r)
			},
		},
	}

	gw.server = &http.Server{
		Addr:         cfg.Gateway.Listen,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      gw.setupRouter(),
	}

	return gw, nil
}

func checkOrigin(cfg *config.Config) func(r *http.Request) bool {
	allowedOrigins := cfg.Gateway.Security.AllowedOrigins
	if len(allowedOrigins) == 0 {
		return func(r *http.Request) bool {
			return true
		}
	}

	originSet := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		originSet[origin] = true
	}

	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		_, ok := originSet[origin]
		return ok
	}
}

func (gw *Gateway) setupRouter() http.Handler {
	r := gin.New()
	r.Use(gin.Recovery())

	// WebSocket endpoint
	r.GET("/ws", gw.handleWebSocket)

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Metrics endpoint
	r.GET("/metrics", gw.handleMetrics)

	return r
}

func (gw *Gateway) handleWebSocket(c *gin.Context) {
	clientId := c.Query("clientId")
	if clientId == "" {
		c.JSON(400, gin.H{"error": "clientId required"})
		return
	}

	if gw.rateLimiter != nil {
		ip := c.ClientIP()
		if !gw.rateLimiter.Allow(ip) {
			gw.metrics.IncRateLimitExceeded()
			c.JSON(429, gin.H{"error": "rate limit exceeded"})
			return
		}
	}

	gw.clientsMu.RLock()
	activeCount := len(gw.clients)
	gw.clientsMu.RUnlock()

	if activeCount >= gw.config.Gateway.MaxConnections {
		c.JSON(503, gin.H{"error": "max connections reached"})
		return
	}

	ws, err := gw.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Str("clientId", clientId).Msg("WebSocket upgrade failed")
		return
	}

	ws.SetReadLimit(gw.config.Gateway.MaxMessageSize)

	conn := conn.NewConnectionWithTimeout(clientId, ws, gw.config.Gateway.ConnectionTimeout, gw.config.Gateway.HeartbeatInterval)

	gw.clientsMu.Lock()
	gw.clients[clientId] = conn
	gw.clientsMu.Unlock()

	gw.metrics.IncConnectionsTotal()
	gw.metrics.AddConnectionsActive(1)

	log.Info().Str("clientId", clientId).Msg("Client connected")

	conn.StartRead(gw.handleMessage)
	conn.StartWrite()

	ws.SetCloseHandler(func(code int, text string) error {
		gw.handleDisconnect(clientId)
		return nil
	})

	loadHistory := c.Query("loadHistory") == "true"
	if loadHistory && gw.historyStore != nil {
		lastMessageId := c.Query("lastMessageId")
		limit := 100
		if l := c.Query("limit"); l != "" {
			var lim int
			if _, err := json.Marshal(lim); err == nil {
				limit = lim
			}
		}
		go gw.sendHistory(conn, clientId, lastMessageId, limit)
	}

	go func() {
		select {
		case <-conn.CloseChan():
			gw.handleDisconnect(clientId)
		case <-gw.ctx.Done():
			conn.Close()
		}
	}()
}

func (gw *Gateway) handleDisconnect(clientId string) {
	gw.clientsMu.Lock()
	defer gw.clientsMu.Unlock()

	if conn, ok := gw.clients[clientId]; ok {
		if !conn.IsClosed() {
			conn.Close()
		}
		delete(gw.clients, clientId)
		gw.router.RemoveRoute(clientId)
		gw.metrics.AddConnectionsActive(-1)
		log.Info().Str("clientId", clientId).Msg("Client disconnected")
	}
}

func (gw *Gateway) sendHistory(c *conn.Connection, clientID, lastMessageID string, limit int) {
	if gw.historyStore == nil {
		return
	}

	history := gw.historyStore.Get(clientID, lastMessageID, limit)
	if len(history) == 0 {
		return
	}

	total := len(history)
	log.Info().Str("clientId", clientID).Int("count", total).Str("after", lastMessageID).Msg("Sending history")

	for i, msg := range history {
		if c.IsClosed() {
			return
		}

		historyMsg := &conn.Message{
			Type:        conn.MessageTypeText,
			MessageID:   msg.MessageID,
			ClientID:    msg.ClientID,
			Data:        msg.Data,
			Timestamp:   msg.Timestamp,
			ContentType: msg.ContentType,
		}
		c.Send(historyMsg)

		if (i+1)%10 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	completeMsg := map[string]interface{}{
		"type":            conn.MessageTypeControl,
		"action":          "history_complete",
		"total":           total,
		"oldestMessageId": history[0].MessageID,
		"newestMessageId": history[total-1].MessageID,
	}
	data, _ := json.Marshal(completeMsg)
	c.Send(&conn.Message{
		Type:      conn.MessageTypeText,
		Data:      data,
		Timestamp: time.Now().UnixNano(),
	})
}

func (gw *Gateway) handleMessage(msg *conn.Message) {
	msgSize := int64(len(msg.Data))

	if gw.memLimiter.IsUnderPressure() {
		if msgSize > 64*1024 {
			log.Warn().Str("clientId", msg.ClientID).Int64("size", msgSize).Msg("Memory pressure, rejecting large message")
			gw.metrics.IncMessagesFailed()
			return
		}
	}

	if !gw.memLimiter.CanAllocate(msgSize) {
		log.Warn().Str("clientId", msg.ClientID).Int64("size", msgSize).Msg("Memory limit exceeded, rejecting message")
		gw.metrics.IncMessagesFailed()
		return
	}

	gw.memLimiter.Allocate(msgSize)
	defer gw.memLimiter.Release(msgSize)

	gw.metrics.IncMessagesReceived()
	gw.metrics.AddBytesReceived(msgSize)

	if msgSize > metrics.LargeMessageThreshold {
		gw.metrics.IncLargeMessages(msgSize)
		log.Info().Str("clientId", msg.ClientID).Int64("size", msgSize).Msg("Large message received")
	}

	if gw.historyStore != nil && msg.MessageID != "" {
		gw.historyStore.Add(msg.ClientID, msg.MessageID, msg.Data, msg.ContentType)
	}

	gatewayUrl, ok := gw.router.GetRoute(msg.ClientID)
	if !ok {
		log.Warn().Str("clientId", msg.ClientID).Msg("No route found")
		gw.metrics.IncMessagesFailed()
		return
	}

	msg.Route = gatewayUrl
	gw.buffer.Put(msg)

	if err := gw.forwardToOpenClaw(gatewayUrl, msg); err != nil {
		log.Error().Err(err).Str("gateway", gatewayUrl).Msg("Failed to forward message")
		gw.metrics.IncMessagesFailed()
		return
	}

	gw.metrics.IncMessagesSent()
	gw.metrics.AddBytesSent(int64(len(msg.Data)))
}

func (gw *Gateway) forwardToOpenClaw(gatewayURL string, msg *conn.Message) error {
	gw.clientsMu.RLock()
	ocConn, exists := gw.openclawClients[gatewayURL]
	gw.clientsMu.RUnlock()

	if !exists || !gw.isOpenClawActive(ocConn) {
		gw.openclawMu.Lock()
		gw.clientsMu.RLock()
		ocConn, exists = gw.openclawClients[gatewayURL]
		gw.clientsMu.RUnlock()

		if !exists || !gw.isOpenClawActive(ocConn) {
			var err error
			ocConn, err = gw.connectToOpenClawLocked(gatewayURL)
			gw.openclawMu.Unlock()
			if err != nil {
				return err
			}
		} else {
			gw.openclawMu.Unlock()
		}
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	select {
	case ocConn.writeChan <- data:
		return nil
	default:
		log.Warn().Str("gateway", gatewayURL).Msg("OpenClaw write buffer full")
		return errors.New("write buffer full")
	}
}

// connectToOpenClaw 连接到 OpenClaw Gateway
func (gw *Gateway) connectToOpenClaw(gatewayURL string) (*OpenClawConnection, error) {
	gw.openclawMu.Lock()
	defer gw.openclawMu.Unlock()
	return gw.connectToOpenClawLocked(gatewayURL)
}

func (gw *Gateway) connectToOpenClawLocked(gatewayURL string) (*OpenClawConnection, error) {
	log.Info().Str("gateway", gatewayURL).Msg("Connecting to OpenClaw Gateway")

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	ws, _, err := dialer.Dial(gatewayURL, nil)
	if err != nil {
		return nil, err
	}

	ocConn := &OpenClawConnection{
		GatewayURL: gatewayURL,
		WS:         ws,
		Active:     true,
		writeChan:  make(chan []byte, OpenClawWriteBufferSize),
		closeChan:  make(chan struct{}),
	}

	gw.clientsMu.Lock()
	gw.openclawClients[gatewayURL] = ocConn
	gw.clientsMu.Unlock()

	go gw.handleOpenClawMessages(ocConn)
	go gw.openClawWriteLoop(ocConn)

	log.Info().Str("gateway", gatewayURL).Msg("Connected to OpenClaw Gateway")

	return ocConn, nil
}

func (gw *Gateway) openClawWriteLoop(ocConn *OpenClawConnection) {
	for {
		select {
		case data := <-ocConn.writeChan:
			ocConn.mu.RLock()
			ws := ocConn.WS
			ocConn.mu.RUnlock()

			if ws == nil {
				return
			}

			ws.SetWriteDeadline(time.Now().Add(OpenClawWriteTimeout))
			if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Error().Err(err).Str("gateway", ocConn.GatewayURL).Msg("Write error to OpenClaw")
				ocConn.mu.Lock()
				ocConn.Active = false
				if ocConn.WS != nil {
					ocConn.WS.Close()
					ocConn.WS = nil
				}
				ocConn.mu.Unlock()
				return
			}

		case <-ocConn.closeChan:
			return

		case <-gw.ctx.Done():
			return
		}
	}
}

func (gw *Gateway) handleOpenClawMessages(ocConn *OpenClawConnection) {
	defer func() {
		close(ocConn.closeChan)
		ocConn.mu.Lock()
		ocConn.Active = false
		if ocConn.WS != nil {
			ocConn.WS.Close()
			ocConn.WS = nil
		}
		ocConn.mu.Unlock()
	}()

	for {
		_, data, err := ocConn.WS.ReadMessage()
		if err != nil {
			log.Error().Err(err).Str("gateway", ocConn.GatewayURL).Msg("Read error from OpenClaw")
			return
		}

		var msg conn.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Error().Err(err).Msg("Invalid message from OpenClaw")
			continue
		}

		gw.clientsMu.RLock()
		client, ok := gw.clients[msg.ClientID]
		gw.clientsMu.RUnlock()

		if ok && !client.IsClosed() {
			client.Send(&msg)
		} else {
			log.Warn().Str("clientId", msg.ClientID).Msg("Client not found for response")
		}
	}
}

func (gw *Gateway) isOpenClawActive(ocConn *OpenClawConnection) bool {
	if ocConn == nil {
		return false
	}
	ocConn.mu.RLock()
	defer ocConn.mu.RUnlock()
	return ocConn.Active && ocConn.WS != nil
}

func (gw *Gateway) handleMetrics(c *gin.Context) {
	stats := gw.metrics.GetStats()

	gw.clientsMu.RLock()
	stats["currentConnections"] = int64(len(gw.clients))
	stats["openclawConnections"] = int64(len(gw.openclawClients))
	gw.clientsMu.RUnlock()

	used, max, percent := gw.memLimiter.GetUsage()
	stats["memoryUsedMB"] = used / 1024 / 1024
	stats["memoryLimitMB"] = max / 1024 / 1024
	stats["memoryPercent"] = int64(percent * 100)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	stats["heapAllocMB"] = int64(m.HeapAlloc / 1024 / 1024)
	stats["heapSysMB"] = int64(m.HeapSys / 1024 / 1024)
	stats["numGoroutine"] = int64(runtime.NumGoroutine())

	if gw.rateLimiter != nil {
		totalClients, avgTokens := gw.rateLimiter.Stats()
		stats["rateLimitClients"] = int64(totalClients)
		stats["rateLimitAvgTokens"] = int64(avgTokens)
		stats["rateLimitQPS"] = int64(gw.config.Gateway.RateLimit.RequestsPerSecond)
	}

	if gw.historyStore != nil {
		historyStats := gw.historyStore.GetStats()
		stats["historyClients"] = historyStats["totalClients"]
		stats["historyMessages"] = historyStats["totalMessages"]
	}

	c.JSON(200, stats)
}

func (gw *Gateway) StartMetricsLogger() {
	if !gw.config.Metrics.Enabled {
		return
	}

	ticker := time.NewTicker(gw.config.Metrics.LogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats := gw.metrics.GetStats()
			used, max, percent := gw.memLimiter.GetUsage()
			log.Info().
				Int64("connections", stats["connectionsActive"]).
				Int64("openclawConnections", stats["openclawConnections"]).
				Int64("messagesRcv", stats["messagesReceived"]).
				Int64("messagesSent", stats["messagesSent"]).
				Int64("memoryMB", used/1024/1024).
				Int64("memoryLimitMB", max/1024/1024).
				Float64("memoryPercent", percent).
				Int64("uptime", stats["uptimeSeconds"]).
				Msg("Gateway metrics")
		case <-gw.ctx.Done():
			return
		}
	}
}

func (gw *Gateway) Start() error {
	log.Info().
		Str("listen", gw.config.Gateway.Listen).
		Int("maxConnections", gw.config.Gateway.MaxConnections).
		Int("maxMemoryMB", gw.config.Memory.MaxMemoryMB).
		Msg("Starting Gateway")

	go gw.StartMetricsLogger()
	go gw.memLimiter.StartMonitor(gw.config.Memory.GCInterval)

	err := gw.server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

func (gw *Gateway) Stop() error {
	log.Info().Msg("Stopping Gateway")

	gw.cancel()

	gw.clientsMu.Lock()
	for _, conn := range gw.clients {
		conn.Close()
	}

	for _, ocConn := range gw.openclawClients {
		select {
		case <-ocConn.closeChan:
		default:
			close(ocConn.closeChan)
		}
		ocConn.mu.Lock()
		ocConn.Active = false
		if ocConn.WS != nil {
			ocConn.WS.Close()
			ocConn.WS = nil
		}
		ocConn.mu.Unlock()
	}
	gw.clientsMu.Unlock()

	gw.buffer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return gw.server.Shutdown(ctx)
}

var ErrOpenClawNotConnected = errors.New("OpenClaw Gateway not connected")
