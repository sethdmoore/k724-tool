# Missing features

Things the Windows control app does that k724-tool does **not** do yet.
This is a running list — the user is adding to it over time, so treat it
as incomplete. Nothing here is a protocol finding; it is a gap register.

## Lighting

- **Rainbow / gradient (multi-colour) configuration.** The Windows app
  lets you define a lighting look from several colour stops (a
  rainbow / gradient). k724-tool only exposes a single global custom
  colour plus the per-key `0x0b` table. No multi-stop gradient UI, and
  the wire format for it has not been investigated.
- **Audio wave** (`light_mode_audiorecord_text`, effect ID 21) **and
  Light And Shadow** (`light_mode_capturecolor_text`, ID 22). These are
  host-driven effects: the Windows app samples the PC's audio output
  (audio wave) or the screen contents (light and shadow) in real time
  and streams per-key colour to the keyboard. Selecting the mode may be
  a one-byte settings write, but making it *do* anything needs a
  continuous local audio-capture / screen-capture pipeline feeding the
  live per-key channel. Out of scope unless someone wants to build that.
  See `docs/RGB_LIGHTING.md` for the effect-ID table.
- **Preview the selected effect on the per-key grid.** When an effect
  preset is chosen in the Lighting tab, the keyboard diagram in the
  per-key colour section should show an approximation of what that
  effect looks like (a static rendering of the wave / breath / spectrum
  / etc.), so the grid is a live preview and not just a Custom-mode
  editor.

## Per-key

- **Macros.** No macro recording, storage, or assignment. The Windows
  app records key/delay sequences and binds them to a key. Wire format
  unknown.
- **Rebinding.** No key remapping. The `0x09` table (documented in
  `docs/RGB_LIGHTING.md` as the slot-order key for the `0x0b` colour
  table) is the remap table, but k724-tool only reads its order to index
  colours — it never writes a changed binding.

## Screen tab — planned UX changes

Notes for the next fresh session. These are interaction changes to the
existing Screen tab, not new protocol work.

- ~~**Scale / crop.**~~ **Fixed.** `internal/screen/encode.go` gained
  `CentreCrop` (the old unexported `centreCrop`, exported), `CropAt(b,
  zoom, panX, panY)` (an aspect-correct sub-rectangle of `b`, sized by
  `zoom` and positioned by `panX`/`panY` — `zoom=1, panX=panY=0.5`
  reproduces `CentreCrop(b)` exactly), and `FrameCrop(img, matte, crop)`
  (what `Frame` does, but scaling from an explicit rectangle instead of
  an internally-computed centre-crop; `Frame` is now a one-line wrapper
  around a shared `frameImpl`). All three have unit tests in
  `encode_test.go`.
  `cmd/k724/tabs.go`'s Screen tab adds a "Link crop across all frames"
  checkbox plus Crop zoom / Pan X / Pan Y sliders:
  - **linked** (the default) — one shared `(zoom, panX, panY)` triple
    applies to every frame (the common case for a GIF).
  - **individual** — each `frameItem` carries its own triple
    (`crop`/`cropSet`), falling back to `CentreCrop`'s equivalent until
    it's been touched; the sliders read/edit whichever frame is
    currently in the preview (`previewIdx`).
  - `reencode()` (already the one place matte changes were applied) now
    also re-derives every frame's crop rectangle via a `cropRectFor`
    helper, so slider/checkbox changes reuse the same
    reencode-then-rebuildList path the matte picker already used.
  - A fully interactive draggable/resizable crop-rectangle overlay was
    considered but judged a materially bigger lift (a new custom widget
    with corner-drag resize handles) for the same result; sliders were
    the pragmatic choice.
- ~~**Frame reordering / deletion.**~~ **Fixed.** The per-frame
  `[◀] [👁] [▶] [🗑]` button row is gone; each timeline card
  (`cmd/k724/tabs.go`, `frameCard`, a small `widget.BaseWidget` implementing
  `fyne.Draggable`) is now dragged directly:
  - **drag left/right to reorder** — the whole-gesture horizontal delta
    (accumulated in `Dragged`, read at `DragEnd`) divided by one card's
    laid-out width gives the number of slots moved; `moveFrame`/`moveIndex`
    splice `frames` and keep `previewIdx` pointing at the same frame.
  - **drag onto the trash zone to delete** — `DragEnd`'s final absolute
    pointer position is hit-tested against `AbsolutePositionForObject
    (trashArea)` + its size; a hit does exactly what the old 🗑 button did
    (append to `trash`, `rebuildTrash()`, `rebuildList()`). `trashArea` (the
    existing "Removed frames" strip, already sitting directly under the
    timeline) is the drop target — no second one was built — and now stays
    visible even when empty so there's always somewhere to drop the very
    first deleted frame.
  - The explicit 👁 button is gone too — it was redundant with a plain tap
    on the card body, which already did the same thing via
    `frameCard.Tapped`.
