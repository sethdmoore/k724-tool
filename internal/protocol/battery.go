package protocol

import "errors"

// Command 0x1A: battery / status read.
//
// docs/COMMANDS.md: "Confirmed on the wire: request all-zero, response
// 64 01 00 00 00 00" through the wireless receiver, and "64 02 ..." through
// the wired keyboard. 0x64 == 100 decimal, which lines up with a battery
// percentage (100% wired/charging is the usual behaviour for wireless
// peripherals with an onboard battery). RE_STATUS.md session 2 already
// guesses "get battery" for one of the 17 command builders sharing this
// transaction function.
//
// This is a hypothesis, not a confirmed byte map: only one capture of this
// reply exists, and it was never taken at a battery level other than 100%.
// Treat BatteryStatus.Percent as "probably right," and flag anywhere it's
// surfaced accordingly.
const batteryReplyMinLen = 1

// BatteryStatus is a parsed command 0x1A reply.
type BatteryStatus struct {
	// Percent is the reply's first body byte, hypothesised to be a 0-100
	// battery percentage.
	Percent int

	// Raw is the full reply body, copied, for anything not yet decoded
	// (the second byte tracks the connected device's major version per
	// docs/COMMANDS.md, not charge state).
	Raw []byte
}

// ParseBatteryReply extracts the battery status from a raw reply to a
// command 0x1A read.
func ParseBatteryReply(reply []byte) (BatteryStatus, error) {
	if !ReplyOK(reply, CmdBattery) {
		return BatteryStatus{}, errors.New("protocol: not a 0x1A reply")
	}
	if len(reply) < HeaderSize+batteryReplyMinLen {
		return BatteryStatus{}, errors.New("protocol: 0x1A reply body is empty")
	}
	body := reply[HeaderSize:]
	b := make([]byte, len(body))
	copy(b, body)

	percent := int(b[0])
	if percent > 100 {
		percent = 100
	}
	return BatteryStatus{Percent: percent, Raw: b}, nil
}

// BatteryReadSteps returns the step sequence that reads the battery/status
// value: a single bare 0x1A read, request body all-zero (matching the
// captured request). Feed the final reply to ParseBatteryReply.
func BatteryReadSteps() []Step {
	return []Step{
		{Cmd: CmdBattery, Offset: 0, Chunk: make([]byte, 6)},
	}
}
