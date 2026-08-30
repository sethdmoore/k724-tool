package protocol

import (
	"encoding/hex"
	"testing"
)

// capturedDescriptorBody is the command 0x03 reply body, byte-for-byte, from
// every wired capture in ../../.. (../captures/open_redragon.pcapng,
// change_polling_*, light_presets, gradient, button_write_j_default_key — all
// identical). The unit is KB V0206 / AP V0100.
const capturedDescriptorBody = "aa55000003801800020000000000000000000000000f321b51000100060206f08703125500"

func capturedDescriptorReply(t *testing.T) []byte {
	t.Helper()
	body, err := hex.DecodeString(capturedDescriptorBody)
	if err != nil {
		t.Fatalf("bad test hex: %v", err)
	}
	if len(body) != DescriptorLen {
		t.Fatalf("captured body is %d bytes, want DescriptorLen=%d", len(body), DescriptorLen)
	}
	reply := make([]byte, ReportSize)
	reply[0] = reportMarker
	reply[3] = CmdDescriptor
	copy(reply[HeaderSize:], body)
	return reply
}

func TestParseDescriptorReply(t *testing.T) {
	d, err := ParseDescriptorReply(capturedDescriptorReply(t))
	if err != nil {
		t.Fatalf("ParseDescriptorReply: %v", err)
	}

	if got, want := d.VID(), uint16(VendorID); got != want {
		t.Errorf("VID = %#04x, want %#04x", got, want)
	}
	if got, want := d.PID(), uint16(ProductIDWired); got != want {
		t.Errorf("PID = %#04x, want %#04x", got, want)
	}
	if got, want := d.KBVersion(), SupportedKBVersion; got != want {
		t.Errorf("KBVersion = %#04x, want %#04x", got, want)
	}
	if got, want := d.APVersion(), SupportedAPVersion; got != want {
		t.Errorf("APVersion = %#04x, want %#04x", got, want)
	}
	if got, want := FormatVersion(d.KBVersion()), "V0206"; got != want {
		t.Errorf("FormatVersion(KB) = %q, want %q", got, want)
	}
	if got, want := FormatVersion(d.APVersion()), "V0100"; got != want {
		t.Errorf("FormatVersion(AP) = %q, want %q", got, want)
	}
}

func TestParseDescriptorReplyRejects(t *testing.T) {
	t.Run("wrong command", func(t *testing.T) {
		reply := capturedDescriptorReply(t)
		reply[3] = CmdReadAt
		if _, err := ParseDescriptorReply(reply); err == nil {
			t.Fatal("want error for a non-0x03 reply, got nil")
		}
	})
	t.Run("short body", func(t *testing.T) {
		reply := capturedDescriptorReply(t)[:HeaderSize+10]
		if _, err := ParseDescriptorReply(reply); err == nil {
			t.Fatal("want error for a truncated reply, got nil")
		}
	})
}

func TestFormatVersion(t *testing.T) {
	cases := map[uint16]string{
		0x0206: "V0206",
		0x0100: "V0100",
		0x0000: "V0000",
		0x12ab: "V12ab",
	}
	for v, want := range cases {
		if got := FormatVersion(v); got != want {
			t.Errorf("FormatVersion(%#04x) = %q, want %q", v, got, want)
		}
	}
}

func TestDescriptorReadSteps(t *testing.T) {
	steps := DescriptorReadSteps()
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	if steps[0].Cmd != CmdDescriptor {
		t.Errorf("step cmd = %#02x, want %#02x", steps[0].Cmd, CmdDescriptor)
	}
	if len(steps[0].Chunk) != DescriptorLen {
		t.Errorf("step chunk = %d bytes, want %d", len(steps[0].Chunk), DescriptorLen)
	}
}
