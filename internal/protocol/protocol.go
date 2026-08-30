// Package protocol implements the Redragon K724-RGB-PRO wire protocol: HID
// report framing, the checksum, BCD encoding, and the clock-set payload.
// A live USB capture confirmed the format.
package protocol

import (
	"encoding/hex"
	"time"
)

const (
	// ReportSize is the length of one HID interrupt-OUT report, in bytes.
	ReportSize = 64

	// HeaderSize is the length of the report header, before the chunk data.
	HeaderSize = 8

	// MaxChunkLen is the largest chunk a single report can carry.
	MaxChunkLen = ReportSize - HeaderSize

	// DescriptorLen is the payload length of the command 0x03 descriptor read
	// the wired open sequence uses. The reply embeds the keyboard's VID/PID
	// (little-endian pair) and the KB / AP firmware versions at fixed offsets;
	// see descriptor.go and docs/COMMANDS.md.
	DescriptorLen = 37

	reportMarker = 0x04
)

// Command IDs used by the device. See docs/COMMANDS.md for the full table.
const (
	CmdPing       = 0xAA // ping / connection probe (wireless open sequence only)
	Cmd01         = 0x01 // begin transaction / write batch
	CmdCommit     = 0x02 // commit / apply a write batch
	CmdDescriptor = 0x03 // read the device descriptor block (wired open sequence)
	CmdReadAt     = 0x05 // read N bytes at a device offset
	CmdWriteAt    = 0x06 // write N bytes at a device offset (the settings block)
	CmdWriteClock = CmdWriteAt
	CmdBeginBulk  = 0x23 // "begin bulk image transfer" marker, before the 0x21 run
	CmdScreen     = 0x21 // TFT frame push, 24-bit device offset in bytes 5-7
)

// USB device identity. The wired keyboard and the 2.4 GHz wireless receiver
// share a vendor ID but use different product IDs.
const (
	VendorID          = 0x320f
	ProductIDWired    = 0x511b
	ProductIDWireless = 0x511c
)

// HID usage pages to skip when a caller looks for the vendor command
// channel. These pages carry the standard keyboard and gamepad interfaces,
// not the vendor interface.
const (
	UsagePageGenericDesktop = 0x0001
	UsagePageKeyboard       = 0x0007
)

