// Command k724 is a desktop GUI for the Redragon K724-RGB-PRO keyboard: set
// the onboard clock, pick the lighting effect / brightness / speed / colour,
// choose the USB polling rate, and upload a still image or short animation to
// the TFT screen. It replaces the Windows-only control app.
//
// Every device operation runs on one worker goroutine (see run); widget
// updates from it are marshalled back with fyne.Do.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"k724tool/internal/applog"
	"k724tool/internal/k724"
	"k724tool/internal/protocol"
)

// batteryPollInterval is how often the toolbar battery indicator re-reads
// command 0x1A while a device stays connected.
const batteryPollInterval = 5 * time.Minute

// batteryBarWidth is the number of terminal-style cells in the toolbar
// battery indicator's bar.
const batteryBarWidth = 8

// eighthBlocks are the Unicode eighth-block characters used to render a
// partially-filled trailing cell in the battery bar (index 0 is an empty
// cell, index 8 would be a full cell but is never used — a full cell is
// drawn as '█' instead).
var eighthBlocks = [8]rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// formatBatteryBar renders percent (clamped to 0-100) as e.g. "99% [███████▉]"
// — full-block cells plus one eighth-resolution partial trailing cell, the
// same technique common CLI progress bars use.
func formatBatteryBar(percent int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	// Total eighths-of-a-cell filled across the whole bar, rounded to the
	// nearest eighth (integer arithmetic: +50 before the /100 truncation).
	eighths := (percent*batteryBarWidth*8 + 50) / 100
	full := eighths / 8
	rem := eighths % 8

	var b strings.Builder
	fmt.Fprintf(&b, "%d%% [", percent)
	for i := 0; i < batteryBarWidth; i++ {
		switch {
		case i < full:
			b.WriteRune('█')
		case i == full && rem > 0:
			b.WriteRune(eighthBlocks[rem])
		default:
			b.WriteRune(' ')
		}
	}
	b.WriteString("]")
	return b.String()
}

const appID = "com.github.k724tool.k724"

// ui runs fn on the Fyne UI thread. Fyne 2.6+ requires this for any widget
// mutation from a non-main goroutine.
func ui(fn func()) { fyne.Do(fn) }

// App holds the window, the worker queue, and the shared widgets.
type App struct {
	fyneApp fyne.App
	win     fyne.Window

	jobs chan func() // closures serialised onto the worker goroutine

	dev     *k724.Device  // touched ONLY on the worker goroutine
	targets []k724.Target // set on the UI thread by refreshTargets

	deviceSelect   *widget.Select
	statusLabel    *widget.Label
	firmwareBanner *widget.Label // hidden unless the connected unit's firmware mismatches

	// hooks fired after a successful connect + settings read
	onSettings []func(protocol.SettingsBlock)
	// hooks fired on every connection-state change
	onConnState []func(connected bool, t k724.Target)
	// hooks fired whenever a device operation starts (busy=true) or finishes
	// (busy=false). Tabs use this to lock their controls so a second write
	// cannot be queued on top of one already running — e.g. a clock change
	// during a screen upload. UI thread only.
	onBusy []func(busy bool)
	// busy is true while a device operation is in flight. UI thread only.
	busy bool

	uploadCancel func() // set on the UI thread while a screen upload runs
}

func main() {
	if p, err := applog.Init("k724"); err != nil {
		applog.Warnf("no log file: %v", err)
	} else {
		applog.Infof("log file: %s", p)
	}
	applog.Infof("k724 starting (pid %d)", os.Getpid())

	a := &App{
		fyneApp: app.NewWithID(appID),
		jobs:    make(chan func(), 32),
	}
	a.win = a.fyneApp.NewWindow("K724-RGB-PRO")
	a.win.Resize(fyne.NewSize(620, 720))

	go a.worker()

	if err := k724.Init(); err != nil {
		applog.Errorf("hidapi init failed: %v", err)
		a.statusLabel = widget.NewLabel("hidapi init failed: " + err.Error())
		a.win.SetContent(a.statusLabel)
		a.win.ShowAndRun()
		return
	}
	defer k724.Exit()

	a.win.SetContent(a.build())
	a.refreshTargets()
	a.win.ShowAndRun()
}

