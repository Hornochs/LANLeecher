// Package board builds the beamer/scoreboard content. The /board page and
// GET /api/v1/board render exactly the same data, so every overlay or bot
// sees what the beamer shows.
package board

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// Board is the JSON payload of GET /api/v1/board.
type Board struct {
	Event       *Event     `json:"event"`
	GeneratedAt time.Time  `json:"generated_at"`
	Totals      Totals     `json:"totals"`
	Throughput  Throughput `json:"throughput"`
	TopDownload []Entry    `json:"top_download"`
	TopUpload   []Entry    `json:"top_upload"`
}

// Event identifies the shown event.
type Event struct {
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Totals of the event.
type Totals struct {
	BytesDown int64 `json:"bytes_down"`
	BytesUp   int64 `json:"bytes_up"`
	Clients   int   `json:"clients"`
}

// Throughput is the current rate of all non-uplink ports in bit/s.
type Throughput struct {
	DownBps int64 `json:"down_bps"`
	UpBps   int64 `json:"up_bps"`
}

// Entry is a ranking line without MACs or IPs.
type Entry struct {
	Rank int `json:"rank"`
	// RankChange is positive when the entry moved up since the previous poll.
	RankChange int    `json:"rank_change"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	BytesDown  int64  `json:"bytes_down"`
	BytesUp    int64  `json:"bytes_up"`
}

// Service builds boards and notifies stream subscribers after each poll.
type Service struct {
	db         *store.DB
	throughput func() (float64, float64)

	mu   sync.Mutex
	prev map[string]map[string]int
	cur  map[string]map[string]int
	subs map[chan struct{}]struct{}
	last time.Time
}

// New creates a board service. throughput returns the live rates.
func New(db *store.DB, throughput func() (float64, float64)) *Service {
	return &Service{db: db, throughput: throughput, prev: map[string]map[string]int{}, cur: map[string]map[string]int{},
		subs: map[chan struct{}]struct{}{}}
}

// ValidLimit reports the allowed board sizes.
func ValidLimit(n int) bool { return n == 10 || n == 20 || n == 50 }

// Build returns the board for an event (nil = active event).
func (s *Service) Build(ctx context.Context, ev *store.Event, limit int) (*Board, error) {
	if !ValidLimit(limit) {
		limit = 10
	}
	b := &Board{GeneratedAt: store.Now(), TopDownload: []Entry{}, TopUpload: []Entry{}}
	if ev == nil {
		active, err := s.db.ActiveEvent(ctx)
		if err == store.ErrNotFound {
			return b, nil
		}
		if err != nil {
			return nil, err
		}
		ev = active
	}
	b.Event = &Event{Name: ev.Name, Slug: ev.Slug, StartedAt: ev.StartedAt, EndedAt: ev.EndedAt}
	sum, err := s.db.EventSummary(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	b.Totals = Totals{BytesDown: sum.BytesDown, BytesUp: sum.BytesUp, Clients: sum.Clients}
	if ev.Active() && s.throughput != nil {
		d, u := s.throughput()
		b.Throughput = Throughput{DownBps: int64(d), UpBps: int64(u)}
	}
	for _, dir := range []string{"down", "up"} {
		rows, err := s.db.Rankings(ctx, store.RankingQuery{EventID: ev.ID, Order: dir, Limit: limit})
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%d/%d/%s", ev.ID, limit, dir)
		ranks := map[string]int{}
		s.mu.Lock()
		prev := s.prev[key]
		entries := make([]Entry, 0, len(rows))
		for _, r := range rows {
			id := fmt.Sprintf("%s/%d", r.Type, r.Key)
			ranks[id] = r.Rank
			change := 0
			if p, ok := prev[id]; ok {
				change = p - r.Rank
			} else if prev != nil {
				change = limit + 1 - r.Rank // new in the list
			}
			entries = append(entries, Entry{Rank: r.Rank, RankChange: change, Name: PublicName(r.Type, r.Key, r.Name),
				Type: r.Type, BytesDown: r.BytesDown, BytesUp: r.BytesUp})
		}
		s.cur[key] = ranks
		s.mu.Unlock()
		if dir == "down" {
			b.TopDownload = entries
		} else {
			b.TopUpload = entries
		}
	}
	return b, nil
}

// PublicName hides MAC addresses in names shown without read:clients: a
// device without display name or hostname is named after its MAC.
func PublicName(typ string, key int64, name string) string {
	if typ == "mac" && macaddr.Normalize(name) != "" {
		return fmt.Sprintf("Client #%d", key)
	}
	return name
}

// Notify is called after each poll. It keeps the current ranks as the
// baseline for rank changes and wakes stream subscribers (at most every 2 s).
func (s *Service) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.last) < 2*time.Second {
		return
	}
	s.last = time.Now()
	for k, v := range s.cur {
		s.prev[k] = v
	}
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe returns a channel that fires after polls, and a cancel function.
func (s *Service) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}
