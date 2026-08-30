// Package k724 is the device layer: it enumerates the keyboard's vendor HID
// interface, opens it, and runs protocol step sequences against it. This is
// the only package in the tree that uses cgo (through go-hid) and touches
// hardware; internal/protocol and internal/screen stay pure.
package k724

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	hid "github.com/sstallion/go-hid"

	"k724tool/internal/applog"
	"k724tool/internal/protocol"
)

// transactDeadline is the total time Transact will wait for a matching reply,
// across however many stale/unsolicited reports it has to drain first. The
// keyboard normally answers in about a millisecond. pingDeadline is the
// shorter budget used when probing an interface during Open.
const (
	transactDeadline = 2 * time.Second
	pingDeadline     = 600 * time.Millisecond
)

// beginBulkDeadline is the reply budget for the 0x23 "begin bulk image
// transfer" marker only. The keyboard prepares (erases) its screen-flash
// region on 0x23 and is slow to answer: captures/screen2.pcapng shows a
// 2.406 s gap between the 0x23 request and its reply, well past
// transactDeadline. 0x23 is also NOT resent on timeout (retries=0): a second
// begin-bulk pushed into the pipe while the first is still being processed
// desyncs every following 0x21 ACK. See Transact.
const beginBulkDeadline = 10 * time.Second

// drainTimeout is the per-read wait while flushing a device's input queue
// before the first command; maxDrain caps how many reports that flush eats.
const (
	drainTimeout = 25 * time.Millisecond
	maxDrain     = 64
)

// writeRetries is how many times Transact re-sends a report after a clean
// timeout (no reply at all). Every command in this protocol is addressed by an
// explicit offset or is an idempotent marker, so a resend is safe.
const writeRetries = 1

// commitDelay is a pause before every 0x02 commit. The Windows app leaves
// ~20 ms between the 0x06 settings write and its 0x02 (visible in
// captures/light_presets.pcapng, and noted in docs/COMMANDS.md as a "10 ms
// delay before send"). Firing the commit immediately after the write's reply
// is a suspected cause of the wired keyboard latching a half-applied block.
const commitDelay = 15 * time.Millisecond

// Init and Exit bracket all hidapi use. Call Init once at startup and Exit at
// shutdown.
func Init() error { return hid.Init() }
func Exit() error { return hid.Exit() }

// Target identifies one connected keyboard or receiver.
type Target struct {
	Path    string
	Product string
	VID     uint16
	PID     uint16
	Iface   int
	Wired   bool
}

// Label is a short human-readable name for the target.
func (t Target) Label() string {
	if t.Wired {
		return "Wired keyboard"
	}
	return "Wireless receiver"
}

// Enumerate returns every K724-RGB-PRO vendor interface currently attached,
// wired or wireless. Standard keyboard/gamepad interfaces are filtered out.
func Enumerate() ([]Target, error) {
	seen := map[string]bool{}
	var out []Target
	err := hid.Enumerate(protocol.VendorID, hid.ProductIDAny, func(info *hid.DeviceInfo) error {
		if !protocol.IsVendorUsagePage(info.UsagePage) {
			return nil
		}
		if info.ProductID != protocol.ProductIDWired && info.ProductID != protocol.ProductIDWireless {
			return nil
		}
		if seen[info.Path] {
			return nil
		}
		seen[info.Path] = true
		out = append(out, Target{
			Path:    info.Path,
			Product: info.ProductStr,
			VID:     info.VendorID,
			PID:     info.ProductID,
			Iface:   info.InterfaceNbr,
			Wired:   info.ProductID == protocol.ProductIDWired,
		})
		return nil
	})
	if err != nil {
		applog.Errorf("enumerate: %v", err)
		return out, err
	}
	applog.Infof("enumerate: %d vendor interface(s)", len(out))
	for i, t := range out {
		applog.Infof("  [%d] %s  %04x:%04x iface=%d  %q  path=%s",
			i, t.Label(), t.VID, t.PID, t.Iface, t.Product, t.Path)
	}
	return out, nil
}

// Device is an open, ping-confirmed connection to the vendor command channel.
type Device struct {
	h      *hid.Device
	target Target

	// descriptor is the parsed 0x03 reply from probe(). Only the wired open
	// sequence issues 0x03, so hasDescriptor is false for the receiver.
	descriptor    protocol.DescriptorBlock
	hasDescriptor bool
}

// Target returns the target this device was opened for.
func (d *Device) Target() Target { return d.target }

