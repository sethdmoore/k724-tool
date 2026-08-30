package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// showOpaqueColourPicker opens a small RGB-only colour picker. Unlike
// dialog.NewColorPicker it has no alpha channel at all, so the caller can
// never come back with a translucent colour — the result is always fully
// opaque. onPicked runs with the chosen colour when the user confirms.
func showOpaqueColourPicker(title, message string, initial color.NRGBA, win fyne.Window, onPicked func(color.NRGBA)) {
	cur := color.NRGBA{R: initial.R, G: initial.G, B: initial.B, A: 0xff}

	swatch := canvas.NewRectangle(cur)
	swatch.SetMinSize(fyne.NewSize(0, 32))

	hex := widget.NewEntry()

	var updating bool
	set := func(c color.NRGBA) {
		cur = color.NRGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
		swatch.FillColor = cur
		swatch.Refresh()
	}

	newChannel := func(v uint8, apply func(uint8)) (*widget.Slider, *widget.Label) {
		lbl := widget.NewLabel(fmt.Sprintf("%d", v))
		s := widget.NewSlider(0, 255)
		s.Step = 1
		s.Value = float64(v)
		s.OnChanged = func(f float64) {
			n := uint8(f)
			lbl.SetText(fmt.Sprintf("%d", n))
			if updating {
				return
			}
			apply(n)
			updating = true
			hex.SetText(fmt.Sprintf("#%02x%02x%02x", cur.R, cur.G, cur.B))
			updating = false
		}
		return s, lbl
	}

	rSlider, rLbl := newChannel(cur.R, func(n uint8) { set(color.NRGBA{R: n, G: cur.G, B: cur.B}) })
	gSlider, gLbl := newChannel(cur.G, func(n uint8) { set(color.NRGBA{R: cur.R, G: n, B: cur.B}) })
	bSlider, bLbl := newChannel(cur.B, func(n uint8) { set(color.NRGBA{R: cur.R, G: cur.G, B: n}) })

	hex.SetText(fmt.Sprintf("#%02x%02x%02x", cur.R, cur.G, cur.B))
	hex.OnChanged = func(s string) {
		if updating {
			return
		}
		var r, g, b uint8
		if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
			return
		}
		set(color.NRGBA{R: r, G: g, B: b})
		updating = true
		rSlider.SetValue(float64(r))
		gSlider.SetValue(float64(g))
		bSlider.SetValue(float64(b))
		rLbl.SetText(fmt.Sprintf("%d", r))
		gLbl.SetText(fmt.Sprintf("%d", g))
		bLbl.SetText(fmt.Sprintf("%d", b))
		updating = false
	}

	form := container.NewGridWithColumns(3,
		widget.NewLabel("R"), rSlider, rLbl,
		widget.NewLabel("G"), gSlider, gLbl,
		widget.NewLabel("B"), bSlider, bLbl,
	)

	body := container.NewVBox(
		widget.NewLabel(message),
		swatch,
		form,
		container.NewGridWithColumns(2, widget.NewLabel("Hex"), hex),
	)

	dialog.ShowCustomConfirm(title, "Select", "Cancel", body, func(ok bool) {
		if ok && onPicked != nil {
			onPicked(cur)
		}
	}, win)
}
