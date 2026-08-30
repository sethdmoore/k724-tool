package screen

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// The golden .rgb565 files are frame 0 and frame 1 of the upload in
// captures/gradient.pcapng, sliced out of the reassembled burst. They are the
// exact bytes the device received for grad0.bmp / grad1.bmp, which are already
// 240x135, so Frame must reproduce them with no scaling.
func TestFrameMatchesDeviceCapture(t *testing.T) {
	cases := []struct{ bmp, golden string }{
		{"testdata/grad0.bmp", "testdata/grad0.rgb565"},
		{"testdata/grad1.bmp", "testdata/grad1.rgb565"},
	}
	for _, c := range cases {
		t.Run(c.bmp, func(t *testing.T) {
			f, err := os.Open(c.bmp)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			img, _, err := image.Decode(f)
			if err != nil {
				t.Fatal(err)
			}
			got := Frame(img, nil)

			want, err := os.ReadFile(c.golden)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != FrameBytes {
				t.Fatalf("frame is %d bytes, want %d", len(got), FrameBytes)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("frame mismatch: first diff at %d", firstDiff(got, want))
			}
		})
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func TestFramesSingleImage(t *testing.T) {
	data, err := os.ReadFile("testdata/grad0.bmp")
	if err != nil {
		t.Fatal(err)
	}
	frames, transparent, err := Frames(bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	if transparent {
		t.Errorf("grad0.bmp reported as transparent, want opaque")
	}
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if len(frames[0]) != FrameBytes {
		t.Fatalf("frame is %d bytes, want %d", len(frames[0]), FrameBytes)
	}
}

func TestDecodeRoundTrips(t *testing.T) {
	// grad0: white top-left, black bottom-right. Decode the golden frame and
	// check the corners, then re-encode and confirm it is stable.
	golden, err := os.ReadFile("testdata/grad0.rgb565")
	if err != nil {
		t.Fatal(err)
	}
	img := Decode(golden)

	tl := img.At(0, 0)
	br := img.At(Width-1, Height-1)
	r, g, b, _ := tl.RGBA()
	if r>>8 < 0xf0 || g>>8 < 0xf0 || b>>8 < 0xf0 {
		t.Errorf("top-left = %v, want near-white", tl)
	}
	r, g, b, _ = br.RGBA()
	if r>>8 > 0x0f || g>>8 > 0x0f || b>>8 > 0x0f {
		t.Errorf("bottom-right = %v, want near-black", br)
	}

	if again := Frame(img, nil); !bytes.Equal(again, golden) {
		t.Errorf("Frame(Decode(x)) != x: first diff at %d", firstDiff(again, golden))
	}
}

func TestFrameScalesArbitrarySize(t *testing.T) {
	// A 600x600 solid image must still come out as one full-size frame.
	src := image.NewRGBA(image.Rect(0, 0, 600, 600))
	for i := range src.Pix {
		src.Pix[i] = 0xFF
	}
	got := Frame(src, nil)
	if len(got) != FrameBytes {
		t.Fatalf("frame is %d bytes, want %d", len(got), FrameBytes)
	}
	// White -> RGB565 0xFFFF.
	if got[0] != 0xFF || got[1] != 0xFF {
		t.Fatalf("first pixel = %02x%02x, want ffff", got[0], got[1])
	}
}

func TestHasAlpha(t *testing.T) {
	opaque := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range opaque.Pix {
		opaque.Pix[i] = 0xff
	}
	if HasAlpha(opaque) {
		t.Errorf("fully opaque RGBA reported as having alpha")
	}

	holed := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := range holed.Pix {
		holed.Pix[i] = 0xff
	}
	holed.SetNRGBA(1, 1, color.NRGBA{}) // one transparent pixel
	if !HasAlpha(holed) {
		t.Errorf("NRGBA with a transparent pixel reported as opaque")
	}
}

// A source with transparency must take the matte colour where it is see-through,
// and a nil matte must fill those pixels with black (the historical behaviour).
func TestFrameMattesTransparentPixels(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			if x < Width/2 {
				src.SetNRGBA(x, y, color.NRGBA{R: 0xff, A: 0xff}) // opaque red
			}
			// right half left at the zero value: fully transparent
		}
	}

	px := func(f []byte, x, y int) uint16 {
		o := (y*Width + x) * 2
		return uint16(f[o])<<8 | uint16(f[o+1])
	}

	white := Frame(src, color.White)
	if got := px(white, 10, 10); got != 0xF800 {
		t.Errorf("opaque-red pixel = %04x, want f800", got)
	}
	if got := px(white, Width-10, 10); got != 0xFFFF {
		t.Errorf("transparent pixel over white matte = %04x, want ffff", got)
	}

	black := Frame(src, nil)
	if got := px(black, Width-10, 10); got != 0x0000 {
		t.Errorf("transparent pixel over nil (black) matte = %04x, want 0000", got)
	}
}

func TestFramesReportsPNGTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x80}) // half-alpha
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	_, transparent, err := Frames(bytes.NewReader(buf.Bytes()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !transparent {
		t.Errorf("PNG with partial alpha reported as opaque")
	}
}
