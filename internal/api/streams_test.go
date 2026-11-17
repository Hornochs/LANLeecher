package api

import "testing"

func TestStreamLimits(t *testing.T) {
	var l streamLimits
	for i := 0; i < maxStreamsPerIP; i++ {
		if !l.acquire("192.0.2.1") {
			t.Fatalf("stream %d refused", i+1)
		}
	}
	if l.acquire("192.0.2.1") {
		t.Fatal("more streams than maxStreamsPerIP from one address")
	}
	if !l.acquire("192.0.2.2") {
		t.Fatal("other address refused")
	}
	l.release("192.0.2.1")
	if !l.acquire("192.0.2.1") {
		t.Fatal("slot not freed")
	}
	// Total limit across addresses.
	var m streamLimits
	n := 0
	for i := 0; m.acquire(string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676))); i++ {
		n++
	}
	if n != maxStreams {
		t.Fatalf("total streams = %d, want %d", n, maxStreams)
	}
}
