package protocol

import "testing"

func TestKeyColorTableRoundTrip(t *testing.T) {
	var tbl KeyColorTable
	tbl.SetRGB(49, 0xff, 0x00, 0x00) // A -> red
	tbl.SetRGB(50, 0x00, 0xff, 0x00) // S -> green
	tbl.SetRGB(51, 0x00, 0x00, 0xff) // D -> blue

	b := tbl.Bytes()
	if len(b) != KeyColorTableLen {
		t.Fatalf("Bytes() = %d, want %d", len(b), KeyColorTableLen)
	}
	if b[49*3] != 0xff || b[50*3+1] != 0xff || b[51*3+2] != 0xff {
		t.Fatalf("entries not laid out R,G,B per slot: % x", b[49*3:52*3])
	}

	got, err := ParseKeyColorTable(b)
	if err != nil {
		t.Fatalf("ParseKeyColorTable: %v", err)
	}
	if got != tbl {
		t.Fatalf("round-trip mismatch")
	}
}

// TestKeyColorWriteStepsMatchCapture checks the chunking against
// write_light_a-r_s-g_d-b_q-w_e-bk.pcapng: 0x01, then 0x0b chunks at offsets
// 0x00/0x38/0x70/0xa8/0xe0/0x118/0x150 (56 bytes each, last 48), then 0x02.
func TestKeyColorWriteStepsMatchCapture(t *testing.T) {
	var tbl KeyColorTable
	steps := KeyColorWriteSteps(tbl)

	if steps[0].Cmd != Cmd01 || steps[len(steps)-1].Cmd != CmdCommit {
		t.Fatalf("not bracketed by 0x01/0x02: %02x .. %02x", steps[0].Cmd, steps[len(steps)-1].Cmd)
	}
	chunks := steps[1 : len(steps)-1]
	wantOff := []uint32{0x00, 0x38, 0x70, 0xa8, 0xe0, 0x118, 0x150}
	if len(chunks) != len(wantOff) {
		t.Fatalf("got %d 0x0b chunks, want %d", len(chunks), len(wantOff))
	}
	total := 0
	for i, s := range chunks {
		if s.Cmd != CmdKeyColors {
			t.Errorf("chunk %d cmd = %02x, want %02x", i, s.Cmd, CmdKeyColors)
		}
		if s.Offset != wantOff[i] {
			t.Errorf("chunk %d offset = %#x, want %#x", i, s.Offset, wantOff[i])
		}
		if s.Wide {
			t.Errorf("chunk %d is wide-offset; 0x0b uses the 16-bit offset", i)
		}
		want := screenChunk
		if i == len(chunks)-1 {
			want = KeyColorTableLen - (len(chunks)-1)*screenChunk // 48
		}
		if len(s.Chunk) != want {
			t.Errorf("chunk %d len = %d, want %d", i, len(s.Chunk), want)
		}
		total += len(s.Chunk)
	}
	if total != KeyColorTableLen {
		t.Fatalf("chunks carry %d bytes, want %d", total, KeyColorTableLen)
	}
}

func TestLayoutIndicesSaneAndNamed(t *testing.T) {
	seen := map[int]bool{}
	for _, i := range LayoutIndices() {
		if i < 0 || i >= KeyColorCount {
			t.Errorf("layout index %d out of range", i)
		}
		if seen[i] {
			t.Errorf("layout index %d appears twice", i)
		}
		seen[i] = true
	}
	// Spot-check the five keys the colour capture pinned down.
	want := map[string]int{"A": 49, "S": 50, "D": 51, "Q": 33, "E": 35}
	got := map[string]int{}
	for _, row := range KeyboardLayout {
		for _, k := range row {
			if _, ok := want[k.Name]; ok {
				got[k.Name] = k.Index
			}
		}
	}
	for name, idx := range want {
		if got[name] != idx {
			t.Errorf("%s at index %d, want %d", name, got[name], idx)
		}
	}
}

func TestFrameIntervalIs16Bit(t *testing.T) {
	var s SettingsBlock
	s.SetFrameIntervalMS(50000)
	if s.Raw[offFrameInterval] != 0x50 || s.Raw[offFrameInterval+1] != 0xc3 {
		t.Fatalf("50000 -> % x, want 50 c3", s.Raw[offFrameInterval:offFrameInterval+2])
	}
	if got := s.FrameIntervalMS(); got != 50000 {
		t.Fatalf("FrameIntervalMS = %d, want 50000", got)
	}
	s.SetFrameIntervalMS(1 << 20) // clamps to FrameIntervalMax
	if got := s.FrameIntervalMS(); got != FrameIntervalMax {
		t.Fatalf("clamp = %d, want %d", got, FrameIntervalMax)
	}
}

func TestOtherSettingsToggles(t *testing.T) {
	var s SettingsBlock
	for _, tc := range []struct {
		name string
		off  int
		set  func(bool)
		get  func() bool
	}{
		{"keyswap", offKeySwap, s.SetKeySwap, s.KeySwap},
		{"nkro", offNKRO, s.SetNKRO, s.NKRO},
		{"winlock", offWinLock, s.SetWinLock, s.WinLock},
	} {
		tc.set(true)
		if s.Raw[tc.off] != 1 || !tc.get() {
			t.Errorf("%s: set(true) -> byte %d = %d", tc.name, tc.off, s.Raw[tc.off])
		}
		tc.set(false)
		if s.Raw[tc.off] != 0 || tc.get() {
			t.Errorf("%s: set(false) -> byte %d = %d", tc.name, tc.off, s.Raw[tc.off])
		}
	}
}
