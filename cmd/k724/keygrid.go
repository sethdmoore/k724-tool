package main

import (
	"encoding/hex"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"k724tool/internal/protocol"
)

// unitPx is the on-screen width of one quarter-unit of key width; a standard
// 4-quarter key is 4*unitPx wide. keyRowPx is a key's height.
const (
	unitPx   = float32(8.5)
	keyRowPx = float32(28)
)

// paintState is shared by every keyCell in a keyGrid so that, while the
// primary mouse button is held down over one cell and the pointer is then
// dragged onto another, the newly-entered cell can tell that it should paint
// itself too — like a brush/paint-bucket drag instead of one click at a time.
type paintState struct {
	down bool
}

// keyCell is a single clickable, colour-filled key in the per-key editor grid.
type keyCell struct {
	widget.BaseWidget
	label string
	units int
	fill  color.NRGBA
	onTap func()
	paint *paintState

	rect *canvas.Rectangle
	text *canvas.Text
}

func newKeyCell(label string, units int, onTap func(), paint *paintState) *keyCell {
	c := &keyCell{label: label, units: units, onTap: onTap, paint: paint, fill: color.NRGBA{A: 0xff}}
	c.rect = canvas.NewRectangle(c.fill)
	c.rect.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	c.rect.StrokeWidth = 1
	c.rect.CornerRadius = 3
	c.text = canvas.NewText(label, textOn(c.fill))
	c.text.Alignment = fyne.TextAlignCenter
	c.text.TextSize = theme.CaptionTextSize()
	c.ExtendBaseWidget(c)
	return c
}

// setFill repaints the cell with col and picks a readable label colour.
func (c *keyCell) setFill(col color.NRGBA) {
	c.fill = col
	c.rect.FillColor = col
	c.text.Color = textOn(col)
	c.rect.Refresh()
	c.text.Refresh()
}

func (c *keyCell) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

// MouseDown/MouseUp (desktop.Mouseable) and MouseIn/MouseMoved/MouseOut
// (desktop.Hoverable) implement drag-to-paint: holding the primary button
// down and dragging across cells paints each one as the pointer enters it,
// rather than requiring a separate click per key. Tapped above is left as-is
// for a plain click.
func (c *keyCell) MouseDown(evt *desktop.MouseEvent) {
	if evt.Button != desktop.MouseButtonPrimary {
		return
	}
	if c.paint != nil {
		c.paint.down = true
	}
	if c.onTap != nil {
		c.onTap()
	}
}

// MouseUp is a second line of defense for the plain-click case (no
// Dragged/DragEnd ever fires there, since the pointer never crosses the
// driver's drag-move threshold). See Dragged/DragEnd below for the reliable
// release signal that covers the drag case, including releases outside the
// grid or the window.
func (c *keyCell) MouseUp(*desktop.MouseEvent) {
	if c.paint != nil {
		c.paint.down = false
	}
}

func (c *keyCell) MouseIn(*desktop.MouseEvent) {
	if c.paint != nil && c.paint.down && c.onTap != nil {
		c.onTap()
	}
}

func (c *keyCell) MouseMoved(*desktop.MouseEvent) {}

func (c *keyCell) MouseOut() {}

// Dragged/DragEnd (fyne.Draggable) exist purely so the driver's
// button-release handling gives us a guaranteed DragEnd callback on the
// originating cell once a drag is underway — unlike MouseUp, which is
// dispatched by hit-testing the cursor's position at release time and so
// never fires if the button is released outside the grid, or outside the
// window entirely. Dragged is intentionally a no-op: unlike frameCard on the
// Screen tab, which moves itself for reorder feedback, these cells must stay
// put in the grid layout — painting itself still happens only via
// MouseDown/MouseIn.
func (c *keyCell) Dragged(*fyne.DragEvent) {}

func (c *keyCell) DragEnd() {
	if c.paint != nil {
		c.paint.down = false
	}
}

func (c *keyCell) MinSize() fyne.Size {
	return fyne.NewSize(float32(c.units)*unitPx, keyRowPx)
}

func (c *keyCell) CreateRenderer() fyne.WidgetRenderer {
	return &keyCellRenderer{c: c, objs: []fyne.CanvasObject{c.rect, c.text}}
}

type keyCellRenderer struct {
	c    *keyCell
	objs []fyne.CanvasObject
}

func (r *keyCellRenderer) Layout(s fyne.Size) {
	r.c.rect.Resize(s)
	r.c.rect.Move(fyne.NewPos(0, 0))
	th := r.c.text.MinSize().Height
	r.c.text.Resize(fyne.NewSize(s.Width, th))
	r.c.text.Move(fyne.NewPos(0, (s.Height-th)/2))
}
func (r *keyCellRenderer) MinSize() fyne.Size           { return r.c.MinSize() }
func (r *keyCellRenderer) Refresh()                     { r.c.rect.Refresh(); r.c.text.Refresh() }
func (r *keyCellRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *keyCellRenderer) Destroy()                     {}

