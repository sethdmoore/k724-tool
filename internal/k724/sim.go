package k724

// The simulator: an in-process stand-in for the keyboard's vendor HID
// endpoint, so the rest of this package — probe, the settings
// read-modify-write, the per-key colour table, the screen upload sequence —
// can be exercised with no hardware attached at all. It implements
// hidHandle, so Device drives it through the exact same Transact/RunSteps
// code every real command goes through; only the transport underneath
// changes.
//
// It is not a firmware emulator: it does not model timing quirks, dropped
// reports, or undecoded fields. It answers each command the way the
// documented byte offsets say the real keyboard would, which is enough to
// develop and unit-test client-side logic (GUI/CLI features, settings
// round-tripping, upload chunking) without a physical unit.
//
// Enable with the K724_SIM environment variable; see SimulationEnabled.

import (
	"encoding/hex"
	"os"
	"strconv"
	"sync"
	"time"

	hid "github.com/sstallion/go-hid"

	"k724tool/internal/applog"
	"k724tool/internal/protocol"
)

const (
	simPathWired    = "sim://wired"
	simPathWireless = "sim://wireless"
)

// SimulationEnabled reports whether the K724_SIM environment variable
// requests the built-in virtual keyboard. When set, Enumerate appends a
// simulated wired and wireless Target, and Open recognises their sim://
// path and hands back a Device backed by the in-process responder below
// instead of hidapi.
func SimulationEnabled() bool {
	return os.Getenv("K724_SIM") != ""
}

func isSimPath(path string) bool {
	return path == simPathWired || path == simPathWireless
}

func simTargets() []Target {
	return []Target{
		{
			Path:    simPathWired,
			Product: "Simulated Gaming KB",
			VID:     protocol.VendorID,
			PID:     protocol.ProductIDWired,
			Iface:   -1,
			Wired:   true,
		},
		{
			Path:    simPathWireless,
			Product: "Simulated 2.4G Wireless Receiver",
			VID:     protocol.VendorID,
			PID:     protocol.ProductIDWireless,
			Iface:   -1,
			Wired:   false,
		},
	}
}

func openSim(t Target) (*Device, error) {
	applog.Infof("openSim %s: starting virtual keyboard", t.Path)
	return attach(t, newSimTransport(t), t.Path)
}

// descriptorTemplate is the command 0x03 reply body captured in
// docs/COMMANDS.md / internal/protocol/descriptor.go, byte-for-byte except
// for the VID/PID and firmware-version fields, which simTransport patches
// per target. Keeping the rest of the captured bytes verbatim means the
// simulator answers with a real (if only partly decoded) reply shape rather
// than a made-up one.
var descriptorTemplate = mustHexBytes(
	"aa5500000380180002" +
		"000000000000000000000000" +
		"0f321b51" +
		"000100" +
		"060206" +
		"f08703125500")

func mustHexBytes(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// simTransport is an in-memory stand-in for the keyboard's command channel.
// It satisfies hidHandle. Every field it mutates is guarded by mu because
// Write (called from the caller's goroutine) and ReadWithTimeout race on
// nothing but replyCh, which is itself synchronised — mu only protects the
// simulated device state.
type simTransport struct {
	target Target

	mu        sync.Mutex
	settings  protocol.SettingsBlock
	keyColors [protocol.KeyColorTableLen]byte
	screen    []byte
	vid, pid  uint16
	kbVersion uint16
	apVersion uint16
	battery   int

	replyCh chan []byte
}

func newSimTransport(t Target) *simTransport {
	s := &simTransport{
		target:    t,
		vid:       t.VID,
		pid:       t.PID,
		kbVersion: simVersionOverride("K724_SIM_KB_VERSION", protocol.SupportedKBVersion),
		apVersion: simVersionOverride("K724_SIM_AP_VERSION", protocol.SupportedAPVersion),
		battery:   simBatteryOverride(),
		replyCh:   make(chan []byte, 8),
	}
	// A plausible factory-default settings block, not a captured one: some
	// preset effect, mid brightness/speed, full USB polling, one screen
	// frame, the real clock. Every field is independently overwritable by a
	// test or by the GUI's own read-modify-write.
	s.settings.SetEffect(1)
	s.settings.SetBrightness(3)
	s.settings.SetSpeed(3)
	s.settings.SetColor(0, 255, 0)
	s.settings.SetPollingIndex(0)
	s.settings.SetFrameCount(1)
	s.settings.SetFrameIntervalMS(100)
	s.settings.SetTime(time.Now())
	return s
}

// simVersionOverride lets a test (or a developer poking at the firmware
// mismatch banner) simulate a keyboard on a different firmware revision
// without touching code, e.g. K724_SIM_KB_VERSION=0100.
func simVersionOverride(envVar string, def uint16) uint16 {
	v := os.Getenv(envVar)
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 16, 16)
	if err != nil {
		applog.Warnf("sim: %s=%q is not 16-bit hex, using default", envVar, v)
		return def
	}
	return uint16(n)
}

