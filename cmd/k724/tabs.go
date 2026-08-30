package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"k724tool/internal/applog"
	"k724tool/internal/k724"
	"k724tool/internal/protocol"
	"k724tool/internal/screen"
)

const clockLayout = "2006-01-02 15:04:05"

// prefKeyTable is the fyne.Preferences key holding the hex-encoded 384-byte
// per-key colour table. The keyboard cannot report the table back, so the tool
// keeps the authoritative copy here.
const prefKeyTable = "customKeyTable"

// ---------------------------------------------------------------- Clock tab

func (a *App) buildClockTab() fyne.CanvasObject {
	deviceTime := widget.NewLabel("device clock: —")

	entry := widget.NewEntry()
	entry.SetPlaceHolder(clockLayout)

	syncBtn := widget.NewButton("Sync to system time", func() {
		a.runOnDevice("sync clock", func(d *k724.Device) error { return d.SyncClock() })
	})
	setBtn := widget.NewButton("Set entered time", func() {
		t, err := time.ParseInLocation(clockLayout, strings.TrimSpace(entry.Text), time.Local)
		if err != nil {
			dialog.ShowError(fmt.Errorf("time must look like %q", clockLayout), a.win)
			return
		}
		a.runOnDevice("set clock", func(d *k724.Device) error { return d.SetClock(t) })
	})
	fillBtn := widget.NewButton("Fill with system time", func() {
		entry.SetText(time.Now().Format(clockLayout))
	})

	a.onSettings = append(a.onSettings, func(b protocol.SettingsBlock) {
		t := b.Time(time.Local)
		deviceTime.SetText("device clock: " + t.Format("Mon "+clockLayout))
		if strings.TrimSpace(entry.Text) == "" {
			entry.SetText(t.Format(clockLayout))
		}
	})
	connected := false
	refreshControls := func() { toggle(connected && !a.busy, syncBtn, setBtn) }
	a.onConnState = append(a.onConnState, func(c bool, _ k724.Target) {
		connected = c
		refreshControls()
		if !c {
			deviceTime.SetText("device clock: —")
		}
	})
	a.onBusy = append(a.onBusy, func(bool) { refreshControls() })
	refreshControls()

	hint := wrapLabel("Both buttons do a read-modify-write: only the timestamp " +
		"field of the settings block changes, so lighting, polling and screen " +
		"settings keep their values. Safe on wired and wireless.")

	return container.NewVBox(
		title("Onboard clock"),
		deviceTime,
		syncBtn,
		widget.NewSeparator(),
		widget.NewLabel("Or set a specific time:"),
		container.NewBorder(nil, nil, nil, fillBtn, entry),
		setBtn,
		widget.NewSeparator(),
		hint,
	)
}

// -------------------------------------------------------------- Lighting tab

