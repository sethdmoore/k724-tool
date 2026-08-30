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

func TestDebugSuppressedByDefault(t *testing.T) {
	mu.Lock()
	ring = nil
	level = LevelInfo
	mu.Unlock()
	t.Cleanup(func() { SetLevel(LevelInfo) })

	Debugf("noisy detail")
	Infof("visible")

	got := Lines()
	if len(got) != 1 {
		t.Fatalf("got %d lines with the default level, want 1 (Debugf dropped): %v", len(got), got)
	}
	if !strings.Contains(got[0], "visible") {
		t.Errorf("kept line = %q, want the Infof line", got[0])
	}
}

func TestSetLevelDebugShowsEverything(t *testing.T) {
	mu.Lock()
	ring = nil
	mu.Unlock()
	SetLevel(LevelDebug)
	t.Cleanup(func() { SetLevel(LevelInfo) })

	Debugf("noisy detail")

	got := Lines()
	if len(got) != 1 || !strings.Contains(got[0], "noisy detail") {
		t.Fatalf("got %v, want the Debugf line after SetLevel(LevelDebug)", got)
	}
}

func TestSetLevelSuppressesBelowThreshold(t *testing.T) {
	mu.Lock()
	ring = nil
	mu.Unlock()
	SetLevel(LevelError)
	t.Cleanup(func() { SetLevel(LevelInfo) })

	Infof("should be dropped")
	Warnf("should be dropped too")
	Errorf("should survive")

	got := Lines()
	if len(got) != 1 || !strings.Contains(got[0], "should survive") {
		t.Fatalf("got %v, want only the Errorf line after SetLevel(LevelError)", got)
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want Level
		ok   bool
	}{
		{"debug", LevelDebug, true},
		{"DEBUG", LevelDebug, true},
		{"Info", LevelInfo, true},
		{"warn", LevelWarn, true},
		{"warning", LevelWarn, true},
		{"error", LevelError, true},
		{"", LevelInfo, false},
		{"bogus", LevelInfo, false},
	}
	for _, c := range cases {
		got, ok := ParseLevel(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseLevel(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
