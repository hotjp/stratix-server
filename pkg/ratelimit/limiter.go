package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	clients    map[string]*clientLimiter
	mu         sync.RWMutex
	rate       int
	burst      int
	cleanupInt time.Duration
}

type clientLimiter struct {
	tokens     float64
	lastUpdate time.Time
}

func NewLimiter(rate, burst int) *Limiter {
	l := &Limiter{
		clients:    make(map[string]*clientLimiter),
		rate:       rate,
		burst:      burst,
		cleanupInt: 5 * time.Minute,
	}
	go l.cleanup()
	return l
}

func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cl, exists := l.clients[ip]
	if !exists {
		cl = &clientLimiter{
			tokens:     float64(l.burst - 1),
			lastUpdate: now,
		}
		l.clients[ip] = cl
		return true
	}

	elapsed := now.Sub(cl.lastUpdate).Seconds()
	cl.tokens += float64(l.rate) * elapsed
	if cl.tokens > float64(l.burst) {
		cl.tokens = float64(l.burst)
	}
	cl.lastUpdate = now

	if cl.tokens >= 1 {
		cl.tokens -= 1
		return true
	}
	return false
}

func (l *Limiter) Wait(ip string) bool {
	return l.Allow(ip)
}

func (l *Limiter) GetTokens(ip string) float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	cl, exists := l.clients[ip]
	if !exists {
		return float64(l.burst)
	}
	return cl.tokens
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(l.cleanupInt)
	defer ticker.Stop()

	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for ip, cl := range l.clients {
			if now.Sub(cl.lastUpdate) > 10*time.Minute {
				delete(l.clients, ip)
			}
		}
		l.mu.Unlock()
	}
}

func (l *Limiter) Stats() (totalClients int, avgTokens float64) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	totalClients = len(l.clients)
	if totalClients == 0 {
		return
	}

	var sum float64
	for _, cl := range l.clients {
		sum += cl.tokens
	}
	avgTokens = sum / float64(totalClients)
	return
}

func (l *Limiter) Reset(ip string) {
	l.mu.Lock()
	delete(l.clients, ip)
	l.mu.Unlock()
}