func (a *App) buildLightingTab() fyne.CanvasObject {
	haveBlock := false
	colorPicked := false

	effect := widget.NewSelect(effectNames(), nil)
	effect.PlaceHolder = "(effect)"

	brightness := widget.NewSlider(0, 5)
	brightness.Step = 1
	brightnessVal := widget.NewLabel("0")
	brightness.OnChanged = func(v float64) { brightnessVal.SetText(fmt.Sprintf("%d", int(v))) }

	speed := widget.NewSlider(0, 5)
	speed.Step = 1
	speedVal := widget.NewLabel("0")
	speed.OnChanged = func(v float64) { speedVal.SetText(fmt.Sprintf("%d", int(v))) }

	// The device's raw speed byte is actually more like a delay/period: a
	// larger raw value animates *slower*. The slider is meant to read
	// "right = faster" (matching the Brightness slider's low-left/high-right
	// feel), so invert between the slider's displayed value and the raw
	// byte written to / read from the device. Self-inverse, so the same
	// helper works both directions.
	invSpeed := func(v int) int { return 5 - v }

	current := color.NRGBA{A: 0xff}
	swatch := canvas.NewRectangle(current)
	swatch.SetMinSize(fyne.NewSize(48, 24))
	pick := widget.NewButton("Pick colour…", func() {
		a.pickColour("Custom colour", "Applied to every key", current, func(nc color.NRGBA) {
			current = nc
			colorPicked = true
			swatch.FillColor = current
			swatch.Refresh()
		})
	})

	apply := widget.NewButton("Apply changes", func() {
		wantEffect, okEffect := effectIDForName(effect.Selected)
		wantBr, wantSp := int(brightness.Value), invSpeed(int(speed.Value))
		col, picked := current, colorPicked
		applog.Infof("lighting apply: want effect=%q(%d ok=%v) brightness=%d speed=%d colourPicked=%v(%02x%02x%02x)",
			effect.Selected, wantEffect, okEffect, wantBr, wantSp, picked, col.R, col.G, col.B)
		a.runOnDevice("apply lighting", func(d *k724.Device) error {
			return d.ApplySettings(func(b *protocol.SettingsBlock) {
				// Only touch a field whose intended value differs from what
				// the device currently has — never blind-write, especially
				// not the colour bytes (that is the all-keys-white bug).
				if okEffect && wantEffect != b.Effect() {
					applog.Infof("lighting apply: effect %d -> %d", b.Effect(), wantEffect)
					b.SetEffect(wantEffect)
				}
				if wantBr != b.Brightness() {
					applog.Infof("lighting apply: brightness %d -> %d", b.Brightness(), wantBr)
					b.SetBrightness(wantBr)
				}
				if wantSp != b.Speed() {
					applog.Infof("lighting apply: speed %d -> %d", b.Speed(), wantSp)
					b.SetSpeed(wantSp)
				}
				if picked {
					r, g, bl := b.Color()
					applog.Infof("lighting apply: colour %02x%02x%02x -> %02x%02x%02x", r, g, bl, col.R, col.G, col.B)
					b.SetColor(col.R, col.G, col.B)
				} else {
					r, g, bl := b.Color()
					applog.Infof("lighting apply: colour left as-is on device = %02x%02x%02x", r, g, bl)
				}
			})
		})
	})

	connected := false
	refreshControls := func() {
		on := connected && !a.busy
		toggle(on, effect, brightness, speed, pick)
		if on && haveBlock {
			apply.Enable()
		} else {
			apply.Disable()
		}
	}

	a.onSettings = append(a.onSettings, func(b protocol.SettingsBlock) {
		haveBlock = true
		colorPicked = false
		if n := effectNameForID(b.Effect()); n != "" {
			effect.SetSelected(n)
		} else {
			effect.ClearSelected()
		}
		brightness.SetValue(float64(b.Brightness()))
		speed.SetValue(float64(invSpeed(b.Speed())))
		r, g, bl := b.Color()
		current = color.NRGBA{R: r, G: g, B: bl, A: 0xff}
		swatch.FillColor = current
		swatch.Refresh()
		refreshControls()
	})
	a.onConnState = append(a.onConnState, func(c bool, _ k724.Target) {
		connected = c
		refreshControls()
	})
	a.onBusy = append(a.onBusy, func(bool) { refreshControls() })
	refreshControls()

	form := container.New(layout.NewFormLayout(),
		widget.NewLabel("Effect"), effect,
		widget.NewLabel("Brightness"), container.NewBorder(nil, nil, nil, brightnessVal, brightness),
		widget.NewLabel("Speed"), container.NewBorder(nil, nil, nil, speedVal, speed),
		widget.NewLabel("Colour"), container.NewHBox(swatch, pick),
	)

	// ---- per-key colour editor (the "Custom" effect) --------------------

	prefs := a.fyneApp.Preferences()

	// The paint "brush": a hue plus its own brightness (0-100 %). Clicking a
	// key stores both — RGB in the colour, brightness in the alpha — matching
	// the per-key alpha the Windows app keeps. paintEffective is what actually
	// gets dropped and shown.
	paintHue := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	paintBright := 100
	paintEffective := func() color.NRGBA {
		return effective(color.NRGBA{R: paintHue.R, G: paintHue.G, B: paintHue.B, A: uint8(paintBright * 255 / 100)})
	}
	paintSwatch := canvas.NewRectangle(paintEffective())
	paintSwatch.SetMinSize(fyne.NewSize(48, 24))
	refreshPaintSwatch := func() {
		paintSwatch.FillColor = paintEffective()
		paintSwatch.Refresh()
	}

	var grid *keyGrid
	saveTable := func() { prefs.SetString(prefKeyTable, encodeKeys(grid.keys)) }
	brush := func() color.NRGBA {
		return color.NRGBA{R: paintHue.R, G: paintHue.G, B: paintHue.B, A: uint8(paintBright * 255 / 100)}
	}

	grid = newKeyGrid(func(idx int) {
		grid.setIndex(idx, brush())
		saveTable()
	})
	if keys, ok := decodeKeys(prefs.String(prefKeyTable)); ok {
		grid.load(keys)
	}

	paintPick := widget.NewButton("Paint colour…", func() {
		a.pickColour("Paint colour", "Click keys in the grid to paint them", paintHue, func(nc color.NRGBA) {
			paintHue = nc
			refreshPaintSwatch()
		})
	})

	paintBrightSlider := widget.NewSlider(0, 100)
	paintBrightSlider.Step = 5
	paintBrightSlider.Value = float64(paintBright)
	paintBrightVal := widget.NewLabel("100%")
	paintBrightSlider.OnChanged = func(v float64) {
		paintBright = int(v)
		paintBrightVal.SetText(fmt.Sprintf("%d%%", paintBright))
		refreshPaintSwatch()
	}

	fillAll := widget.NewButton("Fill all", func() { grid.fill(brush()); saveTable() })
	clearAll := widget.NewButton("Clear (off)", func() {
		grid.fill(color.NRGBA{A: 0xff})
		saveTable()
	})

	applyKeys := widget.NewButton("Apply per-key colours", func() {
		tbl := grid.wireTable()
		pe := paintEffective()
		primary := [3]byte{pe.R, pe.G, pe.B}
		a.runOnDevice("apply per-key colours", func(d *k724.Device) error {
			return d.ApplyKeyColors(tbl, primary)
		})
	})
	applyKeys.Importance = widget.HighImportance

	perKeyControls := []fyne.Disableable{paintPick, paintBrightSlider, fillAll, clearAll, applyKeys}
	refreshPerKey := func() {
		toggle(connected && !a.busy, perKeyControls...)
	}
	a.onConnState = append(a.onConnState, func(bool, k724.Target) { refreshPerKey() })
	a.onBusy = append(a.onBusy, func(bool) { refreshPerKey() })
	refreshPerKey()

	perKey := container.NewVBox(
		title("Per-key colours"),
		wrapLabel("The “Custom” effect shows this table. Set a paint colour and its "+
			"brightness, click keys to paint them, then Apply — the tool writes the "+
			"128-entry table and switches the keyboard to Custom. Each key keeps its own "+
			"brightness (folded into its colour on the wire, like the Windows app). The "+
			"table is remembered between sessions — the keyboard can't report it back. "+
			"Wired only."),
		container.NewHBox(
			widget.NewLabel("Paint"), paintSwatch, paintPick,
			widget.NewLabel("Brightness"), paintBrightVal,
		),
		paintBrightSlider,
		container.NewHBox(fillAll, clearAll),
		container.NewHScroll(grid.root),
		applyKeys,
	)

	body := container.NewVBox(
		title("Lighting"),
		form,
		apply,
		wrapLabel("Loads the keyboard's current values on connect. Apply writes back only "+
			"the controls you changed. Over the 2.4 GHz receiver the keyboard may ignore "+
			"these; they are a wired feature."),
		widget.NewSeparator(),
		perKey,
	)
	return container.NewScroll(body)
}

// --------------------------------------------------------------- Polling tab

func (a *App) buildPollingTab() fyne.CanvasObject {
	labels := make([]string, len(protocol.PollingRates))
	for i, hz := range protocol.PollingRates {
		labels[i] = fmt.Sprintf("%d Hz", hz)
	}
	sel := widget.NewSelect(labels, nil)
	sel.PlaceHolder = "(rate)"

	apply := widget.NewButton("Apply polling rate", func() {
		idx := sel.SelectedIndex()
		if idx < 0 {
			return
		}
		a.runOnDevice("apply polling rate", func(d *k724.Device) error {
			return d.ApplySettings(func(b *protocol.SettingsBlock) {
				if idx != b.PollingIndex() {
					b.SetPollingIndex(idx)
				}
			})
		})
	})

	a.onSettings = append(a.onSettings, func(b protocol.SettingsBlock) {
		if i := b.PollingIndex(); i >= 0 && i < len(labels) {
			sel.SetSelectedIndex(i)
		}
	})
	connected := false
	refreshControls := func() { toggle(connected && !a.busy, sel, apply) }
	a.onConnState = append(a.onConnState, func(c bool, _ k724.Target) {
		connected = c
		refreshControls()
	})
	a.onBusy = append(a.onBusy, func(bool) { refreshControls() })
	refreshControls()

	return container.NewVBox(
		title("USB polling rate"),
		sel,
		apply,
	)
}

// ---------------------------------------------------------------- Screen tab

type frameItem struct {
	name string
	src  image.Image // native-size source (GIF frames already composited)
	data []byte      // screen.FrameCrop(src, matte, crop); rebuilt when matte or crop changes

	// crop is this frame's own (zoom, panX, panY) triple, only meaningful in
	// "individual frames" mode (see cropLinked in buildScreenTab) and only
	// once cropSet is true — an added frame starts without one, and reencode
	// falls back to screen.CentreCrop's equivalent (zoom=1, pan centred) for
	// it, same as before this feature existed. cropSet exists as a separate
	// bool rather than using a zero triple as "unset" because zoom=0 would
	// itself need clamping to 1 anyway, so it can't double as a sentinel.
	crop    cropState
	cropSet bool
}

// cropState is a zoom/pan triple, converted to a concrete crop rectangle via
// screen.CropAt at reencode time. Kept as (zoom, panX, panY) rather than a
// resolved image.Rectangle so it survives being carried across images of
// different native sizes unchanged (e.g. restoring a frame from the trash),
// and so the slider UI has something stable to read back.
type cropState struct {
	zoom, panX, panY float64
}

