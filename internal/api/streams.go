package api

import "sync"

// Limits for the board stream (Server-Sent Events). With board.public every
// attendee may open it, so the number of open streams is bounded in total and
// per client address, and a client that stops reading is dropped after
// streamWriteTimeout instead of holding a goroutine forever.
const (
	maxStreams         = 512
	maxStreamsPerIP    = 8
	streamWriteTimeout = 15 // seconds
)

type streamLimits struct {
	mu    sync.Mutex
	total int
	perIP map[string]int
}

// acquire reserves a stream slot for ip; release must be called when it ends.
func (l *streamLimits) acquire(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.perIP == nil {
		l.perIP = map[string]int{}
	}
	if l.total >= maxStreams || l.perIP[ip] >= maxStreamsPerIP {
		return false
	}
	l.total++
	l.perIP[ip]++
	return true
}

func (l *streamLimits) release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	if l.perIP[ip]--; l.perIP[ip] <= 0 {
		delete(l.perIP, ip)
	}
}
