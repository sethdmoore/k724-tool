package protocol

import "testing"

func TestBuildReportWideOffset(t *testing.T) {
	// docs/SCREEN.md worked example: cmd 0x21, len 0x38, 24-bit offset
	// 0x02fdd8 -> bytes d8 fd 02, checksum 0x0230 with an all-zero payload.
	chunk := make([]byte, 0x38)
	r := BuildReportWide(CmdScreen, 0x02fdd8, chunk)

	if r[3] != 0x21 || r[4] != 0x38 {
		t.Fatalf("cmd/len = %02x %02x, want 21 38", r[3], r[4])
	}
	if r[5] != 0xd8 || r[6] != 0xfd || r[7] != 0x02 {
		t.Fatalf("offset bytes = %02x %02x %02x, want d8 fd 02", r[5], r[6], r[7])
	}
	if got := uint16(r[1]) | uint16(r[2])<<8; got != 0x0230 {
		t.Fatalf("checksum = 0x%04x, want 0x0230", got)
	}
}

func TestUploadStepsShape(t *testing.T) {
	frame := make([]byte, FrameBytes)
	frames := [][]byte{frame, frame, frame}

	steps, err := UploadSteps(frames, 200, SettingsBlock{})
	if err != nil {
		t.Fatalf("UploadSteps: %v", err)
	}

	// prologue: 0x01, 0x06, 0x02, 0x23
	if steps[0].Cmd != Cmd01 || steps[1].Cmd != CmdWriteAt ||
		steps[2].Cmd != CmdCommit || steps[3].Cmd != CmdBeginBulk {
		t.Fatalf("prologue cmds = %02x %02x %02x %02x", steps[0].Cmd, steps[1].Cmd, steps[2].Cmd, steps[3].Cmd)
	}
	// epilogue: trailing 0x02
	last := steps[len(steps)-1]
	if last.Cmd != CmdCommit {
		t.Fatalf("last cmd = %02x, want 02", last.Cmd)
	}

	// The settings chunk must carry the frame count and interval.
	cfg, err := ParseSettings(steps[1].Chunk)
	if err != nil {
		t.Fatalf("settings chunk: %v", err)
	}
	if cfg.FrameCount() != 3 {
		t.Errorf("FrameCount = %d, want 3", cfg.FrameCount())
	}
	if cfg.FrameIntervalMS() != 200 {
		t.Errorf("FrameIntervalMS = %d, want 200", cfg.FrameIntervalMS())
	}

	// 0x21 chunks: continuous 56-byte run over 3*FrameSlot bytes, one short
	// final chunk. 3*0x10000 = 196608 = 3510*56 + 48.
	var screen []Step
	for _, s := range steps {
		if s.Cmd == CmdScreen {
			screen = append(screen, s)
		}
	}
	if len(screen) != 3511 {
		t.Fatalf("0x21 chunk count = %d, want 3511", len(screen))
	}
	if screen[0].Offset != 0 || !screen[0].Wide {
		t.Errorf("first chunk offset = %d wide=%v", screen[0].Offset, screen[0].Wide)
	}
	fin := screen[len(screen)-1]
	if fin.Offset != 0x2FFD0 {
		t.Errorf("final chunk offset = 0x%x, want 0x2FFD0", fin.Offset)
	}
	if len(fin.Chunk) != 48 {
		t.Errorf("final chunk len = %d, want 48", len(fin.Chunk))
	}
	for i, s := range screen[:len(screen)-1] {
		if len(s.Chunk) != 56 {
			t.Fatalf("chunk %d len = %d, want 56", i, len(s.Chunk))
		}
	}
}

func TestUploadStepsRejectsBadFrame(t *testing.T) {
	if _, err := UploadSteps(nil, 100, SettingsBlock{}); err == nil {
		t.Error("want error for zero frames")
	}
	if _, err := UploadSteps([][]byte{make([]byte, 10)}, 100, SettingsBlock{}); err == nil {
		t.Error("want error for wrong-sized frame")
	}
}