// Firmware compares the connected unit's reported firmware versions against
// the versions this tool's protocol was reverse-engineered on.
type Firmware struct {
	Known       bool   // the 0x03 descriptor carried a firmware block (wired only)
	KBVersion   uint16 // keyboard firmware, e.g. 0x0206 for "V0206"
	APVersion   uint16 // 2.4 GHz receiver firmware, e.g. 0x0100
	KBSupported bool   // KBVersion == protocol.SupportedKBVersion
	APSupported bool   // APVersion == protocol.SupportedAPVersion
}

// OK reports whether every version the descriptor carried matches the version
// this tool targets. It is false when no firmware block was read.
func (f Firmware) OK() bool { return f.Known && f.KBSupported && f.APSupported }

// Warning returns a one-line ⚠️ message when the connected unit's firmware
// differs from what this tool was built for, or "" when it matches or when no
// firmware block was read (the wireless receiver).
func (f Firmware) Warning() string {
	if f.OK() || !f.Known {
		return ""
	}
	return fmt.Sprintf(
		"⚠️  Firmware mismatch: this unit reports KB %s / AP %s, but this tool was reverse-engineered on KB %s / AP %s. It may not work as expected.",
		protocol.FormatVersion(f.KBVersion), protocol.FormatVersion(f.APVersion),
		protocol.FormatVersion(protocol.SupportedKBVersion), protocol.FormatVersion(protocol.SupportedAPVersion),
	)
}

// Firmware returns the firmware comparison for the open device. Known is
// false unless the wired open sequence read a 0x03 descriptor block.
func (d *Device) Firmware() Firmware {
	if !d.hasDescriptor {
		return Firmware{}
	}
	return Firmware{
		Known:       true,
		KBVersion:   d.descriptor.KBVersion(),
		APVersion:   d.descriptor.APVersion(),
		KBSupported: d.descriptor.KBVersion() == protocol.SupportedKBVersion,
		APSupported: d.descriptor.APVersion() == protocol.SupportedAPVersion,
	}
}

// Open opens t's vendor interface. It tries t.Path first, then scans t's
// VID/PID for a vendor interface that answers the connection probe (0xAA for
// the wireless receiver, 0x01 + a 0x03 descriptor read for the wired keyboard
// — see probe).
func Open(t Target) (*Device, error) {
	applog.Infof("open: %s  %04x:%04x  path=%s", t.Label(), t.VID, t.PID, t.Path)

	if t.Path != "" {
		if d, err := openPath(t, t.Path); err != nil {
			// A permission error is worth surfacing verbatim; keep it.
			if IsPermissionError(err) {
				return nil, err
			}
			applog.Warnf("open: stored path failed, scanning %04x:%04x instead", t.VID, t.PID)
		} else if d != nil {
			return d, nil
		}
	}

	var found *Device
	var permErr error
	tried := 0
	err := hid.Enumerate(t.VID, t.PID, func(info *hid.DeviceInfo) error {
		if found != nil || !protocol.IsVendorUsagePage(info.UsagePage) {
			return nil
		}
		tried++
		d, e := openPath(t, info.Path)
		switch {
		case d != nil:
			found = d
		case e != nil && IsPermissionError(e):
			permErr = e
		}
		return nil
	})
	if err != nil {
		applog.Errorf("open: enumerate %04x:%04x: %v", t.VID, t.PID, err)
		return nil, err
	}
	if found != nil {
		return found, nil
	}
	if permErr != nil {
		return nil, permErr
	}
	applog.Errorf("open: no vendor interface answered the probe (%d candidate path(s) tried)", tried)
	return nil, fmt.Errorf("no HID interface for the %s answered the connection probe", strings.ToLower(t.Label()))
}

func openPath(t Target, path string) (*Device, error) {
	h, err := hid.OpenPath(path)
	if err != nil {
		if IsPermissionError(err) {
			applog.Errorf("openPath %s: permission denied", path)
		} else {
			applog.Warnf("openPath %s: %v", path, err)
		}
		return nil, err
	}
	d := &Device{h: h, target: t}
	if n := d.drain(); n > 0 {
		applog.Infof("openPath %s: flushed %d stale report(s) before probe", path, n)
	}
	if err := d.probe(); err != nil {
		applog.Warnf("openPath %s: probe failed: %v", path, err)
		h.Close()
		return nil, err
	}
	applog.Infof("openPath %s: probe OK", path)
	return d, nil
}