// simBatteryOverride lets a test (or a developer poking at the GUI's battery
// display) simulate a specific charge level with K724_SIM_BATTERY=42.
// Defaults to 100, matching the one captured 0x1A reply this tool has ever
// seen (see internal/protocol/battery.go).
func simBatteryOverride() int {
	v := os.Getenv("K724_SIM_BATTERY")
	if v == "" {
		return 100
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 100 {
		applog.Warnf("sim: K724_SIM_BATTERY=%q is not 0..100, using 100", v)
		return 100
	}
	return n
}

// Write decodes one 64-byte HID report the same way the firmware would and
// queues the matching reply for ReadWithTimeout.
func (s *simTransport) Write(p []byte) (int, error) {
	if len(p) < protocol.HeaderSize {
		return 0, hid.ErrTimeout // malformed report; no real device sends one
	}
	cmd := p[3]
	length := int(p[4])
	if length > protocol.MaxChunkLen {
		length = protocol.MaxChunkLen
	}
	var offset uint32
	if cmd == protocol.CmdScreen {
		offset = uint32(p[5]) | uint32(p[6])<<8 | uint32(p[7])<<16
	} else {
		offset = uint32(p[5]) | uint32(p[6])<<8
	}
	body := make([]byte, length)
	if end := protocol.HeaderSize + length; end <= len(p) {
		copy(body, p[protocol.HeaderSize:end])
	}

	s.mu.Lock()
	reply := s.handleLocked(cmd, offset, body, length)
	s.mu.Unlock()

	s.replyCh <- reply
	return len(p), nil
}

// handleLocked builds the reply frame for one decoded request. mu is held.
func (s *simTransport) handleLocked(cmd byte, offset uint32, body []byte, length int) []byte {
	switch cmd {
	case protocol.CmdPing, protocol.Cmd01, protocol.CmdCommit, protocol.CmdBeginBulk:
		return protocol.BuildReport(cmd, 0, nil)

	case protocol.CmdDescriptor:
		return protocol.BuildReport(cmd, 0, s.descriptorBodyLocked())

	case protocol.CmdReadAt:
		return protocol.BuildReport(cmd, uint16(offset), s.readSettingsLocked(int(offset), length))

	case protocol.CmdWriteAt:
		s.writeSettingsLocked(int(offset), body)
		return protocol.BuildReport(cmd, uint16(offset), nil)

	case protocol.CmdKeyColors:
		s.writeKeyColorsLocked(int(offset), body)
		return protocol.BuildReport(cmd, uint16(offset), nil)

	case protocol.CmdScreen:
		s.writeScreenLocked(offset, body)
		return protocol.BuildReportWide(cmd, offset, nil)

	case protocol.CmdBattery:
		return protocol.BuildReport(cmd, 0, s.batteryBodyLocked())

	default:
		applog.Warnf("sim: unhandled command 0x%02x, acking with an empty reply", cmd)
		return protocol.BuildReport(cmd, 0, nil)
	}
}

func (s *simTransport) descriptorBodyLocked() []byte {
	b := append([]byte(nil), descriptorTemplate...)
	b[21], b[22] = byte(s.vid), byte(s.vid>>8)
	b[23], b[24] = byte(s.pid), byte(s.pid>>8)
	b[26], b[27] = byte(s.apVersion>>8), byte(s.apVersion)
	b[29], b[30] = byte(s.kbVersion>>8), byte(s.kbVersion)
	return b
}

// batteryBodyLocked builds a command 0x1A reply body: percent, then the
// device-major-version byte the one real capture showed (AP=01, KB=02),
// then four zero bytes, matching the observed "64 01 00 00 00 00" /
// "64 02 00 00 00 00" shape (see internal/protocol/battery.go).
func (s *simTransport) batteryBodyLocked() []byte {
	major := byte(1)
	if s.target.Wired {
		major = 2
	}
	return []byte{byte(s.battery), major, 0, 0, 0, 0}
}

func (s *simTransport) readSettingsLocked(offset, length int) []byte {
	out := make([]byte, length)
	for i := 0; i < length; i++ {
		if p := offset + i; p >= 0 && p < len(s.settings.Raw) {
			out[i] = s.settings.Raw[p]
		}
	}
	return out
}

func (s *simTransport) writeSettingsLocked(offset int, body []byte) {
	for i, v := range body {
		if p := offset + i; p >= 0 && p < len(s.settings.Raw) {
			s.settings.Raw[p] = v
		}
	}
}

func (s *simTransport) writeKeyColorsLocked(offset int, body []byte) {
	for i, v := range body {
		if p := offset + i; p >= 0 && p < len(s.keyColors) {
			s.keyColors[p] = v
		}
	}
}

func (s *simTransport) writeScreenLocked(offset uint32, body []byte) {
	need := int(offset) + len(body)
	if need > len(s.screen) {
		grown := make([]byte, need)
		copy(grown, s.screen)
		s.screen = grown
	}
	copy(s.screen[offset:], body)
}

// ReadWithTimeout returns the reply queued by the most recent Write, or
// hid.ErrTimeout if none arrives within timeout — the same sentinel the real
// hidapi handle returns, so Transact's retry/timeout logic is unchanged.
func (s *simTransport) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	select {
	case reply := <-s.replyCh:
		return copy(p, reply), nil
	case <-time.After(timeout):
		return 0, hid.ErrTimeout
	}
}

// Close is a no-op; the simulator holds no OS resources.
func (s *simTransport) Close() error { return nil }