// defaultCrop is CentreCrop's equivalent in (zoom, panX, panY) terms: no zoom,
// centred pan.
var defaultCrop = cropState{zoom: 1, panX: 0.5, panY: 0.5}

const maxFrames = 25

// frameCard is one thumbnail card on the Screen tab's timeline. It implements
// fyne.Draggable so a frame can be dragged left/right to reorder it among its
// siblings, or dragged onto the trash drop zone (trashArea) to remove it —
// see the onDrag closure built in buildScreenTab's rebuildList, which owns
// the frames/trash slices and decides what a given gesture meant. frameCard
// itself just reports the gesture; it holds no frame-list state of its own.
//
// While a drag is in progress, frameCard also pushes a lightweight "ghost"
// copy of itself onto the window's canvas overlay stack (hostCanvas) so it
// always paints above every sibling card — container.NewHBox paints strictly
// in Objects() order, which never changes mid-drag, so without an overlay a
// card dragged toward a higher-index sibling would render underneath it.
type frameCard struct {
	widget.BaseWidget
	body fyne.CanvasObject

	// hostCanvas, thumb and frameNum are only used to build and place the
	// drag ghost (see buildGhost/Dragged). hostCanvas may be nil, in which
	// case dragging falls back to the plain Move()-only behaviour.
	hostCanvas fyne.Canvas
	thumb      image.Image // same pixels as the card's thumbnail
	frameNum   int         // 1-based position, shown on the card and its ghost

	// onDrag, if set, fires once per drag gesture (at DragEnd) with the
	// total delta accumulated since the drag started and the pointer's
	// final absolute (screen) position.
	onDrag func(dx, dy float32, absPos fyne.Position)
	// onDragMove, if set, fires on every Dragged event (including the first
	// of a gesture) with the pointer's current absolute position — used to
	// drive the trash-drop-zone highlight.
	onDragMove func(absPos fyne.Position)
	// onTap, if set, fires for a plain click (no drag) on the card body.
	onTap func()

	dragging bool
	origPos  fyne.Position
	dx, dy   float32
	lastAbs  fyne.Position

	ghost       fyne.CanvasObject
	ghostOrigin fyne.Position
}

func newFrameCard(body fyne.CanvasObject, hostCanvas fyne.Canvas, thumb image.Image, frameNum int,
	onDragMove func(absPos fyne.Position), onDrag func(dx, dy float32, absPos fyne.Position), onTap func()) *frameCard {
	c := &frameCard{
		body:       body,
		hostCanvas: hostCanvas,
		thumb:      thumb,
		frameNum:   frameNum,
		onDragMove: onDragMove,
		onDrag:     onDrag,
		onTap:      onTap,
	}
	c.ExtendBaseWidget(c)
	return c
}

func (c *frameCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.body)
}

// Tapped implements fyne.Tappable.
func (c *frameCard) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

// buildGhost builds a small detached copy of the card's visual (same
// thumbnail pixels, same frame-number label) for the drag overlay. It's a
// fresh CanvasObject rather than a reference to c.body because a CanvasObject
// can only belong to one place in the render tree at a time — c.body stays
// put in listBox for the whole drag.
func (c *frameCard) buildGhost() fyne.CanvasObject {
	gi := canvas.NewImageFromImage(c.thumb)
	gi.FillMode = canvas.ImageFillContain
	gi.ScaleMode = canvas.ImageScalePixels
	gi.SetMinSize(fyne.NewSize(72, 40))
	return container.NewVBox(
		widget.NewLabelWithStyle(fmt.Sprintf("%d", c.frameNum), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		gi,
	)
}

// Dragged implements fyne.Draggable. It nudges the card's own position for
// visual feedback as the pointer moves and accumulates the gesture's total
// delta; the timeline is rebuilt from scratch at DragEnd (via onDrag calling
// rebuildList), which snaps every card back to its laid-out position.
//
// On the first event of a gesture it also raises a ghost copy onto the
// canvas overlay stack — Fyne's mechanism for guaranteeing something paints
// above the whole window content, the same one dialogs/menus use — and keeps
// it positioned in lockstep with the card's own (locally-computed) movement,
// translated into absolute canvas coordinates via ghostOrigin.
func (c *frameCard) Dragged(ev *fyne.DragEvent) {
	if !c.dragging {
		c.dragging = true
		c.origPos = c.Position()
		c.dx, c.dy = 0, 0
		if c.hostCanvas != nil {
			c.ghost = c.buildGhost()
			c.hostCanvas.Overlays().Add(c.ghost)
			c.ghost.Resize(c.Size())
			c.ghostOrigin = fyne.CurrentApp().Driver().AbsolutePositionForObject(c)
			c.ghost.Move(c.ghostOrigin)
		}
	}
	c.dx += ev.Dragged.DX
	c.dy += ev.Dragged.DY
	c.lastAbs = ev.AbsolutePosition
	c.Move(c.origPos.AddXY(c.dx, c.dy))
	if c.ghost != nil {
		c.ghost.Move(c.ghostOrigin.AddXY(c.dx, c.dy))
	}
	if c.onDragMove != nil {
		c.onDragMove(ev.AbsolutePosition)
	}
}

// DragEnd implements fyne.Draggable.
func (c *frameCard) DragEnd() {
	c.dragging = false
	if c.ghost != nil {
		if c.hostCanvas != nil {
			c.hostCanvas.Overlays().Remove(c.ghost)
		}
		c.ghost = nil
	}
	if c.onDrag != nil {
		c.onDrag(c.dx, c.dy, c.lastAbs)
	}
	c.dx, c.dy = 0, 0
}

// moveFrame relocates the item at index from to index to (shifting the
// others along) and returns the updated slice. from and to must both be
// valid indices; it's a no-op if they're equal.
func moveFrame(frames []frameItem, from, to int) []frameItem {
	if from == to {
		return frames
	}
	item := frames[from]
	frames = append(frames[:from], frames[from+1:]...)
	frames = append(frames[:to], append([]frameItem{item}, frames[to:]...)...)
	return frames
}

// moveIndex reports where the frame currently at idx ends up after moving
// the item at from to to (same convention as moveFrame). Used to keep
// previewIdx pointing at the same frame across a reorder.
func moveIndex(idx, from, to int) int {
	switch {
	case idx == from:
		return to
	case from < to && idx > from && idx <= to:
		return idx - 1
	case from > to && idx >= to && idx < from:
		return idx + 1
	default:
		return idx
	}
}

// previewPan is a zero-visual widget that wraps the Screen tab's zoomed
// preview image purely to pick up fyne.Draggable events: canvas.Image is not
// itself a widget and doesn't implement Draggable, and its parent
// container.Scroll only reacts to the mouse wheel and its own scrollbar
// widgets (checked against fyne.io/fyne/v2/internal/widget/scroller.go — no
// click-drag handling there at all). CreateRenderer hands back body (the
// preview image) unchanged via widget.NewSimpleRenderer, the same technique
// frameCard uses to stay a thin shell around its own body.
type previewPan struct {
	widget.BaseWidget
	body   fyne.CanvasObject
	onDrag func(dx, dy float32)
}

func newPreviewPan(body fyne.CanvasObject, onDrag func(dx, dy float32)) *previewPan {
	p := &previewPan{body: body, onDrag: onDrag}
	p.ExtendBaseWidget(p)
	return p
}

func (p *previewPan) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(p.body)
}