// probe confirms path leads to the vendor command channel.
//
// The wireless receiver answers a 0xAA ping — that is the sequence the
// session-3 wireless capture used, and it is confirmed working against the
// real receiver.
//
// The wired keyboard must NOT be pinged. Every wired capture
// (../captures/*.pcapng) opens with 0x01 then a 0x03 descriptor read and
// never sends 0xAA at all; sending 0xAA to the wired keyboard drops its
// onboard lighting to solid white until a power cycle, and no later settings
// write clears it. So the wired probe mirrors the capture: 0x01, then a 0x03
// descriptor read whose reply must embed the keyboard's VID/PID.
func (d *Device) probe() error {
	if !d.target.Wired {
		_, err := d.transact(protocol.Step{Cmd: protocol.CmdPing}, pingDeadline, 0)
		return err
	}
	if _, err := d.transact(protocol.Step{Cmd: protocol.Cmd01}, pingDeadline, 0); err != nil {
		return err
	}
	reply, err := d.transact(
		protocol.Step{Cmd: protocol.CmdDescriptor, Offset: 0, Chunk: make([]byte, protocol.DescriptorLen)},
		pingDeadline, 0)
	if err != nil {
		return err
	}
	idpair := []byte{
		byte(d.target.VID), byte(d.target.VID >> 8),
		byte(d.target.PID), byte(d.target.PID >> 8),
	}
	if !bytes.Contains(reply, idpair) {
		return fmt.Errorf("0x03 descriptor reply did not contain %04x:%04x", d.target.VID, d.target.PID)
	}

	// The same 0x03 reply carries the KB / AP firmware versions (see
	// protocol/descriptor.go). Parsing them is best-effort: a reply that
	// passed the VID/PID check above but is too short for the version fields
	// should not fail the probe.
	if desc, perr := protocol.ParseDescriptorReply(reply); perr != nil {
		applog.Warnf("probe: 0x03 reply matched VID/PID but firmware parse failed: %v", perr)
	} else {
		d.descriptor = desc
		d.hasDescriptor = true
		fw := d.Firmware()
		applog.Infof("probe: firmware KB=%s AP=%s (this tool targets KB=%s AP=%s)%s",
			protocol.FormatVersion(fw.KBVersion), protocol.FormatVersion(fw.APVersion),
			protocol.FormatVersion(protocol.SupportedKBVersion), protocol.FormatVersion(protocol.SupportedAPVersion),
			map[bool]string{true: "", false: " — MISMATCH"}[fw.OK()])
	}
	return nil
}

// drain reads and discards whatever the device already has queued on its
// input endpoint, so the first real Transact is not answered by a leftover
// report from a previous session. It returns the count discarded.
func (d *Device) drain() int {
	buf := make([]byte, protocol.ReportSize)
	n := 0
	for n < maxDrain {
		r, err := d.h.ReadWithTimeout(buf, drainTimeout)
		if err != nil || r == 0 {
			break
		}
		n++
	}
	return n
}

// Close releases the device handle.
func (d *Device) Close() error { return d.h.Close() }

// Transact writes step's report and returns the device's reply, trimmed to
// the bytes read. It errors on a write failure or if no reply whose command
// byte matches step.Cmd arrives within transactDeadline.
//
// The device sometimes has an unrelated report already queued — the tail of a
// previous command, or firmware chatter — so a single read can hand back the
// wrong answer and knock every following read one slot out of step. Transact
// therefore keeps reading until it sees a reply that matches step.Cmd,
// discarding (and logging) the rest, and re-sends the report once if a read
// times out with nothing queued at all.
func (d *Device) Transact(step protocol.Step) ([]byte, error) {
	if step.Cmd == protocol.CmdBeginBulk {
		return d.transact(step, beginBulkDeadline, 0)
	}
	return d.transact(step, transactDeadline, writeRetries)
}

// isInterrupted reports whether err is an EINTR-class failure from the
// blocking poll()/read() inside hidapi's hid_read_timeout (or hid_write).
//
// Go's asynchronous preemption delivers SIGURG to running threads; when it
// lands on the thread parked in the cgo syscall, poll()/read() returns
// -1/EINTR and hidapi's Linux backend passes that straight through without
// retrying. go-hid then surfaces it as strerror(EINTR) = "interrupted system
// call" — a plain string error, not a typed syscall.Errno, so it has to be
// matched by text as well. Harmless for a 3-step clock/lighting write; fatal
// for a screen upload, which issues ~18k blocking reads and so trips it
// almost every run at a random chunk.
func isInterrupted(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EINTR) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "interrupted system call")
}

