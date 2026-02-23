package memlimit

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

type Limiter struct {
	maxMemory     int64
	usedMemory    int64
	highWatermark int64
	lowWatermark  int64
	enabled       bool
}

func NewLimiter(maxMemoryMB int) *Limiter {
	maxBytes := int64(maxMemoryMB) * 1024 * 1024
	return &Limiter{
		maxMemory:     maxBytes,
		highWatermark: int64(float64(maxBytes) * 0.8),
		lowWatermark:  int64(float64(maxBytes) * 0.6),
		enabled:       maxMemoryMB > 0,
	}
}

func (l *Limiter) CanAllocate(size int64) bool {
	if !l.enabled {
		return true
	}
	used := atomic.LoadInt64(&l.usedMemory)
	return used+size <= l.maxMemory
}

func (l *Limiter) Allocate(size int64) bool {
	if !l.enabled {
		return true
	}
	for {
		used := atomic.LoadInt64(&l.usedMemory)
		if used+size > l.maxMemory {
			return false
		}
		if atomic.CompareAndSwapInt64(&l.usedMemory, used, used+size) {
			return true
		}
	}
}

func (l *Limiter) Release(size int64) {
	if !l.enabled {
		return
	}
	atomic.AddInt64(&l.usedMemory, -size)
}

func (l *Limiter) GetUsage() (used, max int64, percent float64) {
	used = atomic.LoadInt64(&l.usedMemory)
	max = l.maxMemory
	if max > 0 {
		percent = float64(used) / float64(max) * 100
	}
	return
}

func (l *Limiter) IsUnderPressure() bool {
	if !l.enabled {
		return false
	}
	return atomic.LoadInt64(&l.usedMemory) > l.highWatermark
}

func (l *Limiter) ShouldGC() bool {
	if !l.enabled {
		return false
	}
	return atomic.LoadInt64(&l.usedMemory) > l.lowWatermark
}

func (l *Limiter) StartMonitor(interval time.Duration) {
	if !l.enabled {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			used, max, percent := l.GetUsage()
			log.Debug().
				Uint64("heapAlloc", m.HeapAlloc/1024/1024).
				Uint64("sys", m.Sys/1024/1024).
				Int64("tracked", used/1024/1024).
				Int64("limit", max/1024/1024).
				Float64("percent", percent).
				Msg("Memory stats")

			if l.IsUnderPressure() {
				log.Warn().Float64("percent", percent).Msg("Memory under pressure")
				runtime.GC()
			}
		}
	}()
}

func (l *Limiter) UpdateFromRuntime() int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	atomic.StoreInt64(&l.usedMemory, int64(m.HeapAlloc))
	return int64(m.HeapAlloc)
}

func GetSystemMemory() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys
}
