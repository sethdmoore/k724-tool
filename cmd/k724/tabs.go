package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
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
		wantBr, wantSp := int(brightness.Value), int(speed.Value)
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
		speed.SetValue(float64(b.Speed()))
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
	data []byte      // screen.Frame(src, matte); rebuilt when the matte changes
}

const maxFrames = 25

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

	blank := screen.Decode(nil)
	preview := canvas.NewImageFromImage(blank)
	preview.FillMode = canvas.ImageFillContain
	preview.ScaleMode = canvas.ImageScalePixels // nearest-neighbour: show real pixels
	preview.SetMinSize(fyne.NewSize(float32(screen.Width*zoom), float32(screen.Height*zoom)))
	previewScroll := container.NewScroll(container.NewCenter(preview))
	previewScroll.SetMinSize(fyne.NewSize(0, float32(screen.Height*3+16)))

	posLabel := widget.NewLabel("no frames")
	countLabel := widget.NewLabel(fmt.Sprintf("0 / %d frames", maxFrames))

	// The timeline runs left-to-right; each frame is a small vertical card.
	listBox := container.NewHBox()
	// Removed frames land here so they can be restored.
	var trash []frameItem
	trashBox := container.NewHBox()
	var rebuildTrash func()
	var trashArea *fyne.Container // set in the layout section; hidden when empty

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
	}

	reencode := func() {
		for i := range frames {
			frames[i].data = screen.Frame(frames[i].src, matte)
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

			left := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
				if i > 0 {
					frames[i-1], frames[i] = frames[i], frames[i-1]
					if previewIdx == i {
						previewIdx = i - 1
					}
					rebuildList()
				}
			})
			right := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
				if i < len(frames)-1 {
					frames[i+1], frames[i] = frames[i], frames[i+1]
					if previewIdx == i {
						previewIdx = i + 1
					}
					rebuildList()
				}
			})
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				removed := frames[i]
				frames = append(frames[:i], frames[i+1:]...)
				trash = append([]frameItem{removed}, trash...)
				if len(trash) > trashCap {
					trash = trash[:trashCap]
				}
				rebuildTrash()
				rebuildList()
			})
			view := widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
				previewIdx = i
				updatePreview()
			})
			if i == 0 {
				left.Disable()
			}
			if i == len(frames)-1 {
				right.Disable()
			}
			card := container.NewVBox(
				widget.NewLabelWithStyle(fmt.Sprintf("%d", i+1), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
				th,
				container.NewHBox(left, view, right, del),
			)
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
		if trashArea != nil {
			if len(trash) == 0 {
				trashArea.Hide()
			} else {
				trashArea.Show()
			}
		}
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
				frames = append(frames, frameItem{name: name, src: s, data: screen.Frame(s, matte)})
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
		"Each image becomes one frame. Images are centre-cropped to %d×%d and "+
			"reduced to the screen's 16-bit colour — the preview shows exactly that. "+
			"A GIF is split into its frames. Up to %d frames on the timeline; the "+
			"screen loops them at the frame delay (%d–%d ms — the device does not "+
			"appear to play back any faster than %d ms, so the tool won't send less). "+
			"Removing a frame drops it into the “Removed frames” strip, where you can "+
			"restore it. Upload needs the wired keyboard.\n\n"+
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
	trashArea = container.NewVBox(
		widget.NewLabelWithStyle("Removed frames — restore or delete", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
		trashScroll,
	)
	trashArea.Hide()

	top := container.NewVBox(
		title("TFT screen"),
		container.NewHBox(addBtn, clearBtn, layout.NewSpacer(), countLabel),
		matteRow,
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