// transact is Transact with an explicit reply deadline and resend budget. The
// connection probe during Open uses a short deadline and no resend, so probing
// the wrong interface fails fast; command sequences use the generous defaults.
func (d *Device) transact(step protocol.Step, deadline time.Duration, retries int) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			applog.Warnf("transact cmd 0x%02x: no reply, resending (attempt %d)", step.Cmd, attempt+1)
		}
		reply, err := d.transactOnce(step, deadline)
		if err == nil {
			return reply, nil
		}
		lastErr = err
		if !errors.Is(err, hid.ErrTimeout) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (d *Device) transactOnce(step protocol.Step, wait time.Duration) ([]byte, error) {
	report := step.Report()
	for {
		_, err := d.h.Write(report)
		if err == nil {
			break
		}
		if isInterrupted(err) {
			continue // SIGURG hit the cgo write; the report never left, resend
		}
		return nil, fmt.Errorf("write cmd 0x%02x: %w", step.Cmd, err)
	}

	deadline := time.Now().Add(wait)
	buf := make([]byte, protocol.ReportSize)
	discarded := 0
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("read reply for cmd 0x%02x: %w after %d stale report(s)",
				step.Cmd, hid.ErrTimeout, discarded)
		}
		n, err := d.h.ReadWithTimeout(buf, remaining)
		if err != nil {
			if isInterrupted(err) {
				continue // re-poll for the remaining budget, don't fail the upload
			}
			return nil, fmt.Errorf("read reply for cmd 0x%02x: %w", step.Cmd, err)
		}
		reply := buf[:n]
		if protocol.ReplyOK(reply, step.Cmd) {
			if discarded > 0 {
				applog.Infof("transact cmd 0x%02x: matched after discarding %d stale report(s)", step.Cmd, discarded)
			}
			out := make([]byte, n)
			copy(out, reply)
			return out, nil
		}
		discarded++
		if discarded <= 8 {
			applog.Warnf("transact cmd 0x%02x: stale reply #%d (marker=0x%02x cmd=0x%02x len=%d)",
				step.Cmd, discarded, byteAt(reply, 0), byteAt(reply, 3), n)
		}
	}
}

func byteAt(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}

// RunSteps sends steps in order, confirming each reply before the next. If
// progress is non-nil it is called (done, total) after each step.
func (d *Device) RunSteps(steps []protocol.Step, progress func(done, total int)) error {
	return d.RunStepsCtx(context.Background(), steps, progress)
}

// RunStepsCtx is RunSteps with cancellation. It stops and returns ctx.Err()
// if ctx is done before a step is sent.
func (d *Device) RunStepsCtx(ctx context.Context, steps []protocol.Step, progress func(done, total int)) error {
	if len(steps) > 0 {
		applog.Infof("runSteps: %d step(s), cmds 0x%02x..0x%02x",
			len(steps), steps[0].Cmd, steps[len(steps)-1].Cmd)
	}
	for i, s := range steps {
		if err := ctx.Err(); err != nil {
			applog.Warnf("runSteps: cancelled before step %d/%d (cmd 0x%02x)", i+1, len(steps), s.Cmd)
			return err
		}
		if s.Cmd == protocol.CmdCommit {
			time.Sleep(commitDelay)
		}
		if _, err := d.Transact(s); err != nil {
			applog.Errorf("runSteps: step %d/%d cmd 0x%02x offset 0x%x failed: %v",
				i+1, len(steps), s.Cmd, s.Offset, err)
			return err
		}
		if progress != nil {
			progress(i+1, len(steps))
		}
	}
	applog.Infof("runSteps: all %d step(s) OK", len(steps))
	return nil
}

// ReadSettings reads the 49-byte global settings block from device offset 0.
func (d *Device) ReadSettings() (protocol.SettingsBlock, error) {
	// Drop anything still queued from a previous operation. runOnDevice does a
	// settings read immediately after every write's 0x02 commit; if that commit
	// (or the write) left an extra report on the input endpoint, the next
	// operation's replies land one slot out of step and its transactions read
	// stale data or time out — which showed up as "Set entered time works once
	// then not again". A clean read starts from an empty queue.
	if n := d.drain(); n > 0 {
		applog.Infof("readSettings: flushed %d stale report(s) first", n)
	}
	var reply []byte
	for _, s := range protocol.SettingsReadSteps() {
		r, err := d.Transact(s)
		if err != nil {
			return protocol.SettingsBlock{}, err
		}
		reply = r
	}
	b, err := protocol.ParseSettingsReply(reply)
	if err != nil {
		return b, err
	}
	applog.Infof("settings read : %s", b.Summary())
	applog.Infof("settings read : [% x]", b.Raw[:])
	return b, nil
}

