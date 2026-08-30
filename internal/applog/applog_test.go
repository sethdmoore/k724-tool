package applog

import (
	"strings"
	"testing"
)

func TestRingKeepsRecentAndFormats(t *testing.T) {
	mu.Lock()
	ring = nil
	mu.Unlock()

	for i := 0; i < ringMax+50; i++ {
		Infof("line %d", i)
	}

	got := Lines()
	if len(got) != ringMax {
		t.Fatalf("ring length = %d, want %d", len(got), ringMax)
	}
	if !strings.Contains(got[0], "line 50") {
		t.Errorf("oldest kept line = %q, want it to be %q", got[0], "line 50")
	}
	if !strings.Contains(got[len(got)-1], "line 2049") {
		t.Errorf("newest line = %q, want it to mention line 2049", got[len(got)-1])
	}
	if !strings.Contains(got[0], " INFO  ") {
		t.Errorf("line missing level field: %q", got[0])
	}
}

func TestLevelHelpers(t *testing.T) {
	mu.Lock()
	ring = nil
	mu.Unlock()

	Warnf("careful %s", "now")
	Errorf("boom")

	got := Lines()
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2", len(got))
	}
	if !strings.Contains(got[0], "WARN") || !strings.Contains(got[0], "careful now") {
		t.Errorf("warn line = %q", got[0])
	}
	if !strings.Contains(got[1], "ERROR") || !strings.Contains(got[1], "boom") {
		t.Errorf("error line = %q", got[1])
	}
}
