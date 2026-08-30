package k724

import (
	"context"
	"testing"
	"time"

	"k724tool/internal/protocol"
)

// These tests drive the real Device methods (ReadSettings, ApplySettings,
// ApplyKeyColors, UploadScreen, Enumerate/Open) against the simulator, not
// against simTransport directly, so they exercise the exact same code path a
// real keyboard would: probe, RunSteps, the commit-delay sleep, the
// read-modify-write helpers.

func openSimTarget(t *testing.T, wired bool) *Device {
	t.Helper()
	target := simTargets()[1] // wireless
	if wired {
		target = simTargets()[0]
	}
	dev, err := Open(target)
	if err != nil {
		t.Fatalf("Open(%s): %v", target.Path, err)
	}
	t.Cleanup(func() { dev.Close() })
	return dev
}

func TestSimEnumerateAndOpen(t *testing.T) {
	t.Setenv("K724_SIM", "1")

	targets, err := Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	var sawWired, sawWireless bool
	for _, tg := range targets {
		switch tg.Path {
		case simPathWired:
			sawWired = true
			if !tg.Wired {
				t.Errorf("simulated wired target has Wired=false")
			}
		case simPathWireless:
			sawWireless = true
			if tg.Wired {
				t.Errorf("simulated wireless target has Wired=true")
			}
		}
	}
	if !sawWired || !sawWireless {
		t.Fatalf("Enumerate did not include both simulated targets: %+v", targets)
	}
}

func TestSimClockRoundTrip(t *testing.T) {
	dev := openSimTarget(t, true)

	when := time.Date(2031, time.March, 4, 5, 6, 7, 0, time.Local)
	if err := dev.SetClock(when); err != nil {
		t.Fatalf("SetClock: %v", err)
	}

	got, err := dev.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	want := protocol.BCD(when.Second())
	if got.Raw[35] != want {
		t.Errorf("seconds byte = 0x%02x, want 0x%02x", got.Raw[35], want)
	}
	readBack := got.Time(time.Local)
	if !readBack.Equal(when) {
		t.Errorf("Time() = %v, want %v", readBack, when)
	}
}

func TestSimSettingsReadModifyWrite(t *testing.T) {
	dev := openSimTarget(t, true)

	// Change only brightness; every other field the simulator started with
	// must survive untouched, proving the RMW path (not a blind write) is
	// what's exercised end to end.
	before, err := dev.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if err := dev.ApplySettings(func(b *protocol.SettingsBlock) { b.SetBrightness(5) }); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	after, err := dev.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}

	if after.Brightness() != 5 {
		t.Errorf("Brightness() = %d, want 5", after.Brightness())
	}
	if after.Effect() != before.Effect() {
		t.Errorf("Effect changed: %d -> %d", before.Effect(), after.Effect())
	}
	if after.Speed() != before.Speed() {
		t.Errorf("Speed changed: %d -> %d", before.Speed(), after.Speed())
	}
	r, g, b := after.Color()
	br, bg, bb := before.Color()
	if r != br || g != bg || b != bb {
		t.Errorf("Colour changed: %02x%02x%02x -> %02x%02x%02x", br, bg, bb, r, g, b)
	}
}

func TestSimApplyKeyColors(t *testing.T) {
	dev := openSimTarget(t, true)

	var table protocol.KeyColorTable
	table.Fill(0, 0, 0)
	table.SetRGB(5, 0xAA, 0xBB, 0xCC)

	if err := dev.ApplyKeyColors(table, [3]byte{0xAA, 0xBB, 0xCC}); err != nil {
		t.Fatalf("ApplyKeyColors: %v", err)
	}

	sim, ok := dev.h.(*simTransport)
	if !ok {
		t.Fatalf("device is not backed by the simulator")
	}
	sim.mu.Lock()
	got, err := protocol.ParseKeyColorTable(sim.keyColors[:])
	sim.mu.Unlock()
	if err != nil {
		t.Fatalf("ParseKeyColorTable: %v", err)
	}
	if got != table {
		t.Errorf("simulator's stored key colour table does not match what was written")
	}

	settings, err := dev.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if settings.Effect() != protocol.EffectCustom {
		t.Errorf("Effect() = 0x%02x, want EffectCustom (0x%02x)", settings.Effect(), protocol.EffectCustom)
	}
}

func TestSimUploadScreen(t *testing.T) {
	dev := openSimTarget(t, true)

	frame0 := make([]byte, protocol.FrameBytes)
	frame1 := make([]byte, protocol.FrameBytes)
	for i := range frame0 {
		frame0[i] = byte(i)
		frame1[i] = byte(255 - i)
	}

	if err := dev.UploadScreen(context.Background(), [][]byte{frame0, frame1}, 200, nil); err != nil {
		t.Fatalf("UploadScreen: %v", err)
	}

	sim, ok := dev.h.(*simTransport)
	if !ok {
		t.Fatalf("device is not backed by the simulator")
	}
	sim.mu.Lock()
	screen := append([]byte(nil), sim.screen...)
	sim.mu.Unlock()

	check := func(name string, slot int, want []byte) {
		start := slot * protocol.FrameSlot
		if start+len(want) > len(screen) {
			t.Fatalf("%s: uploaded screen buffer too short (%d bytes)", name, len(screen))
		}
		got := screen[start : start+len(want)]
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: byte %d = 0x%02x, want 0x%02x", name, i, got[i], want[i])
			}
		}
	}
	check("frame0", 0, frame0)
	check("frame1", 1, frame1)

	settings, err := dev.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if settings.FrameCount() != 2 {
		t.Errorf("FrameCount() = %d, want 2", settings.FrameCount())
	}
	if settings.FrameIntervalMS() != 200 {
		t.Errorf("FrameIntervalMS() = %d, want 200", settings.FrameIntervalMS())
	}
}

func TestSimUploadScreenWirelessRejected(t *testing.T) {
	dev := openSimTarget(t, false)
	frame := make([]byte, protocol.FrameBytes)
	if err := dev.UploadScreen(context.Background(), [][]byte{frame}, 100, nil); err == nil {
		t.Fatalf("UploadScreen over the simulated wireless receiver should be rejected")
	}
}

func TestSimFirmwareMismatch(t *testing.T) {
	t.Setenv("K724_SIM_KB_VERSION", "0100")
	dev := openSimTarget(t, true)

	fw := dev.Firmware()
	if !fw.Known {
		t.Fatalf("Firmware().Known = false, want true")
	}
	if fw.OK() {
		t.Errorf("Firmware().OK() = true, want false with a spoofed KB version")
	}
	if fw.Warning() == "" {
		t.Errorf("Firmware().Warning() is empty, want a mismatch message")
	}
}

func TestSimFirmwareDefaultMatches(t *testing.T) {
	dev := openSimTarget(t, true)
	fw := dev.Firmware()
	if !fw.OK() {
		t.Errorf("Firmware().OK() = false with default sim versions, want true: %+v", fw)
	}
}

func TestSimBatteryDefault(t *testing.T) {
	dev := openSimTarget(t, true)
	b, err := dev.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if b.Percent != 100 {
		t.Errorf("Battery().Percent = %d, want 100 (default)", b.Percent)
	}
}

func TestSimBatteryOverride(t *testing.T) {
	t.Setenv("K724_SIM_BATTERY", "42")
	dev := openSimTarget(t, false)
	b, err := dev.Battery()
	if err != nil {
		t.Fatalf("Battery: %v", err)
	}
	if b.Percent != 42 {
		t.Errorf("Battery().Percent = %d, want 42", b.Percent)
	}
}