func (a *App) worker() {
	for j := range a.jobs {
		j()
	}
}

// do enqueues fn on the worker goroutine.
func (a *App) do(fn func()) { a.jobs <- fn }

func (a *App) setStatus(s string) { a.statusLabel.SetText(s) }

// build assembles the toolbar and the tab set.
func (a *App) build() fyne.CanvasObject {
	a.deviceSelect = widget.NewSelect(nil, func(string) {
		a.connect(a.deviceSelect.SelectedIndex())
	})
	a.deviceSelect.PlaceHolder = "(no device)"
	a.statusLabel = widget.NewLabel("starting…")

	a.firmwareBanner = widget.NewLabel("")
	a.firmwareBanner.Wrapping = fyne.TextWrapWord
	a.firmwareBanner.Importance = widget.WarningImportance
	a.firmwareBanner.Hide()

	refresh := widget.NewButton("Refresh", func() { a.refreshTargets() })

	// Don't let the device be switched or re-enumerated mid-operation.
	a.onBusy = append(a.onBusy, func(busy bool) {
		toggle(!busy, a.deviceSelect, refresh)
	})

	// Toolbar battery readout: a second, independent battery reading from
	// the Info tab's manual one. Auto-polled every batteryPollInterval plus
	// once on every successful connect. pollBattery runs on the worker
	// goroutine (it's only ever invoked via a.do), matching every other
	// a.dev access in this file — never read a.dev directly from the
	// ticker goroutine below.
	batteryToolbarLabel := widget.NewLabelWithStyle("—", fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	pollBattery := func() {
		if a.dev == nil {
			return
		}
		bat, err := a.dev.Battery()
		ui(func() {
			if err != nil {
				return
			}
			batteryToolbarLabel.SetText(formatBatteryBar(bat.Percent))
		})
	}
	a.onConnState = append(a.onConnState, func(c bool, _ k724.Target) {
		if !c {
			batteryToolbarLabel.SetText("—")
			return
		}
		a.do(pollBattery)
	})
	go func() {
		t := time.NewTicker(batteryPollInterval)
		defer t.Stop()
		for range t.C {
			a.do(pollBattery)
		}
	}()

	toolbar := container.NewBorder(nil, nil, widget.NewLabel("Device:"), container.NewHBox(refresh, batteryToolbarLabel), a.deviceSelect)
	top := container.NewVBox(toolbar, a.firmwareBanner)

	tabs := container.NewAppTabs(
		container.NewTabItem("Clock", a.buildClockTab()),
		container.NewTabItem("Lighting", a.buildLightingTab()),
		container.NewTabItem("Polling", a.buildPollingTab()),
		container.NewTabItem("Screen", a.buildScreenTab()),
		container.NewTabItem("Info", a.buildInfoTab()),
		container.NewTabItem("Log", a.buildLogTab()),
	)

	return container.NewBorder(top, a.statusLabel, nil, nil, tabs)
}

// showFirmware updates the firmware-mismatch banner. It shows a ⚠️ line when
// the connected unit reports a firmware version this tool was not built for,
// and hides itself otherwise (a match, or the wireless receiver, which does
// not report a version). UI thread only.
func (a *App) showFirmware(fw k724.Firmware) {
	if a.firmwareBanner == nil {
		return
	}
	if msg := fw.Warning(); msg != "" {
		a.firmwareBanner.SetText(msg)
		a.firmwareBanner.Show()
	} else {
		a.firmwareBanner.SetText("")
		a.firmwareBanner.Hide()
	}
}

// refreshTargets re-enumerates devices and repopulates the picker.
func (a *App) refreshTargets() {
	a.do(func() {
		ts, err := k724.Enumerate()
		ui(func() {
			if err != nil {
				a.setStatus("enumerate failed: " + err.Error())
				return
			}
			a.targets = ts
			opts := make([]string, len(ts))
			for i, t := range ts {
				opts[i] = fmt.Sprintf("%s — %s", t.Label(), t.Product)
			}
			a.deviceSelect.Options = opts
			a.deviceSelect.Refresh()
			if len(ts) == 0 {
				a.setStatus("no K724-RGB-PRO found — plug it in and press Refresh")
				a.showFirmware(k724.Firmware{})
				a.fireConnState(false, k724.Target{})
				return
			}
			a.setStatus(fmt.Sprintf("found %d interface(s)", len(ts)))
			if a.deviceSelect.SelectedIndex() < 0 {
				a.deviceSelect.SetSelectedIndex(0)
			}
		})
	})
}

// connect closes any open device and opens targets[idx], then reads its
// settings block and notifies the tabs.
func (a *App) connect(idx int) {
	if idx < 0 || idx >= len(a.targets) {
		return
	}
	t := a.targets[idx]
	a.setStatus("connecting to " + t.Label() + "…")
	applog.Infof("connect: selecting [%d] %s", idx, t.Label())
	a.do(func() {
		if a.dev != nil {
			a.dev.Close()
			a.dev = nil
		}
		dev, err := k724.Open(t)
		if err != nil {
			applog.Errorf("connect: %v", err)
			ui(func() {
				a.showFirmware(k724.Firmware{})
				if k724.IsPermissionError(err) {
					a.showPermissionHelp(t)
				} else {
					a.setStatus("connect failed: " + err.Error())
				}
				a.fireConnState(false, t)
			})
			return
		}
		block, berr := dev.ReadSettings()
		fw := dev.Firmware()
		a.dev = dev
		ui(func() {
			a.showFirmware(fw)
			if w := fw.Warning(); w != "" {
				applog.Warnf("connect: %s", w)
			}
			if berr != nil {
				applog.Warnf("connect: connected to %s but settings read failed: %v", t.Label(), berr)
				a.setStatus(fmt.Sprintf("connected to %s; settings read failed: %v", t.Label(), berr))
			} else {
				applog.Infof("connect: connected to %s", t.Label())
				a.setStatus("connected to " + t.Label())
				for _, f := range a.onSettings {
					f(block)
				}
			}
			a.fireConnState(true, t)
		})
	})
}

func (a *App) fireConnState(connected bool, t k724.Target) {
	for _, f := range a.onConnState {
		f(connected, t)
	}
}

// setBusy flips the busy flag and notifies every tab. Call it on the UI thread
// only. While busy, tabs disable their action controls and runOnDevice refuses
// new work, so device writes stay strictly one-at-a-time.
func (a *App) setBusy(busy bool) {
	a.busy = busy
	for _, f := range a.onBusy {
		f(busy)
	}
}

// runOnDevice enqueues an operation that needs the open device. It reports a
// friendly error if nothing is connected, and refreshes the tabs from a fresh
// settings read on success.
func (a *App) runOnDevice(desc string, op func(*k724.Device) error) {
	if a.busy {
		a.setStatus("busy — wait for the current operation to finish")
		return
	}
	a.setStatus(desc + "…")
	a.setBusy(true)
	a.do(func() {
		if a.dev == nil {
			ui(func() {
				a.setBusy(false)
				a.setStatus("not connected")
			})
			return
		}
		err := op(a.dev)
		var block protocol.SettingsBlock
		var berr error
		if err == nil {
			block, berr = a.dev.ReadSettings()
		}
		ui(func() {
			a.setBusy(false)
			if err != nil {
				dialog.ShowError(fmt.Errorf("%s: %w", desc, err), a.win)
				a.setStatus(desc + " failed")
				return
			}
			a.setStatus(desc + " — done")
			if berr == nil {
				for _, f := range a.onSettings {
					f(block)
				}
			}
		})
	})
}

func (a *App) showPermissionHelp(t k724.Target) {
	a.setStatus("permission denied opening the HID device")
	msg := fmt.Sprintf(`The OS denied access to the keyboard's HID node.

On Linux, install the udev rule and reload:

  sudo cp packaging/70-redragon-k724.rules /etc/udev/rules.d/
  sudo udevadm control --reload && sudo udevadm trigger

Then unplug and replug the %s, and press Refresh.`, t.Label())
	dialog.ShowInformation("Permission denied", msg, a.win)
}
