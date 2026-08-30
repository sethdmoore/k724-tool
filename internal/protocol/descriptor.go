package protocol

import (
	"errors"
	"fmt"
)

// The command 0x03 descriptor block.
//
// The wired open sequence issues a bare 0x03 read of DescriptorLen bytes
// (internal/k724's probe, mirroring every wired capture in ../captures/).
// All five wired captures return the same 37-byte reply body:
//
//		aa 55 00 00 03 80 18 00 02  <12x 00>  0f 32 1b 51  00 01 00  06 02 06  f0 87 03 12 55 00
//		|__ marker    |  |  |  |__ record count (2)
//		              |  |  |__ payload length (0x18 = 24)
//		              |  |__ command id echo
//		              |__ "aa 55" reply tag
//
//	  - bytes 21..24: the wired keyboard's own USB VID/PID, little-endian
//	    (0x320f / 0x511b). probe() matches on this pair.
//	  - bytes 25..30: two 3-byte firmware records, {tag, version_hi, version_lo}:
//	    rec 0 @ 25 = 00 01 00  ->  version 0x0100  ->  "V0100"  (AP: 2.4 GHz receiver)
//	    rec 1 @ 28 = 06 02 06  ->  version 0x0206  ->  "V0206"  (KB: keyboard)
//
// The version is a big-endian uint16 rendered "V%04x". That matches the
// Windows control app: its firmware-table label is built with the format
// string "V%04x" and it ships a literal "V0100" default (both visible with
// `strings -e l` on K724-RGB-PRO.exe). That app's Settings window shows the
// same pair as a table:
//
//	| Name | Version Information | Update Status  |
//	| KB   | V0206               | Latest Version |
//	| AP   | V0100               | Latest Version |
//
// Only one capture set — one keyboard, one firmware revision — backs the
// record offsets, so treat the {tag, version} split and the rec0=AP /
// rec1=KB assignment as a strong hypothesis, not a certainty.
const (
	descVIDOff   = 21 // little-endian uint16
	descPIDOff   = 23 // little-endian uint16
	descAPVerOff = 26 // big-endian uint16, "V%04x"
	descKBVerOff = 29 // big-endian uint16, "V%04x"

	descMinLen = descKBVerOff + 2 // shortest body ParseDescriptorReply accepts
)

// SupportedKBVersion and SupportedAPVersion are the keyboard and receiver
// firmware versions this tool's wire protocol was reverse-engineered against
// (KB V0206, AP V0100). The device layer compares a connected unit's reported
// versions against these and warns on any mismatch — the protocol details
// (settings-block byte map, screen upload, probe sequence) were only ever
// confirmed on this firmware.
const (
	SupportedKBVersion uint16 = 0x0206
	SupportedAPVersion uint16 = 0x0100
)

// DescriptorBlock is a parsed command 0x03 reply.
type DescriptorBlock struct {
	// Raw is the reply body (the bytes after the 8-byte report header),
	// copied. It is at least descMinLen bytes long.
	Raw []byte
}

// ParseDescriptorReply extracts the descriptor block from a raw reply to a
// command 0x03 read.
func ParseDescriptorReply(reply []byte) (DescriptorBlock, error) {
	if !ReplyOK(reply, CmdDescriptor) {
		return DescriptorBlock{}, errors.New("protocol: not a 0x03 reply")
	}
	if len(reply) < HeaderSize+descMinLen {
		return DescriptorBlock{}, fmt.Errorf(
			"protocol: 0x03 reply is %d bytes, need at least %d", len(reply), HeaderSize+descMinLen)
	}
	body := reply[HeaderSize:]
	b := make([]byte, len(body))
	copy(b, body)
	return DescriptorBlock{Raw: b}, nil
}

func (d DescriptorBlock) le16(off int) uint16 {
	return uint16(d.Raw[off]) | uint16(d.Raw[off+1])<<8
}

func (d DescriptorBlock) be16(off int) uint16 {
	return uint16(d.Raw[off])<<8 | uint16(d.Raw[off+1])
}

// VID returns the USB vendor ID embedded in the block.
func (d DescriptorBlock) VID() uint16 { return d.le16(descVIDOff) }

// PID returns the USB product ID embedded in the block.
func (d DescriptorBlock) PID() uint16 { return d.le16(descPIDOff) }

// KBVersion returns the keyboard firmware version, e.g. 0x0206 for "V0206".
func (d DescriptorBlock) KBVersion() uint16 { return d.be16(descKBVerOff) }

// APVersion returns the 2.4 GHz receiver firmware version, e.g. 0x0100.
func (d DescriptorBlock) APVersion() uint16 { return d.be16(descAPVerOff) }

// FormatVersion renders a firmware version the way the Windows app does:
// a "V" followed by the 16-bit value as four lowercase hex digits.
func FormatVersion(v uint16) string { return fmt.Sprintf("V%04x", v) }

// DescriptorReadSteps returns the step sequence that reads the device
// descriptor block: a single bare 0x03 read of DescriptorLen bytes, no 0x01
// in front of it beyond the one the open sequence already sent. Feed the
// final reply to ParseDescriptorReply.
func DescriptorReadSteps() []Step {
	return []Step{
		{Cmd: CmdDescriptor, Offset: 0, Chunk: make([]byte, DescriptorLen)},
	}
}
