package protocol

import (
	"errors"
	"fmt"
	"time"
)

// SettingsBlockLen is the length of the global settings block: the 49-byte
// buffer at device offset 0 that command 0x06 writes and command 0x05 reads.
// The RTC clock, lighting preset, brightness, effect speed, custom colour,
// USB polling rate, and the screen frame count / interval are all fields in
// this one block. See docs/PROTOCOL.md "Settings block layout".
const SettingsBlockLen = 49

// Field offsets within the settings block, confirmed by one-variable-at-a-time
// diffs across the session-4 wired captures (docs/PROTOCOL.md).
const (
	offEffect       = 1 // lighting effect / preset index
	offBrightness   = 2 // 0..5
	offSpeed        = 3 // 0..5
	offColorR       = 6 // custom colour, 24-bit RGB at 6..8
	offColorG       = 7
	offColorB       = 8
	offKeySwap      = 19 // "Exchange key": WASD <-> arrow cluster, 1 on / 0 off
	offNKRO         = 20 // N-key rollover, 1 on (NKRO) / 0 off (6-key)
	offWinLock      = 21 // Windows / Super key lock, 1 on / 0 off
	offPollingIndex = 22 // 0..3 = 1000/500/250/125 Hz
	offFrameCount   = 34 // screen animation frame count, 1..25
	offTime         = 35 // BCD SS MM HH WD DD MM YY at 35..41
	// Screen frame interval in ms: 16-bit little-endian at bytes 43..44.
	// write_light_a-r_s-g_d-b_q-w_e-bk.pcapng carries 0xC350 (50000) here,
	// matching the Windows app's "Interval time 50000 ms" field; the earlier
	// captures only ever set the low byte (0x64 = 100, 0xC8 = 200), which is
	// why docs/SCREEN.md first read it as a single byte.
	offFrameInterval = 43
)

// FrameIntervalMax is the largest screen frame interval this tool will send.
// The field is 16-bit; 50000 ms is the largest value seen from the Windows app.
const FrameIntervalMax = 50000

// PollingRates maps a polling-index (settings byte 22) to its rate in Hz.
var PollingRates = [4]int{1000, 500, 250, 125}

// SettingsBlock is the 49-byte global settings block. Read one from the device
// with SettingsReadSteps + ParseSettingsReply, change one or more fields
// through the setters, then write it back with SettingsWriteSteps. Every
// setter leaves the other bytes untouched, which is what makes a wired write
// safe (docs/PROTOCOL.md "the README's white-RGB bug").
type SettingsBlock struct {
	Raw [SettingsBlockLen]byte
}

// ParseSettings copies b into a SettingsBlock. b must be exactly
// SettingsBlockLen bytes.
func ParseSettings(b []byte) (SettingsBlock, error) {
	var s SettingsBlock
	if len(b) != SettingsBlockLen {
		return s, fmt.Errorf("protocol: settings block is %d bytes, want %d", len(b), SettingsBlockLen)
	}
	copy(s.Raw[:], b)
	return s, nil
}