// WriteSettings writes b back to device offset 0. Callers reach it through
// ApplySettings, which does a ReadSettings (and its queue flush) first, so a
// separate flush here would only burn another read timeout.
func (d *Device) WriteSettings(b protocol.SettingsBlock) error {
	applog.Infof("settings write: %s", b.Summary())
	applog.Infof("settings write: [% x]", b.Raw[:])
	return d.RunSteps(protocol.SettingsWriteSteps(b), nil)
}

// ApplySettings reads the current block, runs mutate on it, stamps the
// current local time, and writes it back. Fields mutate leaves alone keep
// their on-device values — this is what makes a wired write safe.
func (d *Device) ApplySettings(mutate func(*protocol.SettingsBlock)) error {
	return d.ApplySettingsAt(time.Now(), mutate)
}

// ApplySettingsAt is ApplySettings with an explicit timestamp instead of the
// current time. Use it only to prove a write took effect (see cmd/setclock
// -test); normal writes should stamp the real time.
func (d *Device) ApplySettingsAt(when time.Time, mutate func(*protocol.SettingsBlock)) error {
	b, err := d.ReadSettings()
	if err != nil {
		return err
	}
	if mutate != nil {
		mutate(&b)
	}
	b.SetTime(when)
	return d.WriteSettings(b)
}

// SyncClock stamps the current local time into the settings block, leaving
// every other field untouched.
func (d *Device) SyncClock() error { return d.ApplySettings(nil) }

// SetClock stamps when into the settings block, leaving every other field
// untouched.
func (d *Device) SetClock(when time.Time) error { return d.ApplySettingsAt(when, nil) }

// ApplyKeyColors writes the per-key RGB table and switches the keyboard to the
// custom lighting effect. It reproduces the Windows app's two-phase sequence
// from write_light_a-r_s-g_d-b_q-w_e-bk.pcapng: first the 0x0b table batch
// (0x01 -> seven 0x0b chunks -> 0x02), then a normal settings write that sets
// the effect to Custom and stores primary in the global colour field.
//
// There is no known read-back for the 0x0b table, so the caller owns the
// authoritative copy; this only writes.
func (d *Device) ApplyKeyColors(t protocol.KeyColorTable, primary [3]byte) error {
	applog.Infof("applyKeyColors: writing %d-entry table, primary %02x%02x%02x",
		protocol.KeyColorCount, primary[0], primary[1], primary[2])
	if err := d.RunSteps(protocol.KeyColorWriteSteps(t), nil); err != nil {
		applog.Errorf("applyKeyColors: table write failed: %v", err)
		return err
	}
	return d.ApplySettings(func(b *protocol.SettingsBlock) {
		if b.Effect() != protocol.EffectCustom {
			applog.Infof("applyKeyColors: effect %d -> %d (Custom)", b.Effect(), protocol.EffectCustom)
			b.SetEffect(protocol.EffectCustom)
		}
		b.SetColor(primary[0], primary[1], primary[2])
	})
}

// UploadScreen uploads frames (each FrameBytes of big-endian RGB565) to the
// TFT screen at intervalMS between frames. Wired only.
func (d *Device) UploadScreen(ctx context.Context, frames [][]byte, intervalMS int, progress func(done, total int)) error {
	if !d.target.Wired {
		return errors.New("the TFT screen upload works over the wired keyboard only")
	}
	applog.Infof("uploadScreen: %d frame(s), interval %d ms", len(frames), intervalMS)
	base, err := d.ReadSettings()
	if err != nil {
		applog.Errorf("uploadScreen: pre-read settings failed: %v", err)
		return err
	}
	steps, err := protocol.UploadSteps(frames, intervalMS, base)
	if err != nil {
		applog.Errorf("uploadScreen: build steps failed: %v", err)
		return err
	}
	if err := d.RunStepsCtx(ctx, steps, progress); err != nil {
		applog.Errorf("uploadScreen: failed: %v", err)
		return err
	}
	applog.Infof("uploadScreen: %d frame(s) delivered", len(frames))
	return nil
}

// IsPermissionError reports whether err looks like an OS permission denial on
// the HID node — on Linux, a missing udev rule.
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "permission denied") ||
		strings.Contains(s, "access denied") ||
		strings.Contains(s, "eacces") ||
		strings.Contains(s, "operation not permitted")
}
