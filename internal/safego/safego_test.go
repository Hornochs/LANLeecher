package safego

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestSuperviseRestartsAfterPanic(t *testing.T) {
	old := RestartDelay
	RestartDelay = time.Millisecond
	defer func() { RestartDelay = old }()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	runs := 0
	Supervise(context.Background(), log, "test", func(context.Context) {
		runs++
		if runs < 3 {
			panic("boom")
		}
	})
	if runs != 3 {
		t.Fatalf("runs = %d, want 3", runs)
	}
}

func TestDoRecovers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if !Do(log, "test", func() { panic("x") }) {
		t.Fatal("panic not reported")
	}
	if Do(log, "test", func() {}) {
		t.Fatal("false panic report")
	}
}