var (
	clockPayloadPrefix = mustHex("000503020001cccccc06000000b400ff00ff0000ff00000000000000000000ff00000a")
	clockPayloadSuffix = mustHex("00640000000100")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// BCD encodes value, a number from 0 through 99, as one binary-coded
// decimal byte.
func BCD(value int) byte {
	return byte(((value / 10) << 4) | (value % 10))
}

// Checksum returns the 16-bit checksum of a report body: the sum of the
// bytes from index 3 through the end of body, kept to 16 bits.
func Checksum(body []byte) uint16 {
	var sum int
	for _, b := range body[3:] {
		sum += int(b)
	}
	return uint16(sum & 0xFFFF)
}

// BuildReport builds one 64-byte HID interrupt-OUT report for command cmd,
// at device buffer offset offset, with chunk as the payload. The chunk must
// hold no more than MaxChunkLen bytes.
func BuildReport(cmd byte, offset uint16, chunk []byte) []byte {
	if len(chunk) > MaxChunkLen {
		panic("protocol: chunk longer than MaxChunkLen")
	}

	report := make([]byte, ReportSize)
	report[0] = reportMarker
	report[3] = cmd
	report[4] = byte(len(chunk))
	report[5] = byte(offset)
	report[6] = byte(offset >> 8)
	copy(report[HeaderSize:], chunk)

	sum := Checksum(report[:HeaderSize+len(chunk)])
	report[1] = byte(sum)
	report[2] = byte(sum >> 8)
	return report
}

// BuildReportWide builds one 64-byte HID interrupt-OUT report that carries a
// 24-bit little-endian device offset in bytes 5-7 instead of the usual 16-bit
// offset in bytes 5-6. Only command 0x21 (the TFT frame push) uses this form;
// see docs/SCREEN.md. The checksum formula is unchanged: byte 7 falls inside
// the summed range, so it is covered automatically.
func BuildReportWide(cmd byte, offset uint32, chunk []byte) []byte {
	if len(chunk) > MaxChunkLen {
		panic("protocol: chunk longer than MaxChunkLen")
	}

	report := make([]byte, ReportSize)
	report[0] = reportMarker
	report[3] = cmd
	report[4] = byte(len(chunk))
	report[5] = byte(offset)
	report[6] = byte(offset >> 8)
	report[7] = byte(offset >> 16)
	copy(report[HeaderSize:], chunk)

	sum := Checksum(report[:HeaderSize+len(chunk)])
	report[1] = byte(sum)
	report[2] = byte(sum >> 8)
	return report
}

// ReplyOK reports whether reply is a valid answer to a report sent with
// command cmd.
func ReplyOK(reply []byte, cmd byte) bool {
	return len(reply) >= 4 && reply[0] == reportMarker && reply[3] == cmd
}

// IsVendorUsagePage reports whether usagePage belongs to the vendor command
// channel, as opposed to a standard keyboard or gamepad usage page.
func IsVendorUsagePage(usagePage uint16) bool {
	return usagePage != UsagePageGenericDesktop && usagePage != UsagePageKeyboard
}

// ClockPayload builds the 49-byte command 0x06 payload that sets the
// device clock to t.
//
// Go's time.Weekday is already Sunday=0..Saturday=6, the same convention
// the device wants, so no weekday conversion is necessary here.
func ClockPayload(t time.Time) []byte {
	fields := []byte{
		BCD(t.Second()),
		BCD(t.Minute()),
		BCD(t.Hour()),
		BCD(int(t.Weekday())),
		BCD(t.Day()),
		BCD(int(t.Month())),
		BCD(t.Year() % 100),
	}

	payload := make([]byte, 0, 49)
	payload = append(payload, clockPayloadPrefix...)
	payload = append(payload, fields...)
	payload = append(payload, clockPayloadSuffix...)
	if len(payload) != 49 {
		panic("protocol: clock payload is not 49 bytes")
	}
	return payload
}

// Step is one command in a report sequence: a command ID, a device buffer
// offset, and a data chunk. When Wide is set the offset is framed as a 24-bit
// value (bytes 5-7); otherwise it is the usual 16-bit offset (bytes 5-6).
type Step struct {
	Cmd    byte
	Offset uint32
	Chunk  []byte
	Wide   bool
}

// Report builds the 64-byte HID interrupt-OUT report for this step.
func (s Step) Report() []byte {
	if s.Wide {
		return BuildReportWide(s.Cmd, s.Offset, s.Chunk)
	}
	return BuildReport(s.Cmd, uint16(s.Offset), s.Chunk)
}

// ClockSteps returns the step sequence that sets the device clock to t: a
// ping, two 0x01 writes, three 0x06 chunks that carry the payload, and a
// 0x02 commit. Send the steps in order and confirm each reply before the
// next write.
//
// Deprecated: this blind write hard-codes the session-3 wireless template,
// including the custom-colour bytes that force every key to white on the
// wired keyboard (see docs/PROTOCOL.md). The safe path is a read-modify-write
// of the settings block: SettingsReadSteps, SettingsBlock.SetTime,
// SettingsWriteSteps. Kept only for cmd/setclock's existing behaviour.
func ClockSteps(t time.Time) []Step {
	payload := ClockPayload(t)
	return []Step{
		{Cmd: CmdPing},
		{Cmd: Cmd01},
		{Cmd: Cmd01},
		{Cmd: CmdWriteClock, Offset: 0, Chunk: payload[0:24]},
		{Cmd: CmdWriteClock, Offset: 24, Chunk: payload[24:48]},
		{Cmd: CmdWriteClock, Offset: 48, Chunk: payload[48:49]},
		{Cmd: CmdCommit},
	}
}
