package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

// observedBlock is the 49-byte settings block read from a wired K724-RGB-PRO
// (320f:511b) with command 0x05 in captures/open_redragon.pcapng.
const observedBlockHex = "0001040200001efef706000000b400ff00ff0000ff00000000000000000000ff0002012647130528082600640000000102"

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

func TestParseSettingsAccessors(t *testing.T) {
	s, err := ParseSettings(mustDecodeHex(t, observedBlockHex))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}

	if got := s.Effect(); got != 0x01 {
		t.Errorf("Effect = 0x%02x, want 0x01", got)
	}
	if got := s.Brightness(); got != 4 {
		t.Errorf("Brightness = %d, want 4", got)
	}
	if got := s.Speed(); got != 2 {
		t.Errorf("Speed = %d, want 2", got)
	}
	if r, g, b := s.Color(); r != 0x1e || g != 0xfe || b != 0xf7 {
		t.Errorf("Color = %02x %02x %02x, want 1e fe f7", r, g, b)
	}
	if got := s.PollingIndex(); got != 0 {
		t.Errorf("PollingIndex = %d, want 0", got)
	}
	if got := s.PollingHz(); got != 1000 {
		t.Errorf("PollingHz = %d, want 1000", got)
	}
	if got := s.FrameCount(); got != 1 {
		t.Errorf("FrameCount = %d, want 1", got)
	}
	if got := s.FrameIntervalMS(); got != 100 {
		t.Errorf("FrameIntervalMS = %d, want 100", got)
	}

	got := s.Time(time.UTC)
	want := time.Date(2026, 8, 28, 13, 47, 26, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Time = %s, want %s", got, want)
	}
}

func TestPollingIndexStepsMatchCaptures(t *testing.T) {
	// captures/change_polling_1000-500-250-125.pcapng stepped byte 22
	// 00 -> 01 -> 02 -> 03 as the rate went 1000 -> 500 -> 250 -> 125.
	for idx, hz := range PollingRates {
		var s SettingsBlock
		s.SetPollingIndex(idx)
		if s.Raw[offPollingIndex] != byte(idx) {
			t.Errorf("SetPollingIndex(%d) wrote byte %d", idx, s.Raw[offPollingIndex])
		}
		if s.PollingHz() != hz {
			t.Errorf("index %d -> %d Hz, want %d", idx, s.PollingHz(), hz)
		}
	}
}

func TestSetTimeBCD(t *testing.T) {
	var s SettingsBlock
	// Friday 2026-08-28 13:48:18 -> weekday 5.
	s.SetTime(time.Date(2026, 8, 28, 13, 48, 18, 0, time.UTC))
	want := []byte{0x18, 0x48, 0x13, 0x05, 0x28, 0x08, 0x26}
	if got := s.Raw[offTime : offTime+7]; !bytes.Equal(got, want) {
		t.Errorf("time field = % x, want % x", got, want)
	}
}

func TestSetTimeIsTheOnlyChange(t *testing.T) {
	// The safe wired clock-set: read the block, stamp a new time, write it
	// back with every other byte preserved.
	orig, err := ParseSettings(mustDecodeHex(t, observedBlockHex))
	if err != nil {
		t.Fatal(err)
	}
	got := orig
	got.SetTime(time.Date(2026, 8, 28, 13, 48, 18, 0, time.UTC))

	for i := range got.Raw {
		inTime := i >= offTime && i < offTime+7
		if !inTime && got.Raw[i] != orig.Raw[i] {
			t.Errorf("byte %d changed (%02x -> %02x) outside the time field", i, orig.Raw[i], got.Raw[i])
		}
	}
}

func TestSettingsWriteStepsShape(t *testing.T) {
	s, _ := ParseSettings(mustDecodeHex(t, observedBlockHex))
	steps := SettingsWriteSteps(s)
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(steps))
	}
	if steps[0].Cmd != Cmd01 || steps[1].Cmd != CmdWriteAt || steps[2].Cmd != CmdCommit {
		t.Fatalf("step cmds = %02x %02x %02x", steps[0].Cmd, steps[1].Cmd, steps[2].Cmd)
	}
	if len(steps[1].Chunk) != SettingsBlockLen {
		t.Fatalf("write chunk = %d bytes, want %d", len(steps[1].Chunk), SettingsBlockLen)
	}
}

func TestSettingsReadStepsShape(t *testing.T) {
	steps := SettingsReadSteps()
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	// The read must be bare: no leading 0x01, or the wired keyboard is left
	// mid-transaction and whites out every key (see SettingsReadSteps).
	if steps[0].Cmd != CmdReadAt || len(steps[0].Chunk) != SettingsBlockLen {
		t.Fatalf("read step = cmd %02x, chunk %d bytes", steps[0].Cmd, len(steps[0].Chunk))
	}
}

func TestParseSettingsReply(t *testing.T) {
	block := mustDecodeHex(t, observedBlockHex)
	reply := make([]byte, ReportSize)
	reply[0] = reportMarker
	reply[3] = CmdReadAt
	reply[4] = SettingsBlockLen
	copy(reply[HeaderSize:], block)

	s, err := ParseSettingsReply(reply)
	if err != nil {
		t.Fatalf("ParseSettingsReply: %v", err)
	}
	if !bytes.Equal(s.Raw[:], block) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestSettingsWriteChecksumMatchesCapture(t *testing.T) {
	// captures/open_redragon.pcapng: the 0x06 settings write reported
	// checksum 0x083f for this exact 49-byte body.
	body := mustDecodeHex(t, "0001040200001efef706000000b400ff00ff0000ff00000000000000000000ff0002011848130528082600640000000102")
	s, _ := ParseSettings(body)
	report := SettingsWriteSteps(s)[1].Report()
	if got := uint16(report[1]) | uint16(report[2])<<8; got != 0x083f {
		t.Errorf("checksum = 0x%04x, want 0x083f", got)
	}
}
