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

- **Scale / crop.** A GIF is very unlikely to fit the 240×135 screen at
  its native aspect ratio, so the encoder's centre-crop will usually
  cut off content. Add a scale/crop control with two modes:
  - **All frames linked/locked** — one crop rectangle applied to every
    frame (the common case for a GIF).
  - **Individual frames** — per-frame crop rectangle, for when frames
    were added from different sources.
- **Frame reordering / deletion.** Replace the per-frame
  `[◀] [👁] [▶] [🗑]` button row with:
  - **drag and drop to sort** frames on the timeline.
  - **drag to trash** — a drop target (top-right of the timeline,
    roughly) that a dragged frame can be dropped onto to remove it.
- **Preview pan.** In the zoomed preview image, click and drag to pan
  around the frame (currently the preview is fixed).
- **Frame-delay lower bound is 50 ms.** The Windows app locks the
  interval field to a 50 ms minimum. k724-tool currently allows 10 ms
  (slider `widget.NewSlider(10, 2000)` and the entry, in
  `cmd/k724/tabs.go`; `SetFrameIntervalMS` in
  `internal/protocol/settings.go` clamps to `1..FrameIntervalMax`).
  A test upload at 10 ms does **not** play faster on the device — the
  firmware appears to floor it — so the sub-50 ms range is misleading.
  Add a `FrameIntervalMin = 50` constant, clamp to it in
  `SetFrameIntervalMS`, and raise the slider min + default to 50.

## Keyboard layout diagram — fixes

Nitpicks in the per-key colour grid (`internal/protocol/keymap.go`,
`KeyboardLayout`).

- **Nav column alignment.** Ins / Del / PgUp / PgDn must line up
  perfectly vertically, sitting slightly to the right of the Up arrow.
  Right now each is placed with a `gap(2)` after a differently-sized
  main block on its row, so the column is ragged.
- **Remove the knob.** The volume knob (index 13) has no LED, so it
  should not be in the diagram at all. `keymap.go`'s own header comment
  already says the knob is meant to be omitted like the other special
  `0xa0…` records — the `{"Knob", 13, 4}` entry contradicts that and
  should go.

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
- **Device picker shows each device twice.** The dropdown lists
  "Wired keyboard — Gaming KB" ×2 and "Wireless receiver — 2.4G
  Wireless Receiver" ×2. `internal/k724.Enumerate` is returning more
  than one HID path per physical device (multiple interfaces /
  collections on the vendor usage page). Dedupe so each physical
  device appears once — key on something stable (USB serial, or
  VID:PID + interface, or the container/parent path), not the raw
  hidraw path.

## Info tab

Add an **Info** tab that shows:

- the connected keyboard's **firmware versions** — KB and AP — which
  the tool already reads from the `0x03` descriptor block on the wired
  connect (see `README.md` → "Firmware compatibility" and
  `docs/COMMANDS.md` → "Command `0x03`"). Right now those values are
  only used for the mismatch banner; surface them plainly here.
- the **product / device identity** — model string, VID:PID, wired vs
  wireless, HID path.
- k724-tool's **own version** (build version / commit).

Effectively a read-only "About this keyboard + about this tool" panel.

## Logging — finer levels, quieter default

`internal/applog` currently has only `INFO` / `WARN` / `ERROR`
(`Infof`/`Warnf`/`Errorf`) and **no level filtering** — every line is
written. Everything device-related logs at `INFO`, so the normal Log
tab is a wall of hex.

Wanted:

- **Add a `DEBUG` level** (`Debugf`) and a settable threshold. Default
  threshold stays `INFO`.
- **Demote the noisy lines to `DEBUG`:**
  - the raw settings-block hex dumps (`settings read : [00 05 04 …]`,
    `settings write: [ … ]`).
  - `readSettings: flushed N stale report(s) first`.
  - `runSteps: N step(s), cmds 0x01..0x02` and `runSteps: all N
    step(s) OK`.
  - the `openPath …: flushed / probe OK` chatter.
- **Keep at `INFO`:** one line per user-visible action — e.g. a single
  decoded `settings write: effect=5 brightness=4 …` when a write
  actually changes something, connect / disconnect, upload
  start/finish, errors. A plain read that changes nothing probably
  should not log at `INFO` at all.
- Expose the level in the UI (a selector on the Log tab) and/or an
  env var, so a bug report can be captured at `DEBUG` without a
  rebuild.