// Dragged implements fyne.Draggable.
func (p *previewPan) Dragged(ev *fyne.DragEvent) {
	if p.onDrag != nil {
		p.onDrag(ev.Dragged.DX, ev.Dragged.DY)
	}
}

// DragEnd implements fyne.Draggable. There's no gesture-total state to settle
// here (unlike frameCard's reorder/delete decision) — each Dragged event
// already applied its own delta directly to the scroll offset.
func (p *previewPan) DragEnd() {}

// panPreview nudges s's viewport by one drag delta, in the same direction the
// pointer moved (a grab-and-drag feel, as if the image were being pushed
// around under a fixed window — the opposite sign from a scrollbar drag,
// which moves the *thumb* with the pointer and so moves the content the
// other way). s.Content is laid out by container.NewCenter, whose MinSize
// matches its child's (the preview image's) — see container.NewCenter and
// widget.Scroll.scrollBy, which both key off Content.MinSize() the same way.
//
// s.Offset is clamped by hand (mirroring widget.Scroll's own unexported
// computeOffset) because Scroll.ScrollToOffset, unlike the mouse-wheel path,
// does not clamp its argument at all. At low zoom, where the content already
// fits the viewport on an axis, the clamp pins that axis's offset to 0, so
// dragging is a harmless no-op exactly where there's nothing to pan.
func panPreview(s *container.Scroll, dx, dy float32) {
	if s == nil {
		return
	}
	inner := s.Content.MinSize()
	outer := s.Size()
	off := s.Offset
	off.X = clampScrollOffset(off.X-dx, outer.Width, inner.Width)
	off.Y = clampScrollOffset(off.Y-dy, outer.Height, inner.Height)
	s.ScrollToOffset(off)
}