// textOn returns black or white, whichever reads better on bg.
func textOn(bg color.NRGBA) color.Color {
	// Rec. 601 luma; treat near-black fill as "no colour" and use the theme fg.
	y := 0.299*float64(bg.R) + 0.587*float64(bg.G) + 0.114*float64(bg.B)
	if bg.R == 0 && bg.G == 0 && bg.B == 0 {
		return theme.Color(theme.ColorNameForeground)
	}
	if y < 140 {
		return color.White
	}
	return color.Black
}

// keyGrid is the editor grid plus the handles the Lighting tab needs to drive
// it. Each entry is stored as an NRGBA whose RGB is the key's hue and whose
// A is that key's own brightness (0..255) — the Windows app keeps the same
// per-key alpha in CustomLightMode.LightColorInfo. The 0x0b table on the wire
// is only 3 bytes per key, so wireTable folds the alpha into the RGB.
type keyGrid struct {
	root  *fyne.Container
	cells map[int]*keyCell
	keys  [protocol.KeyColorCount]color.NRGBA
}

// newKeyGrid builds the grid from protocol.KeyboardLayout. onPaint(idx) fires
// when a key is clicked; the caller decides what colour to drop and calls
// setIndex.
func newKeyGrid(onPaint func(idx int)) *keyGrid {
	g := &keyGrid{cells: map[int]*keyCell{}}
	for i := range g.keys {
		g.keys[i] = color.NRGBA{A: 0xff} // off, full brightness
	}
	// Shared across every cell so a drag started on one cell is recognised
	// by the cells it's dragged onto next. See keyCell's MouseDown/MouseIn.
	paint := &paintState{}
	rows := make([]fyne.CanvasObject, 0, len(protocol.KeyboardLayout))
	for _, row := range protocol.KeyboardLayout {
		cols := make([]fyne.CanvasObject, 0, len(row))
		for _, k := range row {
			if k.Index < 0 { // spacer
				sp := canvas.NewRectangle(color.Transparent)
				sp.SetMinSize(fyne.NewSize(float32(k.Units)*unitPx, keyRowPx))
				cols = append(cols, sp)
				continue
			}
			idx := k.Index
			cell := newKeyCell(k.Name, k.Units, func() { onPaint(idx) }, paint)
			g.cells[idx] = cell
			cols = append(cols, cell)
		}
		rows = append(rows, container.NewHBox(cols...))
	}
	g.root = container.NewVBox(rows...)
	return g
}

// effective returns the colour a key actually shows: its RGB scaled by its own
// brightness (alpha), returned opaque.
func effective(c color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: uint8(uint16(c.R) * uint16(c.A) / 255),
		G: uint8(uint16(c.G) * uint16(c.A) / 255),
		B: uint8(uint16(c.B) * uint16(c.A) / 255),
		A: 0xff,
	}
}

// setIndex stores one key's colour+brightness and repaints its cell.
func (g *keyGrid) setIndex(idx int, c color.NRGBA) {
	if idx < 0 || idx >= len(g.keys) {
		return
	}
	g.keys[idx] = c
	if cell := g.cells[idx]; cell != nil {
		cell.setFill(effective(c))
	}
}

// fill sets every editable key to c.
func (g *keyGrid) fill(c color.NRGBA) {
	for idx := range g.cells {
		g.setIndex(idx, c)
	}
}

// load replaces the whole model and repaints every cell.
func (g *keyGrid) load(keys [protocol.KeyColorCount]color.NRGBA) {
	g.keys = keys
	for idx, cell := range g.cells {
		cell.setFill(effective(g.keys[idx]))
	}
}

// wireTable is the 0x0b table to send: each key's RGB pre-multiplied by its
// brightness, exactly as the Windows app flattens its per-key alpha.
func (g *keyGrid) wireTable() protocol.KeyColorTable {
	var t protocol.KeyColorTable
	for i, c := range g.keys {
		e := effective(c)
		t.SetRGB(i, e.R, e.G, e.B)
	}
	return t
}

// encodeKeys / decodeKeys serialise the RGBA model for fyne.Preferences.
func encodeKeys(keys [protocol.KeyColorCount]color.NRGBA) string {
	b := make([]byte, 0, protocol.KeyColorCount*4)
	for _, c := range keys {
		b = append(b, c.R, c.G, c.B, c.A)
	}
	return hex.EncodeToString(b)
}

func decodeKeys(s string) ([protocol.KeyColorCount]color.NRGBA, bool) {
	var keys [protocol.KeyColorCount]color.NRGBA
	b, err := hex.DecodeString(s)
	if err != nil {
		return keys, false
	}
	switch len(b) {
	case protocol.KeyColorCount * 4: // RGBA model
		for i := range keys {
			keys[i] = color.NRGBA{R: b[i*4], G: b[i*4+1], B: b[i*4+2], A: b[i*4+3]}
		}
	case protocol.KeyColorTableLen: // legacy RGB-only table: full brightness
		for i := range keys {
			keys[i] = color.NRGBA{R: b[i*3], G: b[i*3+1], B: b[i*3+2], A: 0xff}
		}
	default:
		return keys, false
	}
	return keys, true
}