- ~~**Preview pan.**~~ **Fixed.** `cmd/k724/tabs.go` adds `previewPan`, a
  thin `widget.BaseWidget` with no visual of its own (`CreateRenderer`
  hands back the preview `canvas.Image` unchanged via
  `widget.NewSimpleRenderer`, wrapped around it purely so it implements
  `fyne.Draggable` — `canvas.Image` doesn't, and `container.Scroll` only
  reacts to the mouse wheel and its own scrollbar widgets, confirmed
  against `fyne.io/fyne/v2/internal/widget/scroller.go`). Its `Dragged`
  callback (`panPreview`) applies each event's delta straight to
  `previewScroll.Offset` via `ScrollToOffset`, clamped by hand
  (`clampScrollOffset`, mirroring `Scroll`'s own unexported
  `computeOffset` — `ScrollToOffset` itself doesn't clamp) so it's a
  harmless no-op at low zoom, where the content already fits the
  viewport.
- ~~**Frame-delay lower bound is 50 ms.**~~ **Fixed.** Added
  `protocol.FrameIntervalMin = 50`; `SetFrameIntervalMS` now clamps to
  `FrameIntervalMin..FrameIntervalMax`, and the Screen tab's slider
  (`cmd/k724/tabs.go`) starts at 50 instead of 10.

## Keyboard layout diagram — fixed

~~Nitpicks in the per-key colour grid~~ Both fixed in
`internal/protocol/keymap.go`'s `KeyboardLayout`, locked in by
`keymap_test.go`:

- **Nav column alignment** — Ins / Del / PgUp / PgDn now all start at
  the same cumulative unit offset (62) within their row, so the editor
  grid draws one straight column, sitting to the right of the Up-arrow
  column (58).
- **Knob removed** — the volume knob (index 13) had no LED; the
  `{"Knob", 13, 4}` entry (and its trailing gap) is gone.

## GUI — bugs and polish

- **"Add image…" file dialog is too small and not resizable.** Fyne's
  `dialog.ShowFileOpen` opens a small fixed modal; navigating a deep
  directory tree in it is painful. Give it a sensible large default
  size (resize the dialog before `Show`, sized against the window /
  screen), and/or remember the last-used directory so the user doesn't
  re-walk the tree each time.
- **Log tab — file line.** The "Log file: …" line at the bottom of the
  Log tab should:
  - be **selectable text** (currently a plain label — can't copy the
    path).
  - gain two buttons: **Open log folder** and **Open log file** (open
    the containing directory / the file itself in the OS default
    handler).
- ~~**Device picker shows each device twice.**~~ **Fixed.** Root cause:
  `protocol.IsVendorUsagePage` excluded only the standard keyboard/generic-
  desktop usage pages, but this keyboard also exposes a Consumer Control
  collection (`0x000c`) and an unlabelled `0xffef` one that both slipped
  through the same "not those two" filter, and separately the confirmed
  vendor page (`0xff1c`) shows up on two different interfaces of the same
  physical wireless receiver. `IsVendorUsagePage` now matches `0xff1c`
  directly; `internal/k724.Enumerate` dedupes on `(VID, PID)` — see
  `buildTargets` in `device.go` and the real-hardware capture fixture in
  `device_test.go`. Verified live: `setclock -list` went from four lines
  to two against a real (if only USB-topology-visible) K724.

## Info tab

~~Add an **Info** tab~~ **Done.** `cmd/k724/tabs.go` `buildInfoTab`, wired
into the tab bar in `main.go`, shows:

- the connected keyboard's **firmware versions** — KB and AP — read from
  the same cached `Device.Firmware()` the mismatch banner already uses
  (see `README.md` → "Firmware compatibility" and `docs/COMMANDS.md` →
  "Command `0x03`").
- the **product / device identity** — product string, VID:PID, wired vs
  wireless label, HID path.
- **battery** — new: `protocol.CmdBattery` (`0x1A`) + `Device.Battery()`,
  shown with an explicit "hypothesis, not confirmed" caveat (see
  `internal/protocol/battery.go` and `docs/COMMANDS.md`'s `0x1A` entry —
  only one capture of this reply exists, always at 100%, over both wired
  and wireless).
- k724-tool's **own version** — the git revision Go embeds automatically
  via `runtime/debug.ReadBuildInfo()`, falling back to `"dev"` for a
  `go run` build with no VCS info embedded.

A "Refresh" button re-reads firmware + battery on demand; both also
refresh automatically on connect.

## Logging — finer levels, quieter default

**Backend done; UI exposure still open.** `internal/applog` now has a
`Level` type (`LevelDebug`/`LevelInfo`/`LevelWarn`/`LevelError`),
`Debugf`, `SetLevel`/`GetLevel`, and `ParseLevel`; the default threshold
is `LevelInfo`, overridable with the `K724_LOG_LEVEL` env var so a bug
report can be captured at `DEBUG` without a rebuild already, just not yet
from the running GUI. All of the noisy lines called out below moved to
`Debugf`: the settings-block hex dumps, `readSettings: flushed …`,
both `runSteps: …` lines, and the `openPath …: flushed / probe OK`
chatter. `ApplySettingsAt` now only logs its decoded `settings write:
effect=5 …` line at `INFO` when the write actually changes a field other
than the timestamp (a bare clock sync no longer produces a "changed"
line); a plain `ReadSettings` never logs above `DEBUG`. `attach()` adds
one new `INFO` "connected: …" line. Verified with a throwaway harness
against the simulator: a clock-only sync + one real settings change
produced 5 `INFO` lines total (vs. 18 at `DEBUG`).

~~Still wanted: expose the level in the UI.~~ **Done.** The Log tab
(`buildLogTab` in `cmd/k724/tabs.go`) has a `Level:` selector next to
Refresh/Copy, seeded from `applog.GetLevel()` and calling
`applog.SetLevel` on change, so `DEBUG` is reachable without
`K724_LOG_LEVEL` or a restart.
