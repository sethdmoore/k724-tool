// Package screen turns ordinary images into the K724-RGB-PRO's TFT frame
// format: 240x135 big-endian RGB565, row-major, top-left origin, 64800 bytes
// per frame. It has no cgo and no hardware dependency, so it unit-tests
// anywhere. See docs/SCREEN.md for the wire format.
package screen

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"

	xdraw "golang.org/x/image/draw"

	// Register the decoders image.Decode needs.
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
)

// Dimensions of one frame, mirrored from internal/protocol to keep this
// package import-free of it.
const (
	Width      = 240
	Height     = 135
	FrameBytes = Width * Height * 2
)

// Frame scales img to Width x Height (preserving aspect ratio, centre-cropping
// the overflow) and encodes it as one big-endian RGB565 frame of exactly
// FrameBytes bytes.
//
// The TFT is opaque: it has no alpha channel. If img has any transparency it is
// composited over matte first, so a transparent pixel takes the matte colour
// rather than silently becoming black. A nil matte means opaque black, which
// reproduces the old behaviour byte-for-byte. Fully opaque images ignore matte.
func Frame(img image.Image, matte color.Color) []byte {
	dst := image.NewRGBA(image.Rect(0, 0, Width, Height))

	op := xdraw.Src
	if HasAlpha(img) {
		// Lay the matte down first, then blend the (scaled) source over it so
		// partial alpha mixes correctly.
		xdraw.Draw(dst, dst.Bounds(), image.NewUniform(matteOr(matte)), image.Point{}, xdraw.Src)
		op = xdraw.Over
	}
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, centreCrop(img.Bounds()), op, nil)

	out := make([]byte, FrameBytes)
	o := 0
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			// dst is RGBA, non-premultiplied 8-bit per channel in Pix.
			i := dst.PixOffset(x, y)
			r, g, b := dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2]
			v := uint16(r&0xF8)<<8 | uint16(g&0xFC)<<3 | uint16(b>>3)
			out[o] = byte(v >> 8)
			out[o+1] = byte(v)
			o += 2
		}
	}
	return out
}

// Decode turns a FrameBytes big-endian RGB565 buffer back into an image. Use
// it to preview exactly what the device will display: the centre-crop and the
// 16-bit colour reduction are both already baked into the buffer. A short
// buffer yields a blank image.
func Decode(frame []byte) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	if len(frame) < FrameBytes {
		return img
	}
	for i := 0; i < Width*Height; i++ {
		v := uint16(frame[i*2])<<8 | uint16(frame[i*2+1])
		r5, g6, b5 := (v>>11)&0x1f, (v>>5)&0x3f, v&0x1f
		o := i * 4
		img.Pix[o+0] = byte(r5<<3 | r5>>2) // replicate high bits into the low ones
		img.Pix[o+1] = byte(g6<<2 | g6>>4)
		img.Pix[o+2] = byte(b5<<3 | b5>>2)
		img.Pix[o+3] = 0xff
	}
	return img
}

// HasAlpha reports whether img has any pixel that is not fully opaque. It is
// used to decide whether a matte colour is needed before flattening to RGB565.
func HasAlpha(img image.Image) bool {
	// Most stdlib image types carry a cheap-ish Opaque().
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return true
			}
		}
	}
	return false
}

func matteOr(c color.Color) color.Color {
	if c == nil {
		return color.Black
	}
	return c
}

// centreCrop returns the largest sub-rectangle of b that has the Width:Height
// aspect ratio, centred within b.
func centreCrop(b image.Rectangle) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return b
	}
	// Compare w/h against Width/Height without floating point:
	// w*Height vs h*Width.
	switch {
	case w*Height > h*Width: // source is too wide -> trim the sides
		nw := h * Width / Height
		x0 := b.Min.X + (w-nw)/2
		return image.Rect(x0, b.Min.Y, x0+nw, b.Max.Y)
	case w*Height < h*Width: // source is too tall -> trim top and bottom
		nh := w * Height / Width
		y0 := b.Min.Y + (h-nh)/2
		return image.Rect(b.Min.X, y0, b.Max.X, y0+nh)
	default:
		return b
	}
}

// DecodeFrames decodes an image stream into one or more native-size source
// frames, ready to hand to Frame with a chosen matte. PNG, JPEG and BMP yield a
// single frame. GIF yields one frame per animation frame, each composited over
// the running canvas so partial-frame GIFs render correctly. The bool reports
// whether any frame carried transparency (i.e. whether the matte will matter).
func DecodeFrames(r io.Reader) ([]image.Image, bool, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, false, err
	}
	if isGIF(data) {
		return gifSources(data)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("screen: decode image: %w", err)
	}
	return []image.Image{img}, HasAlpha(img), nil
}

// Frames is a convenience wrapper: DecodeFrames followed by Frame on every
// source with the given matte (nil = opaque black). The bool reports whether
// the source carried transparency.
func Frames(r io.Reader, matte color.Color) ([][]byte, bool, error) {
	srcs, transparent, err := DecodeFrames(r)
	if err != nil {
		return nil, false, err
	}
	out := make([][]byte, len(srcs))
	for i, s := range srcs {
		out[i] = Frame(s, matte)
	}
	return out, transparent, nil
}

func isGIF(b []byte) bool {
	return len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a")
}

func gifSources(data []byte) ([]image.Image, bool, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("screen: decode gif: %w", err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
	out := make([]image.Image, 0, len(g.Image))
	transparent := false
	for i, src := range g.Image {
		b := src.Bounds()
		xdraw.Draw(canvas, b, src, b.Min, xdraw.Over)
		if !transparent && HasAlpha(canvas) {
			transparent = true
		}
		// Snapshot: later frames keep mutating canvas, and flattening is
		// deferred until the caller picks a matte.
		snap := image.NewRGBA(canvas.Bounds())
		copy(snap.Pix, canvas.Pix)
		out = append(out, snap)
		if i < len(g.Disposal) && g.Disposal[i] == gif.DisposalBackground {
			xdraw.Draw(canvas, b, image.Transparent, image.Point{}, xdraw.Src)
		}
	}
	return out, transparent, nil
}
