package main

import (
	"encoding/hex"
	"image/color"
	"testing"

	"k724tool/internal/protocol"
)

func TestEffectiveScalesByAlpha(t *testing.T) {
	cases := []struct {
		in   color.NRGBA
		want color.NRGBA
	}{
		{color.NRGBA{R: 0xff, A: 0xff}, color.NRGBA{R: 0xff, A: 0xff}},
		{color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x00}, color.NRGBA{A: 0xff}},
		{color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 128}, color.NRGBA{R: 128, G: 128, B: 128, A: 0xff}},
	}
	for _, c := range cases {
		if got := effective(c.in); got != c.want {
			t.Errorf("effective(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestKeyRGBARoundTrip(t *testing.T) {
	var keys [protocol.KeyColorCount]color.NRGBA
	for i := range keys {
		keys[i] = color.NRGBA{A: 0xff}
	}
	keys[49] = color.NRGBA{R: 0xff, A: 0xff}
	keys[50] = color.NRGBA{G: 0xff, A: 128}
	keys[33] = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	got, ok := decodeKeys(encodeKeys(keys))
	if !ok {
		t.Fatal("decodeKeys rejected its own encoding")
	}
	if got != keys {
		t.Fatalf("round-trip mismatch at 48..52:\n got %v\nwant %v", got[48:52], keys[48:52])
	}
}

func TestDecodeKeysAcceptsLegacyRGBTable(t *testing.T) {
	var tbl protocol.KeyColorTable
	tbl.SetRGB(49, 0x10, 0x20, 0x30)
	keys, ok := decodeKeys(hex.EncodeToString(tbl.Bytes()))
	if !ok {
		t.Fatal("legacy 384-byte RGB table rejected")
	}
	if keys[49] != (color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff}) {
		t.Fatalf("legacy entry = %v", keys[49])
	}
}

func TestWireTablePremultiplies(t *testing.T) {
	g := &keyGrid{}
	g.setIndex(50, color.NRGBA{G: 0xff, A: 128}) // half-bright green
	g.setIndex(49, color.NRGBA{R: 0xff, A: 0xff})
	if e := g.wireTable()[50]; e != [3]byte{0, 128, 0} {
		t.Fatalf("wire entry 50 = %v, want [0 128 0]", e)
	}
	if e := g.wireTable()[49]; e != [3]byte{0xff, 0, 0} {
		t.Fatalf("wire entry 49 = %v, want [255 0 0]", e)
	}
}
