package protocol

import (
	"errors"
	"fmt"
	"time"
)

// TFT screen geometry. See docs/SCREEN.md.
const (
	ScreenW    = 240
	ScreenH    = 135
	FrameBytes = ScreenW * ScreenH * 2 // 64800, big-endian RGB565

	// FrameSlot is the device-offset stride between frames. Each frame
	// occupies FrameBytes of pixels followed by padding out to FrameSlot;
	// the padding is transmitted as zeros.
	FrameSlot = 0x10000 // 65536

	screenChunk = 0x38 // 56 bytes per 0x21 chunk (the last chunk is short)
)

// UploadSteps builds the full step sequence to push frames to the TFT screen:
//
//	0x01 -> 0x06 settings write (frame count + interval + fresh timestamp)
//	-> 0x02 -> 0x23 -> a continuous run of 0x21 chunks -> 0x02
//
// Each frame must be exactly FrameBytes of big-endian RGB565 (use
// internal/screen). The 0x21 offset starts at 0 and runs unbroken across the
// whole N*FrameSlot address space in 56-byte chunks, with a single short
// final chunk of (N*FrameSlot mod 56) bytes; there is no inter-frame
// delimiter. base supplies the lighting / polling fields to preserve.
func UploadSteps(frames [][]byte, intervalMS int, base SettingsBlock) ([]Step, error) {
	if len(frames) == 0 {
		return nil, errors.New("protocol: no frames to upload")
	}
	for i, f := range frames {
		if len(f) != FrameBytes {
			return nil, fmt.Errorf("protocol: frame %d is %d bytes, want %d", i, len(f), FrameBytes)
		}
	}

	cfg := base
	cfg.SetFrameCount(len(frames))
	cfg.SetFrameIntervalMS(intervalMS)
	cfg.SetTime(time.Now())

	total := len(frames) * FrameSlot
	buf := make([]byte, total)
	for k, f := range frames {
		copy(buf[k*FrameSlot:], f) // leaves the slot's tail padding zero
	}

	steps := make([]Step, 0, 4+total/screenChunk+2)
	steps = append(steps, Step{Cmd: Cmd01})
	steps = append(steps, SettingsWriteSteps(cfg)[1]) // just the 0x06 chunk
	steps = append(steps, Step{Cmd: CmdCommit})
	steps = append(steps, Step{Cmd: CmdBeginBulk})

	for i := 0; i < total; i += screenChunk {
		n := screenChunk
		if i+n > total {
			n = total - i
		}
		chunk := make([]byte, n)
		copy(chunk, buf[i:i+n])
		steps = append(steps, Step{Cmd: CmdScreen, Offset: uint32(i), Chunk: chunk, Wide: true})
	}

	steps = append(steps, Step{Cmd: CmdCommit})
	return steps, nil
}