// ParseSettingsReply extracts the settings block from a raw 64-byte reply to a
// command 0x05 read of SettingsBlockLen bytes at offset 0.
func ParseSettingsReply(reply []byte) (SettingsBlock, error) {
	if !ReplyOK(reply, CmdReadAt) {
		return SettingsBlock{}, errors.New("protocol: not a 0x05 reply")
	}
	if len(reply) < HeaderSize+SettingsBlockLen {
		return SettingsBlock{}, fmt.Errorf("protocol: 0x05 reply is %d bytes, need at least %d", len(reply), HeaderSize+SettingsBlockLen)
	}
	return ParseSettings(reply[HeaderSize : HeaderSize+SettingsBlockLen])
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Effect returns the lighting effect / preset index (settings byte 1).
func (s *SettingsBlock) Effect() byte { return s.Raw[offEffect] }

// SetEffect sets the lighting effect / preset index.
func (s *SettingsBlock) SetEffect(v byte) { s.Raw[offEffect] = v }

// Brightness returns the brightness level, 0..5.
func (s *SettingsBlock) Brightness() int { return int(s.Raw[offBrightness]) }

// SetBrightness sets the brightness level; v is clamped to 0..5.
func (s *SettingsBlock) SetBrightness(v int) { s.Raw[offBrightness] = byte(clampInt(v, 0, 5)) }

// Speed returns the effect speed, 0..5.
func (s *SettingsBlock) Speed() int { return int(s.Raw[offSpeed]) }

// SetSpeed sets the effect speed; v is clamped to 0..5.
func (s *SettingsBlock) SetSpeed(v int) { s.Raw[offSpeed] = byte(clampInt(v, 0, 5)) }

// Color returns the custom colour (settings bytes 6-8), full 0..255 per
// channel.
func (s *SettingsBlock) Color() (r, g, b byte) {
	return s.Raw[offColorR], s.Raw[offColorG], s.Raw[offColorB]
}

// SetColor sets the custom colour.
func (s *SettingsBlock) SetColor(r, g, b byte) {
	s.Raw[offColorR], s.Raw[offColorG], s.Raw[offColorB] = r, g, b
}

// KeySwap reports whether the WASD / arrow-cluster swap ("Exchange key") is on.
func (s *SettingsBlock) KeySwap() bool { return s.Raw[offKeySwap] != 0 }

// SetKeySwap turns the WASD / arrow-cluster swap on or off.
func (s *SettingsBlock) SetKeySwap(on bool) { s.Raw[offKeySwap] = boolByte(on) }

// NKRO reports whether N-key rollover is on (off means 6-key rollover).
func (s *SettingsBlock) NKRO() bool { return s.Raw[offNKRO] != 0 }

// SetNKRO turns N-key rollover on or off.
func (s *SettingsBlock) SetNKRO(on bool) { s.Raw[offNKRO] = boolByte(on) }

// WinLock reports whether the Windows / Super key lock is on.
func (s *SettingsBlock) WinLock() bool { return s.Raw[offWinLock] != 0 }

// SetWinLock turns the Windows / Super key lock on or off.
func (s *SettingsBlock) SetWinLock(on bool) { s.Raw[offWinLock] = boolByte(on) }

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// PollingIndex returns the USB polling-rate index, 0..3.
func (s *SettingsBlock) PollingIndex() int { return int(s.Raw[offPollingIndex]) }

// SetPollingIndex sets the USB polling-rate index; v is clamped to 0..3.
// Index 0..3 maps to 1000/500/250/125 Hz (PollingRates).
func (s *SettingsBlock) SetPollingIndex(v int) { s.Raw[offPollingIndex] = byte(clampInt(v, 0, 3)) }

// PollingHz returns the USB polling rate in Hz.
func (s *SettingsBlock) PollingHz() int { return PollingRates[clampInt(s.PollingIndex(), 0, 3)] }

// FrameCount returns the screen animation frame count.
func (s *SettingsBlock) FrameCount() int { return int(s.Raw[offFrameCount]) }

// SetFrameCount sets the screen animation frame count; v is clamped to 1..25.
func (s *SettingsBlock) SetFrameCount(v int) { s.Raw[offFrameCount] = byte(clampInt(v, 1, 25)) }

// FrameIntervalMS returns the screen frame interval in milliseconds. The field
// is a 16-bit little-endian value at bytes 43..44.
func (s *SettingsBlock) FrameIntervalMS() int {
	return int(s.Raw[offFrameInterval]) | int(s.Raw[offFrameInterval+1])<<8
}

// SetFrameIntervalMS sets the screen frame interval; v is clamped to
// 1..FrameIntervalMax and written little-endian across bytes 43..44.
func (s *SettingsBlock) SetFrameIntervalMS(v int) {
	v = clampInt(v, 1, FrameIntervalMax)
	s.Raw[offFrameInterval] = byte(v)
	s.Raw[offFrameInterval+1] = byte(v >> 8)
}

// Time decodes the BCD timestamp (settings bytes 35-41) into a time.Time in
// loc. The year field is two digits; it is taken as 2000-2099.
func (s *SettingsBlock) Time(loc *time.Location) time.Time {
	d := func(b byte) int { return int(b>>4)*10 + int(b&0x0f) }
	sec := d(s.Raw[offTime+0])
	min := d(s.Raw[offTime+1])
	hour := d(s.Raw[offTime+2])
	day := d(s.Raw[offTime+4])
	mon := d(s.Raw[offTime+5])
	year := 2000 + d(s.Raw[offTime+6])
	return time.Date(year, time.Month(mon), day, hour, min, sec, 0, loc)
}

// SetTime stamps t into the BCD timestamp field (settings bytes 35-41):
// SS MM HH WD DD MM YY, weekday 0=Sunday..6=Saturday. Go's time.Weekday
// already uses that convention.
func (s *SettingsBlock) SetTime(t time.Time) {
	s.Raw[offTime+0] = BCD(t.Second())
	s.Raw[offTime+1] = BCD(t.Minute())
	s.Raw[offTime+2] = BCD(t.Hour())
	s.Raw[offTime+3] = BCD(int(t.Weekday()))
	s.Raw[offTime+4] = BCD(t.Day())
	s.Raw[offTime+5] = BCD(int(t.Month()))
	s.Raw[offTime+6] = BCD(t.Year() % 100)
}

// Summary is a one-line human-readable decode of the fields the GUI exposes,
// for logging. It does not include the RTC time.
func (s *SettingsBlock) Summary() string {
	r, g, b := s.Color()
	return fmt.Sprintf("effect=%d brightness=%d speed=%d colour=%02x%02x%02x polling=%d(%dHz) "+
		"nkro=%t winlock=%t keyswap=%t frames=%d interval=%dms",
		s.Effect(), s.Brightness(), s.Speed(), r, g, b,
		s.PollingIndex(), s.PollingHz(),
		s.NKRO(), s.WinLock(), s.KeySwap(),
		s.FrameCount(), s.FrameIntervalMS())
}

// SettingsReadSteps returns the step sequence that reads the settings block:
// a single 0x05 read of SettingsBlockLen bytes at offset 0. Feed the reply
// from the final step to ParseSettingsReply.
//
// The read is issued bare, with no 0x01 in front of it. In open_redragon.pcapng
// the Windows app reads the block that way — 0x01/0x02 bracket only the 0x06
// write. A leading 0x01 (per docs/COMMANDS.md, "sent right before a write
// batch") opens a transaction the read path never commits; left open on the
// wired keyboard, the LED controller drops every key to white until a power
// cycle. See docs/PROTOCOL.md.
func SettingsReadSteps() []Step {
	return []Step{
		{Cmd: CmdReadAt, Offset: 0, Chunk: make([]byte, SettingsBlockLen)},
	}
}

// SettingsWriteSteps returns the step sequence that writes b back to the
// device: a 0x01, a single 0x06 chunk carrying all 49 bytes at offset 0, and
// a 0x02 commit. The session-4 wired captures use exactly this single-chunk
// form.
func SettingsWriteSteps(b SettingsBlock) []Step {
	chunk := make([]byte, SettingsBlockLen)
	copy(chunk, b.Raw[:])
	return []Step{
		{Cmd: Cmd01},
		{Cmd: CmdWriteAt, Offset: 0, Chunk: chunk},
		{Cmd: CmdCommit},
	}
}
