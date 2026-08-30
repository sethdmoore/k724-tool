package protocol

import "fmt"

// Per-key custom lighting: command 0x0b writes a 384-byte table of 128
// three-byte RGB entries, one per physical key/LED, in the keyboard's matrix
// order. Decoded from write_light_a-r_s-g_d-b_q-w_e-bk.pcapng, where setting
// the A/S/D/Q/E keys to red/green/blue/white/black changed table entries
// 49/50/51/33/35 respectively — the same entry order as the 0x09 key-remap
// table in button_write_j_default_key.pcapng, whose entries are HID usage
// codes and so name every position (see KeyboardLayout).
//
// The Windows app follows the 0x0b batch with a normal 0x06 settings write
// that sets the lighting effect to EffectCustom and stores the last-picked
// colour in the global colour field (bytes 6-8). Device.ApplyKeyColors does
// both.
const (
	// CmdKeyColors is the per-key RGB table write.
	CmdKeyColors = 0x0b

	// KeyColorCount is the number of addressable entries in the table.
	KeyColorCount = 128

	// KeyColorTableLen is the table's wire length: 3 bytes (R,G,B) per entry.
	KeyColorTableLen = KeyColorCount * 3

	// EffectCustom is the lighting-effect index (settings byte 1) that makes
	// the keyboard display the per-key table. 0x13 in the capture.
	EffectCustom = 0x13
)

// KeyColorTable is the 128-entry per-key RGB table. The zero value is an
// all-black (all-LEDs-off) table.
type KeyColorTable [KeyColorCount][3]byte

// SetRGB sets entry i to (r,g,b). Out-of-range indices are ignored.
func (t *KeyColorTable) SetRGB(i int, r, g, b byte) {
	if i < 0 || i >= KeyColorCount {
		return
	}
	t[i] = [3]byte{r, g, b}
}

// Fill sets every entry to (r,g,b).
func (t *KeyColorTable) Fill(r, g, b byte) {
	for i := range t {
		t[i] = [3]byte{r, g, b}
	}
}

// Bytes returns the table's KeyColorTableLen-byte wire form.
func (t KeyColorTable) Bytes() []byte {
	out := make([]byte, 0, KeyColorTableLen)
	for _, e := range t {
		out = append(out, e[0], e[1], e[2])
	}
	return out
}

// ParseKeyColorTable reads a KeyColorTableLen-byte wire form back into a table.
func ParseKeyColorTable(b []byte) (KeyColorTable, error) {
	var t KeyColorTable
	if len(b) != KeyColorTableLen {
		return t, fmt.Errorf("protocol: key colour table is %d bytes, want %d", len(b), KeyColorTableLen)
	}
	for i := range t {
		t[i] = [3]byte{b[i*3], b[i*3+1], b[i*3+2]}
	}
	return t, nil
}

// KeyColorWriteSteps returns the step sequence that writes t to the device:
// a 0x01, seven 0x0b chunks (56 bytes each, the last 48) carrying the 384-byte
// table at climbing device offsets, then a 0x02 commit. This mirrors the
// chunking in write_light_a-r_s-g_d-b_q-w_e-bk.pcapng exactly (offsets
// 0x00/0x38/0x70/0xa8/0xe0/0x118/0x150).
func KeyColorWriteSteps(t KeyColorTable) []Step {
	buf := t.Bytes()
	steps := make([]Step, 0, 2+((KeyColorTableLen+screenChunk-1)/screenChunk))
	steps = append(steps, Step{Cmd: Cmd01})
	for off := 0; off < KeyColorTableLen; off += screenChunk {
		n := screenChunk
		if off+n > KeyColorTableLen {
			n = KeyColorTableLen - off
		}
		chunk := make([]byte, n)
		copy(chunk, buf[off:off+n])
		steps = append(steps, Step{Cmd: CmdKeyColors, Offset: uint32(off), Chunk: chunk})
	}
	steps = append(steps, Step{Cmd: CmdCommit})
	return steps
}