// clampScrollOffset keeps a scroll offset within [0, inner-outer], matching
// widget.Scroll's own (unexported) computeOffset so hand-driven panning stays
// consistent with wheel-driven scrolling.
func clampScrollOffset(offset, outer, inner float32) float32 {
	if offset+outer >= inner {
		offset = inner - outer
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

func (a *App) buildScreenTab() fyne.CanvasObject {
	var frames []frameItem
	previewIdx := 0
	playing := false
	zoom := 3
	wiredReady := false

	// Background colour composited under transparent source pixels before the
	// frame is flattened to opaque RGB565. nil == opaque black, which matches
	// the behaviour before this control existed.
	var matte color.Color
	matteSeen := false // any added source carried transparency

	// Scale/crop. cropLinked true (the default, matching "the common case
	// for a GIF") means every frame is cropped from sharedCrop; false means
	// each frame uses its own frameItem.crop (falling back to defaultCrop
	// until it's been individually touched). See cropRectFor below, which is
	// the one place this policy is applied.
	cropLinked := true
	sharedCrop := defaultCrop
	// refreshCropControls is set in the controls section, where the
	// zoom/pan sliders live; updatePreview calls it (guarded, since it runs
	// before that section during setup) so the sliders always reflect
	// whichever frame is currently shown, the same one previewIdx tracks.
	var refreshCropControls func()

	blank := screen.Decode(nil)
	preview := canvas.NewImageFromImage(blank)
	preview.FillMode = canvas.ImageFillContain
	preview.ScaleMode = canvas.ImageScalePixels // nearest-neighbour: show real pixels
	preview.SetMinSize(fyne.NewSize(float32(screen.Width*zoom), float32(screen.Height*zoom)))
	// previewPan is a thin fyne.Draggable wrapper with no visual of its own
	// (see its definition below) so that click-and-drag over the preview
	// pans previewScroll instead of doing nothing — canvas.Image alone
	// doesn't implement Draggable, and container.Scroll only responds to
	// the mouse wheel and its own scrollbars, not a click-drag gesture.
	// previewScroll is declared (but not yet assigned) before previewDrag so
	// previewDrag's closure can capture it; it's only ever read once dragging
	// starts, well after the assignment below runs.
	var previewScroll *container.Scroll
	previewDrag := newPreviewPan(preview, func(dx, dy float32) {
		panPreview(previewScroll, dx, dy)
	})
	previewScroll = container.NewScroll(container.NewCenter(previewDrag))
	previewScroll.SetMinSize(fyne.NewSize(0, float32(screen.Height*3+16)))

	posLabel := widget.NewLabel("no frames")
	countLabel := widget.NewLabel(fmt.Sprintf("0 / %d frames", maxFrames))

	// The timeline runs left-to-right; each frame is a small vertical card.
	listBox := container.NewHBox()
	// Removed frames land here so they can be restored.
	var trash []frameItem
	trashBox := container.NewHBox()
	var rebuildTrash func()
	// trashArea is set in the layout section. It stays visible at all times
	// (even with nothing in it) because it doubles as the drag-to-trash drop
	// zone — see frameCard's onDrag closure below, which needs a stable,
	// always-present target to hit-test drops against.
	var trashArea *fyne.Container
	// setTrashHighlight is set in the layout section (where the highlight
	// visuals live). active is true for the whole time any card is being
	// dragged; hover narrows that to "the pointer is over trashArea right
	// now" for a stronger cue. rebuildList's per-card callbacks drive it.
	var setTrashHighlight func(active, hover bool)

	// Frame delay. The field on the wire is 16-bit (protocol.FrameIntervalMax),
	// so the slider covers the common fast range and the entry takes any exact
	// value up to the max (the Windows app's "Interval time" goes to 50000).
	// The floor is protocol.FrameIntervalMin (50 ms): the Windows app locks its
	// interval field to that minimum, and a test upload at 10 ms did not play
	// any faster on the device — the firmware appears to floor it too.
	intervalMS := 100
	interval := widget.NewSlider(protocol.FrameIntervalMin, 2000)
	interval.Step = 10
	interval.Value = 100
	intervalEntry := widget.NewEntry()
	intervalEntry.SetText("100")
	var setInterval func(v int, fromEntry bool)

	progress := widget.NewProgressBar()

	var upload, cancel, playBtn *widget.Button

	// --- helpers ---------------------------------------------------------

	refreshUpload := func() {
		if wiredReady && len(frames) > 0 && !a.busy {
			upload.Enable()
		} else {
			upload.Disable()
		}
	}
	updatePreview := func() {
		if len(frames) == 0 {
			preview.Image = blank
			posLabel.SetText("no frames")
		} else {
			if previewIdx >= len(frames) {
				previewIdx = len(frames) - 1
			}
			if previewIdx < 0 {
				previewIdx = 0
			}
			preview.Image = screen.Decode(frames[previewIdx].data)
			posLabel.SetText(fmt.Sprintf("frame %d / %d", previewIdx+1, len(frames)))
		}
		preview.Refresh()
		// Keep the crop sliders in step with whatever frame the preview is
		// now showing — the same frame individual-mode crop edits apply to.
		// Guarded because updatePreview can run (via rebuildList, called
		// below) before the controls section has assigned this.
		if refreshCropControls != nil {
			refreshCropControls()
		}
	}

	// cropRectFor resolves frame i's own concrete crop rectangle from the
	// current scale/crop policy: the shared triple when linked, or the
	// frame's own triple once it's had one explicitly set, or
	// defaultCrop (CentreCrop's equivalent) for a frame that hasn't.
	cropRectFor := func(i int) image.Rectangle {
		cs := defaultCrop
		switch {
		case cropLinked:
			cs = sharedCrop
		case frames[i].cropSet:
			cs = frames[i].crop
		}
		return screen.CropAt(frames[i].src.Bounds(), cs.zoom, cs.panX, cs.panY)
	}

	reencode := func() {
		for i := range frames {
			frames[i].data = screen.FrameCrop(frames[i].src, matte, cropRectFor(i))
		}
	}

	const trashCap = maxFrames

	var rebuildList func()
	rebuildList = func() {
		listBox.RemoveAll()
		for i := range frames {
			i := i
			th := canvas.NewImageFromImage(screen.Decode(frames[i].data))
			th.FillMode = canvas.ImageFillContain
			th.ScaleMode = canvas.ImageScalePixels
			th.SetMinSize(fyne.NewSize(72, 40))

			body := container.NewVBox(
				widget.NewLabelWithStyle(fmt.Sprintf("%d", i+1), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
				th,
			)

			// card is referenced from within its own onDrag closure (to read
			// its laid-out size once dragging is under way), so it has to be
			// declared before it's assigned.
			var card *frameCard
			card = newFrameCard(body, a.win.Canvas(), th.Image, i+1,
				func(absPos fyne.Position) {
					// Fires on every pointer move of the gesture (including
					// the first) — keeps the trash strip's highlight on for
					// the duration of the drag, and intensifies it while the
					// pointer is actually over the drop zone.
					hover := false
					if trashArea != nil {
						tp := fyne.CurrentApp().Driver().AbsolutePositionForObject(trashArea)
						ts := trashArea.Size()
						hover = absPos.X >= tp.X && absPos.X <= tp.X+ts.Width &&
							absPos.Y >= tp.Y && absPos.Y <= tp.Y+ts.Height
					}
					if setTrashHighlight != nil {
						setTrashHighlight(true, hover)
					}
				},
				func(dx, dy float32, absPos fyne.Position) {
					// Fires once at DragEnd, whatever the gesture resolved
					// to (reorder or trash-drop) — always clear the highlight.
					if setTrashHighlight != nil {
						setTrashHighlight(false, false)
					}
					if i >= len(frames) {
						return // stale closure: another drag already changed the list
					}
					// Dropped onto the trash zone?
					if trashArea != nil {
						tp := fyne.CurrentApp().Driver().AbsolutePositionForObject(trashArea)
						ts := trashArea.Size()
						if absPos.X >= tp.X && absPos.X <= tp.X+ts.Width &&
							absPos.Y >= tp.Y && absPos.Y <= tp.Y+ts.Height {
							removed := frames[i]
							frames = append(frames[:i], frames[i+1:]...)
							trash = append([]frameItem{removed}, trash...)
							if len(trash) > trashCap {
								trash = trash[:trashCap]
							}
							rebuildTrash()
							rebuildList()
							return
						}
					}
					// Otherwise, reorder: the whole-gesture horizontal delta,
					// divided by one card's on-screen footprint (plus the gap
					// between cards), gives the number of slots moved.
					slot := card.Size().Width + theme.Padding()
					if slot <= 0 {
						slot = 80
					}
					offset := int(math.Round(float64(dx) / float64(slot)))
					if offset == 0 {
						rebuildList() // no move — snap the dragged card back
						return
					}
					to := i + offset
					if to < 0 {
						to = 0
					}
					if to >= len(frames) {
						to = len(frames) - 1
					}
					if to != i {
						frames = moveFrame(frames, i, to)
						previewIdx = moveIndex(previewIdx, i, to)
					}
					rebuildList()
				}, func() {
					previewIdx = i
					updatePreview()
				})
			listBox.Add(container.NewPadded(card))
		}
		listBox.Refresh()
		countLabel.SetText(fmt.Sprintf("%d / %d frames", len(frames), maxFrames))
		updatePreview()
		refreshUpload()
	}

	rebuildTrash = func() {
		trashBox.RemoveAll()
		for i := range trash {
			i := i
			th := canvas.NewImageFromImage(screen.Decode(trash[i].data))
			th.FillMode = canvas.ImageFillContain
			th.ScaleMode = canvas.ImageScalePixels
			th.SetMinSize(fyne.NewSize(56, 32))

			restore := widget.NewButtonWithIcon("", theme.ContentUndoIcon(), func() {
				if len(frames) >= maxFrames {
					dialog.ShowInformation("Frame limit",
						fmt.Sprintf("The timeline already holds %d frames.", maxFrames), a.win)
					return
				}
				frames = append(frames, trash[i])
				trash = append(trash[:i], trash[i+1:]...)
				rebuildTrash()
				rebuildList()
			})
			purge := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				trash = append(trash[:i], trash[i+1:]...)
				rebuildTrash()
			})
			card := container.NewVBox(th, container.NewHBox(restore, purge))
			trashBox.Add(container.NewPadded(card))
		}
		trashBox.Refresh()
		// trashArea itself stays visible even when trash is empty — it's the
		// drag-to-trash drop zone, so it needs to be there (and laid out at a
		// real position) before the first frame is ever removed.
	}

	// --- controls ------------------------------------------------------

	// Revealed only once a transparent source is added.
	matteSwatch := canvas.NewRectangle(color.NRGBA{A: 0xff}) // shows opaque black by default
	matteSwatch.SetMinSize(fyne.NewSize(48, 24))
	mattePick := widget.NewButton("Pick colour…", func() {
		init := color.NRGBA{A: 0xff}
		if nc, ok := matte.(color.NRGBA); ok {
			init = nc
		}
		showOpaqueColourPicker("Background colour",
			"Fills transparent pixels before the frame is flattened", init, a.win,
			func(nc color.NRGBA) {
				matte = nc
				matteSwatch.FillColor = nc
				matteSwatch.Refresh()
				reencode()
				rebuildList()
			})
	})
	matteReset := widget.NewButton("Black", func() {
		matte = nil
		matteSwatch.FillColor = color.NRGBA{A: 0xff}
		matteSwatch.Refresh()
		reencode()
		rebuildList()
	})
	matteRow := container.NewHBox(
		widget.NewLabel("Transparent pixels →"), matteSwatch, mattePick, matteReset,
	)
	matteRow.Hide()

	// Scale/crop controls. zoom 1.0 is CentreCrop's own framing (no zoom);
	// panX/panY only have any effect once zoom > 1 gives them room to move
	// (see screen.CropAt's doc comment) or on a source whose aspect ratio
	// already leaves CentreCrop some travel on one axis.
	cropLinkChk := widget.NewCheck("Link crop across all frames", nil)
	cropLinkChk.Checked = true // matches cropLinked's initial value above

	zoomCropLabel := widget.NewLabel("Crop zoom") // distinct from the preview's display Zoom (1x-6x) below
	zoomCrop := widget.NewSlider(1, 4)
	zoomCrop.Step = 0.1
	panXLabel := widget.NewLabel("Pan X")
	panXCrop := widget.NewSlider(0, 1)
	panXCrop.Step = 0.02
	panYLabel := widget.NewLabel("Pan Y")
	panYCrop := widget.NewSlider(0, 1)
	panYCrop.Step = 0.02

	// currentCrop reads whichever crop the sliders should currently show:
	// the shared triple when linked, else the previewed frame's own triple
	// (or defaultCrop, if that frame hasn't been individually adjusted yet).
	currentCrop := func() cropState {
		if cropLinked {
			return sharedCrop
		}
		if len(frames) == 0 {
			return defaultCrop
		}
		idx := previewIdx
		if idx < 0 || idx >= len(frames) {
			idx = 0
		}
		if frames[idx].cropSet {
			return frames[idx].crop
		}
		return defaultCrop
	}

	// syncingCrop suppresses the sliders' own OnChanged while
	// refreshCropControls is setting their values programmatically, the same
	// pattern setInterval uses for the delay slider/entry pair below.
	syncingCrop := false
	refreshCropControls = func() {
		cs := currentCrop()
		syncingCrop = true
		zoomCrop.SetValue(cs.zoom)
		panXCrop.SetValue(cs.panX)
		panYCrop.SetValue(cs.panY)
		syncingCrop = false
		zoomCropLabel.SetText(fmt.Sprintf("Crop zoom %.1f×", cs.zoom))
		panXLabel.SetText(fmt.Sprintf("Pan X %.2f", cs.panX))
		panYLabel.SetText(fmt.Sprintf("Pan Y %.2f", cs.panY))
	}

	// applyCrop writes a slider-edited crop back to wherever it belongs
	// (sharedCrop, or the previewed frame's own triple) and re-derives every
	// affected frame's encoded data from it — the same reencode+rebuildList
	// pairing the matte controls above already use.
	applyCrop := func(mutate func(cs *cropState)) {
		cs := currentCrop()
		mutate(&cs)
		if cropLinked {
			sharedCrop = cs
		} else if len(frames) > 0 {
			idx := previewIdx
			if idx < 0 || idx >= len(frames) {
				idx = 0
			}
			frames[idx].crop = cs
			frames[idx].cropSet = true
		}
		reencode()
		rebuildList()
	}
	zoomCrop.OnChanged = func(v float64) {
		if !syncingCrop {
			applyCrop(func(cs *cropState) { cs.zoom = v })
		}
	}
	panXCrop.OnChanged = func(v float64) {
		if !syncingCrop {
			applyCrop(func(cs *cropState) { cs.panX = v })
		}
	}
	panYCrop.OnChanged = func(v float64) {
		if !syncingCrop {
			applyCrop(func(cs *cropState) { cs.panY = v })
		}
	}
	cropLinkChk.OnChanged = func(linked bool) {
		cropLinked = linked
		// Switching to individual mode does not touch any frame's own
		// crop — it just starts controlling/displaying whichever frame is
		// previewed instead of the shared one, per cropRectFor's policy.
		reencode()
		rebuildList()
	}

	cropRow1 := container.NewHBox(cropLinkChk)
	cropRow2 := container.NewHBox(
		zoomCropLabel, container.NewGridWrap(fyne.NewSize(160, zoomCrop.MinSize().Height), zoomCrop),
		panXLabel, container.NewGridWrap(fyne.NewSize(120, panXCrop.MinSize().Height), panXCrop),
		panYLabel, container.NewGridWrap(fyne.NewSize(120, panYCrop.MinSize().Height), panYCrop),
	)
	cropControls := container.NewVBox(cropRow1, cropRow2)

	addBtn := widget.NewButtonWithIcon("Add image…", theme.ContentAddIcon(), func() {
		fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			defer rc.Close()
			data, rerr := io.ReadAll(rc)
			if rerr != nil {
				dialog.ShowError(rerr, a.win)
				return
			}
			srcs, transparent, ferr := screen.DecodeFrames(bytes.NewReader(data))
			if ferr != nil {
				dialog.ShowError(ferr, a.win)
				return
			}
			base := strings.TrimSuffix(rc.URI().Name(), filepath.Ext(rc.URI().Name()))
			added := 0
			for k, s := range srcs {
				if len(frames) >= maxFrames {
					break
				}
				name := base
				if len(srcs) > 1 {
					name = fmt.Sprintf("%s [%d]", base, k+1)
				}
				// data is filled in by reencode() below (crop starts unset,
				// so cropRectFor falls back to defaultCrop for it, or to
				// sharedCrop if linked — either way it needs frames[i].src
				// bounds, so it can't be precomputed here without duplicating
				// cropRectFor's policy).
				frames = append(frames, frameItem{name: name, src: s})
				added++
			}
			if transparent && !matteSeen {
				matteSeen = true
				matteRow.Show()
			}
			if added < len(srcs) {
				dialog.ShowInformation("Frame limit",
					fmt.Sprintf("Added %d of %d frames; the screen holds %d.", added, len(srcs), maxFrames), a.win)
			}
			reencode()
			rebuildList()
		}, a.win)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".png", ".jpg", ".jpeg", ".bmp", ".gif"}))
		fd.Show()
	})

	clearBtn := widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), func() {
		frames = nil
		previewIdx = 0
		rebuildList()
	})

	prevBtn := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		if len(frames) > 0 {
			previewIdx = (previewIdx - 1 + len(frames)) % len(frames)
			updatePreview()
		}
	})
	nextBtn := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		if len(frames) > 0 {
			previewIdx = (previewIdx + 1) % len(frames)
			updatePreview()
		}
	})

	var ticker *time.Ticker
	playBtn = widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), nil)
	playBtn.OnTapped = func() {
		if playing {
			playing = false
			playBtn.SetText("Play")
			playBtn.SetIcon(theme.MediaPlayIcon())
			return
		}
		if len(frames) < 2 {
			return
		}
		playing = true
		playBtn.SetText("Pause")
		playBtn.SetIcon(theme.MediaPauseIcon())
		d := time.Duration(intervalMS) * time.Millisecond
		if ticker == nil {
			ticker = time.NewTicker(d)
			go func() {
				for range ticker.C {
					fyne.Do(func() {
						if !playing || len(frames) == 0 {
							return
						}
						previewIdx = (previewIdx + 1) % len(frames)
						updatePreview()
					})
				}
			}()
		} else {
			ticker.Reset(d)
		}
	}

	zoomSel := widget.NewSelect([]string{"1×", "2×", "3×", "4×", "6×"}, func(s string) {
		switch s {
		case "1×":
			zoom = 1
		case "2×":
			zoom = 2
		case "3×":
			zoom = 3
		case "4×":
			zoom = 4
		case "6×":
			zoom = 6
		}
		preview.SetMinSize(fyne.NewSize(float32(screen.Width*zoom), float32(screen.Height*zoom)))
		preview.Refresh()
		previewScroll.Refresh()
	})
	zoomSel.SetSelected("3×")

	// setInterval is the one place the delay changes. It clamps to the wire
	// range, mirrors the value into whichever control the user did not touch,
	// and retimes a running preview. syncing suppresses the echo.
	syncingInterval := false
	setInterval = func(v int, fromEntry bool) {
		if v < protocol.FrameIntervalMin {
			v = protocol.FrameIntervalMin
		}
		if v > protocol.FrameIntervalMax {
			v = protocol.FrameIntervalMax
		}
		intervalMS = v
		syncingInterval = true
		if !fromEntry {
			intervalEntry.SetText(strconv.Itoa(v))
		}
		if fv := float64(v); fromEntry && fv >= interval.Min && fv <= interval.Max {
			interval.SetValue(fv)
		}
		syncingInterval = false
		if ticker != nil && playing {
			ticker.Reset(time.Duration(v) * time.Millisecond)
		}
	}
	interval.OnChanged = func(v float64) {
		if !syncingInterval {
			setInterval(int(v), false)
		}
	}
	intervalEntry.OnChanged = func(s string) {
		if syncingInterval {
			return
		}
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			setInterval(n, true)
		}
	}

	upload = widget.NewButtonWithIcon("Upload to screen", theme.UploadIcon(), func() {
		if len(frames) == 0 || a.busy {
			return
		}
		bufs := make([][]byte, len(frames))
		for i := range frames {
			bufs[i] = frames[i].data
		}
		ivl := intervalMS
		ctx, cf := context.WithCancel(context.Background())
		a.uploadCancel = cf
		a.setBusy(true) // locks every other tab's controls for the upload's duration
		cancel.Enable()
		progress.SetValue(0)
		a.setStatus(fmt.Sprintf("uploading %d frame(s)…", len(bufs)))

		applog.Infof("upload: start %d frame(s), interval %d ms", len(bufs), ivl)

		a.do(func() {
			if a.dev == nil {
				applog.Errorf("upload: not connected")
				ui(func() {
					a.setBusy(false)
					cancel.Disable()
					a.setStatus("not connected")
				})
				return
			}
			err := a.dev.UploadScreen(ctx, bufs, ivl, func(done, total int) {
				if done%500 == 0 || done == total {
					applog.Infof("upload: %d/%d steps", done, total)
				}
				if done%120 == 0 || done == total {
					ui(func() { progress.SetValue(float64(done) / float64(total)) })
				}
			})
			ui(func() {
				cf()
				a.setBusy(false)
				a.uploadCancel = nil
				cancel.Disable()
				refreshUpload()
				if err != nil {
					if ctx.Err() != nil {
						applog.Warnf("upload: cancelled")
						a.setStatus("screen upload cancelled")
					} else {
						applog.Errorf("upload: %v", err)
						dialog.ShowError(fmt.Errorf("screen upload: %w", err), a.win)
						a.setStatus("screen upload failed")
					}
					return
				}
				applog.Infof("upload: done")
				progress.SetValue(1)
				a.setStatus("screen upload done")
			})
		})
	})

	cancel = widget.NewButton("Cancel", func() {
		if a.uploadCancel != nil {
			applog.Infof("upload: cancel requested by user")
			a.uploadCancel()
		}
	})

	a.onConnState = append(a.onConnState, func(connected bool, t k724.Target) {
		wiredReady = connected && t.Wired
		cancel.Disable()
		refreshUpload()
	})
	a.onBusy = append(a.onBusy, func(busy bool) {
		toggle(!busy, addBtn, clearBtn)
		refreshUpload()
	})
	upload.Disable()
	cancel.Disable()

	rebuildList()

	// --- layout ------------------------------------------------------

	explain := wrapLabel(fmt.Sprintf(
		"Each image becomes one frame, centre-cropped to %d×%d by default and "+
			"reduced to the screen's 16-bit colour — the preview shows exactly that. "+
			"Use the Zoom/Pan X/Pan Y sliders to crop somewhere other than centred; "+
			"“Link crop across all frames” applies one crop to the whole timeline "+
			"(the common case for a GIF) — turn it off to crop each frame on its "+
			"own, picked up from whichever frame is currently previewed. "+
			"A GIF is split into its frames. Up to %d frames on the timeline; the "+
			"screen loops them at the frame delay (%d–%d ms — the device does not "+
			"appear to play back any faster than %d ms, so the tool won't send less). "+
			"Drag a frame left or right to reorder it, or drag it onto the “Removed "+
			"frames” strip below the timeline to remove it — restore it from there. "+
			"At higher zoom, click and drag the preview to pan around it. "+
			"Upload needs the wired keyboard.\n\n"+
			"The screen has no transparency. When a source has transparent pixels, "+
			"a background-colour control appears — those pixels are filled with it "+
			"before upload (black by default).",
		screen.Width, screen.Height, maxFrames,
		protocol.FrameIntervalMin, protocol.FrameIntervalMax, protocol.FrameIntervalMin))

	previewBar := container.NewHBox(
		prevBtn, nextBtn, playBtn, posLabel,
		layout.NewSpacer(),
		widget.NewLabel("Zoom"), zoomSel,
	)

	rowH := intervalEntry.MinSize().Height
	delayRow := container.NewHBox(
		widget.NewLabel("Frame delay"),
		container.NewGridWrap(fyne.NewSize(220, rowH), interval),
		container.NewGridWrap(fyne.NewSize(76, rowH), intervalEntry),
		widget.NewLabel("ms"),
	)

	listScroll := container.NewHScroll(listBox)
	listScroll.SetMinSize(fyne.NewSize(0, 118))

	trashScroll := container.NewHScroll(trashBox)
	trashScroll.SetMinSize(fyne.NewSize(0, 80))

	// trashHighlight and trashDropCue are only visible while a frame is
	// being dragged — a border plus an obvious trash-can icon/label so the
	// drop-to-delete target is unmistakable. setTrashHighlight (declared
	// earlier, driven from rebuildList's per-card drag callbacks) toggles
	// them between off / active (a drag is happening somewhere) / hover
	// (the pointer is over this strip right now).
	trashHighlight := canvas.NewRectangle(color.NRGBA{})
	trashHighlight.StrokeWidth = 3
	trashDropIcon := widget.NewIcon(theme.DeleteIcon())
	trashDropLabel := widget.NewLabelWithStyle("Drop to delete", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	trashDropCue := container.NewHBox(container.NewGridWrap(fyne.NewSize(28, 28), trashDropIcon), trashDropLabel)
	trashDropCue.Hide()
	setTrashHighlight = func(active, hover bool) {
		switch {
		case hover:
			trashHighlight.StrokeColor = theme.ErrorColor()
			trashDropIcon.SetResource(theme.NewErrorThemedResource(theme.DeleteIcon()))
		case active:
			trashHighlight.StrokeColor = theme.WarningColor()
			trashDropIcon.SetResource(theme.NewWarningThemedResource(theme.DeleteIcon()))
		default:
			trashHighlight.StrokeColor = color.NRGBA{}
		}
		trashHighlight.Refresh()
		if active {
			trashDropCue.Show()
		} else {
			trashDropCue.Hide()
		}
	}

	trashArea = container.NewStack(trashHighlight, container.NewVBox(
		container.NewHBox(
			widget.NewLabelWithStyle("Removed frames — restore or delete", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
			layout.NewSpacer(),
			trashDropCue,
		),
		trashScroll,
	))
	// Stays visible (even empty) — see rebuildTrash: this is also the
	// drag-to-trash drop zone, so it can't wait for a first item to exist.

	top := container.NewVBox(
		title("TFT screen"),
		container.NewHBox(addBtn, clearBtn, layout.NewSpacer(), countLabel),
		matteRow,
		cropControls,
		listScroll,
		trashArea,
		previewBar,
	)
	bottom := container.NewVBox(
		delayRow,
		container.NewHBox(upload, cancel),
		progress,
		widget.NewSeparator(),
		explain,
	)
	return container.NewBorder(top, bottom, nil, nil, previewScroll)
}

// ------------------------------------------------------------------ Info tab

// buildInfoTab is a read-only "about this keyboard + about this tool" panel:
// connection identity, the firmware versions the wired open sequence reads
// (see README.md "Firmware compatibility"), the battery/status value from
// command 0x1A, and this build's own version. Requested by
// docs/MISSING_FEATURES.md "Info tab".
func (a *App) buildInfoTab() fyne.CanvasObject {
	unset := "—"
	connLabel := widget.NewLabel(unset)
	productLabel := widget.NewLabel(unset)
	idLabel := widget.NewLabel(unset)
	pathLabel := widget.NewLabel(unset)
	kbLabel := widget.NewLabel(unset)
	apLabel := widget.NewLabel(unset)
	batteryLabel := widget.NewLabel(unset)

	// refresh re-reads firmware (cheap, already cached on Device from the
	// connect-time probe) and battery (a fresh 0x1A round trip) on the
	// worker goroutine, matching every other tab's a.dev access pattern —
	// a.dev is touched only from inside a.do closures, never from the UI
	// thread directly.
	refresh := func() {
		a.do(func() {
			if a.dev == nil {
				return
			}
			fw := a.dev.Firmware()
			bat, batErr := a.dev.Battery()
			ui(func() {
				if fw.Known {
					kbLabel.SetText(protocol.FormatVersion(fw.KBVersion))
					apLabel.SetText(protocol.FormatVersion(fw.APVersion))
				} else {
					kbLabel.SetText("not reported (wireless receiver)")
					apLabel.SetText("not reported (wireless receiver)")
				}
				if batErr != nil {
					batteryLabel.SetText("unavailable: " + batErr.Error())
				} else {
					batteryLabel.SetText(fmt.Sprintf("%d%%", bat.Percent))
				}
			})
		})
	}

	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), refresh)

	connected := false
	refreshControls := func() { toggle(connected && !a.busy, refreshBtn) }
	a.onConnState = append(a.onConnState, func(c bool, t k724.Target) {
		connected = c
		refreshControls()
		if !c {
			connLabel.SetText(unset)
			productLabel.SetText(unset)
			idLabel.SetText(unset)
			pathLabel.SetText(unset)
			kbLabel.SetText(unset)
			apLabel.SetText(unset)
			batteryLabel.SetText(unset)
			return
		}
		connLabel.SetText(t.Label())
		productLabel.SetText(t.Product)
		idLabel.SetText(fmt.Sprintf("%04x:%04x", t.VID, t.PID))
		pathLabel.SetText(t.Path)
		refresh()
	})
	a.onBusy = append(a.onBusy, func(bool) { refreshControls() })
	refreshControls()

	form := container.NewVBox(
		title("Keyboard"),
		labelRow("Connection", connLabel),
		labelRow("Product", productLabel),
		labelRow("VID:PID", idLabel),
		labelRow("HID path", pathLabel),
		widget.NewSeparator(),
		title("Firmware"),
		labelRow("KB (keyboard)", kbLabel),
		labelRow("AP (2.4 GHz receiver)", apLabel),
		widget.NewSeparator(),
		title("Battery"),
		labelRow("Charge", batteryLabel),
		refreshBtn,
		widget.NewSeparator(),
		title("k724-tool"),
		labelRow("Version", widget.NewLabel(toolVersion())),
	)
	return container.NewVScroll(form)
}

