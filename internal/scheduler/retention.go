package scheduler

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

// retentionInterval is how often idle MACs are purged.
const retentionInterval = time.Hour

// runRetention deletes MACs without traffic that were not seen for
// polling.idle_mac_retention, once at start and then every hour. Flooded or
// short-lived MACs therefore do not pile up in the database.
func (m *Manager) runRetention(ctx context.Context) {
	t := time.NewTicker(retentionInterval)
	defer t.Stop()
	for {
		m.purgeIdleMACs(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) purgeIdleMACs(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("panic while purging idle MACs", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	n, err := m.db.PurgeIdleMACs(ctx, store.Now().Add(-m.cfg.Polling.IdleMACRetention))
	switch {
	case err != nil && ctx.Err() == nil:
		m.log.Warn("purge idle MACs", "err", err)
	case n > 0:
		m.log.Info("purged idle MACs without traffic", "macs", n, "older_than", m.cfg.Polling.IdleMACRetention)
	}
}