// labelRow lays out a bold field name and its value side by side.
func labelRow(name string, value *widget.Label) fyne.CanvasObject {
	return container.NewBorder(nil, nil, widget.NewLabelWithStyle(name+":", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, value)
}

// toolVersion reports this build's own version: the VCS revision Go embeds
// automatically when building from a git checkout (Go 1.18+), or "dev" for
// a `go run` build with no embedded VCS info.
func toolVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev, mod string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			mod = s.Value
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if mod == "true" {
		rev += "-dirty"
	}
	return rev
}

// ------------------------------------------------------------------- Log tab

func (a *App) buildLogTab() fyne.CanvasObject {
	view := widget.NewMultiLineEntry()
	view.Wrapping = fyne.TextWrapWord
	view.SetMinRowsVisible(22)

	var last string
	render := func() {
		text := strings.Join(applog.Lines(), "\n")
		if text == last {
			return
		}
		last = text
		view.SetText(text)
		view.CursorRow = strings.Count(text, "\n") // follow the tail
		view.Refresh()
	}

	refreshBtn := widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), render)
	copyBtn := widget.NewButtonWithIcon("Copy all", theme.ContentCopyIcon(), func() {
		a.fyneApp.Clipboard().SetContent(strings.Join(applog.Lines(), "\n"))
		a.setStatus("log copied to clipboard")
	})

	// K724_LOG_LEVEL sets the level a run starts at; this lets it be raised to
	// DEBUG (full per-command hex dumps) or lowered again without a restart.
	levelSelect := widget.NewSelect(
		[]string{applog.LevelDebug.String(), applog.LevelInfo.String(), applog.LevelWarn.String(), applog.LevelError.String()},
		func(s string) {
			if lvl, ok := applog.ParseLevel(s); ok {
				applog.SetLevel(lvl)
			}
		},
	)
	levelSelect.SetSelected(applog.GetLevel().String())

	// Poll the ring buffer so entries logged from the worker goroutine show up
	// without the user hitting Refresh. render() is a no-op when nothing changed.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			fyne.Do(render)
		}
	}()

	render()

	return container.NewBorder(
		container.NewHBox(refreshBtn, copyBtn, widget.NewLabel("Level:"), levelSelect),
		wrapLabel("Log file: "+applog.Path()),
		nil, nil,
		container.NewScroll(view),
	)
}

// ---------------------------------------------------------------- helpers

func title(s string) *widget.Label {
	return widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func wrapLabel(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// pickColour opens Fyne's advanced colour picker (the same one the Lighting
// tab's global colour uses) seeded with initial, and calls onPicked with the
// chosen colour, opaque. Kept in one place so every colour control in the app
// looks and behaves the same.
func (a *App) pickColour(title, message string, initial color.NRGBA, onPicked func(color.NRGBA)) {
	p := dialog.NewColorPicker(title, message, func(c color.Color) {
		r, g, b, _ := c.RGBA()
		onPicked(color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff})
	}, a.win)
	p.Advanced = true
	p.Show()
	p.SetColor(initial)
}

// toggle enables or disables a set of widgets.
func toggle(enabled bool, ws ...fyne.Disableable) {
	for _, w := range ws {
		if enabled {
			w.Enable()
		} else {
			w.Disable()
		}
	}
}
