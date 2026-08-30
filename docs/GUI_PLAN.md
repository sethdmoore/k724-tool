# GUI tool implementation plan (Fyne)

Status: **M0–M3 built** (first coding session). `cmd/k724` runs: device
picker + probe + permission dialog, and Clock / Lighting / Polling /
Screen tabs. `internal/protocol` gained `settings.go` (49-byte block +
accessors + read/write steps), `screen.go` (`UploadSteps`), and
`BuildReportWide` (24-bit offset). `internal/screen` is the pure RGB565
encoder with a golden test against the device capture. `internal/k724`
is the cgo device layer, lifted from `cmd/setclock`. `packaging/` and
`.github/workflows/build.yml` are in place.

**Task 0 (0x05 read) is done** — the wired `open_redragon.pcapng`
capture shows the full read-modify-write: `0x05` reads 49 bytes at
offset 0 (request body = 49 zeros, `len`=49), the app patches only bytes
35-41, writes back with `0x06`. Byte map verified against all four wired
captures. So Clock / Lighting / Polling all do true RMW and are
wired-safe. `internal/k724.Device.ApplySettings` is the RMW primitive.

`cmd/setclock` is now ported onto `internal/k724` (read-modify-write,
wired-safe); the old blind `protocol.ClockSteps`/`ClockPayload` stay in
the tree only as deprecated reference. `python/set_clock.py` is still
the blind write.

Session 2 changes (from user testing on real hardware):
- **Wireless works well** via the GUI (effects/colour/brightness). **Wired
  whited out the keys.** Root-caused two client bugs, not protocol:
  (1) the Lighting tab's Apply blind-wrote the colour bytes from a UI var
  that defaulted to white — now it writes *only* the controls the user
  changed, and never the colour unless a colour was actually picked;
  (2) the `0x02` commit fired immediately after the `0x06` write's reply,
  where the Windows app leaves ~20 ms (`light_presets.pcapng`,
  `docs/COMMANDS.md`) — `k724.RunStepsCtx` now sleeps `commitDelay`
  (15 ms) before every commit. `light_presets.pcapng` confirms wired
  preset changes are `0x01`→`0x06`→`0x02` only, byte-identical to ours,
  so timing/blind-write were the only differences.
- **Frame cap raised to 25** (user has run a 25-frame GIF via the Windows
  app). Byte 22 re-checked against `screen.pcapng`: it is the polling
  index, NOT frame count — only byte 34 is frame count. `SCREEN.md` fixed.
- **Screen tab reworked**: add multiple files (GIF expands to frames),
  reorder / delete / clear, per-frame thumbnails, a play/step preview,
  and a zoomable nearest-neighbour preview rendered from the actual
  RGB565 (`screen.Decode`) so the crop + 16-bit colour are visible.
- **Clock tab**: added a manual `YYYY-MM-DD HH:MM:SS` entry +
  `Device.SetClock(when)`.

Session 3 changes:
- **Screen tab transparency matte.** The TFT is opaque RGB565, so
  transparent source pixels used to flatten to solid black silently.
  `internal/screen`: `Frame` now takes a `matte color.Color` (nil = opaque
  black, unchanged output — golden test still byte-exact); `HasAlpha`
  detects transparency; new `DecodeFrames` returns native-size sources plus
  a "had transparency" flag and `Frames` wraps it. The Screen tab keeps the
  decoded `src` per frame, and the first time an added image has
  transparency it reveals a background-colour picker that re-encodes every
  frame over the chosen matte.

Session 4 changes (new captures in `redragon/new_captures/`):
- **Per-key custom colours implemented.** `write_light_a-r_s-g_d-b_q-w_e-bk.pcapng`
  decoded: command `0x0b` writes a 384-byte / 128-entry × RGB table in the
  `0x09` key-remap table's slot order, followed by a `0x06` settings write that
  sets effect `0x13` (Custom) + the global colour. New `protocol/customkeys.go`
  (`KeyColorTable`, `KeyColorWriteSteps`), `protocol/keymap.go` (75 % ANSI
  layout from the `0x09` HID-usage decode), `k724.Device.ApplyKeyColors`. The
  Lighting tab gained a clickable keyboard grid; the table is persisted to
  `fyne.Preferences` (no read-back exists). The grid keeps an RGBA model — RGB
  is the hue, alpha is that key's **own brightness** (0-100 %), matching
  `CustomLightMode.LightColorInfo[*].Alpha` in the saved profile (values like
  201, not just 0/255) — and derives each 3-byte wire entry as
  `round(channel * alpha / 255)`. Every colour control now goes through one
  helper, `App.pickColour` (Fyne's advanced picker); the RGB-slider
  `showOpaqueColourPicker` stays only for the screen-matte control.
- **Frame interval is 16-bit LE at bytes 43-44**, not one byte. Confirmed by a
  live `0x05` read (`interval=50000ms`, `50 c3`). `SetFrameIntervalMS` clamps to
  `FrameIntervalMax` (50000); the Screen tab's delay control is now a coarse
  slider plus an exact-ms entry.
- **Settings block bytes 19/20/21** decoded from the `toggle_*.pcapng` captures:
  WASD↔arrow swap, N-key rollover, Windows-lock. Accessors added to
  `SettingsBlock`; not yet surfaced in the UI (a future "Other Settings" tab).
- **Screen timeline is horizontal**, and deleted frames go to a restorable
  "Removed frames" strip instead of being dropped.

Still not done: confirm the wired white-out is actually fixed on
hardware; macOS Input-Monitoring check; per-key colours; whether >3
frames keeps the `0x10000` slot stride (assumed, user says the Windows
app does 25).

Run the GUI on Hyprland with `go run -tags wayland ./cmd/k724` — the
`wayland` tag makes Fyne set the app_id to `com.github.k724tool.k724`,
which the window rule in `~/dotfiles/hyprland/.config/hypr/window_rules/
k724.lua` parks on workspace 1.

Original brief follows. Read `docs/PROTOCOL.md`, `docs/COMMANDS.md`, and
`docs/SCREEN.md` first — this plan assumes their byte-level findings.

## 1. Goal

A desktop GUI that replaces the Windows control app for the K724-RGB-PRO:

- set the onboard clock
- pick the lighting effect / brightness / speed / custom colour
- set the USB polling rate
- upload a still image or a short animation to the TFT screen

Hard requirements (from the user):

1. Cross-platform: Linux, macOS, Windows.
2. Trivial install.
3. **Zero user-side dependencies** beyond the app itself. No bundled
   browser, no closed-source SDK (this is why WebHID/Chrome, Tauri/Wails
   with WebKitGTK on Linux, and Muon/Ultralight were all rejected).

### Why Fyne

Pure-Go GUI, statically linked, one self-contained binary per OS. The
only runtime library it touches is the system's OpenGL loader, which is
present on every desktop. On Windows and macOS the result is genuinely
zero-dependency. On Linux the binary itself is self-contained; the one
thing the user still needs is a udev rule for non-root `/dev/hidraw`
access (see §9) — that is unavoidable for *any* native HID tool and is a
single file.

## 2. What already exists — reuse it

| Path | What it is | Plan |
|---|---|---|
| `internal/protocol/protocol.go` | pure-Go wire framing: `BuildReport`, `Checksum`, `BCD`, `Step`, `ReplyOK`, `IsVendorUsagePage`, device IDs | keep; **extend** (§5). Stays cgo-free. |
| `cmd/setclock/main.go` | working CLI: hidapi enumerate → `0xAA` probe → auto-select interface → send `Step`s | move the device-I/O half into `internal/k724` (§6); keep `cmd/setclock` as a thin CLI on top. |
| `python/set_clock.py` | reference hidapi client | reference only. |
| `python/parse_screen_capture.py` | capture decoder, RGB565→PNG | the **test oracle** for the screen encoder (§11). |
| `go.mod` | `github.com/sstallion/go-hid v0.15.0`, Go 1.26 | add deps in §3. |

`internal/protocol.ClockPayload` / `ClockSteps` hard-code the session-3
wireless constants, including the `cc cc cc` custom-colour bytes that
brick the wired keyboard (README "Known issue"). **Do not build the GUI
on those.** Replace with the settings-block API in §5.

## 3. Dependencies to add

- `fyne.io/fyne/v2` — pin the latest v2.6.x. Use `fyne.Do(func(){…})` to
  push results from worker goroutines onto the UI thread (2.6 makes this
  mandatory).
- `golang.org/x/image` — `bmp` decoder and `draw` (CatmullRom) for
  scaling. GIF/PNG/JPEG are stdlib.
- `github.com/sstallion/go-hid` — already present. cgo wrapper around a
  vendored hidapi 0.14.x. Links IOKit/CoreFoundation (macOS),
  setupapi/hid (Windows), libudev (Linux). All are OS-provided.

Licence check: Fyne BSD-3, go-hid BSD-3 (bundled hidapi BSD-3),
x/image BSD-3. All permissive, no redistribution burden.

## 4. Package layout

```
cmd/
  setclock/        existing CLI — keep, re-point at internal/k724 after M1
  k724/            NEW — Fyne app, package main, UI only
internal/
  protocol/        existing — pure framing + NEW settings.go, screen.go
  k724/            NEW — device layer (cgo): enumerate, open, probe, transact,
                   and the high-level ops (SetClock, ApplySettings, UploadScreen)
  screen/          NEW — pure: image.Image -> 240x135 BE-RGB565 frame; no cgo
packaging/
  70-redragon-k724.rules
  install.sh  k724.desktop
.github/workflows/build.yml
docs/testdata/     golden bytes for the encoder tests
```

Keep `internal/protocol` and `internal/screen` **cgo-free and hardware-free**
so they unit-test on any machine. All cgo and all device I/O lives in
`internal/k724`.

## 5. `internal/protocol` extensions

### 5a. `settings.go` — the 49-byte global settings block

Command `0x06` write / `0x05` read at device offset 0. Field map is in
`docs/PROTOCOL.md` "Settings block layout". Implement:

```go
type SettingsBlock struct{ Raw [49]byte }

func ParseSettings(b []byte) (SettingsBlock, error)   // len must be 49

// documented fields — getters + setters over Raw:
//  byte 1      LightingEffect  (0x00..0x17, see docs/RGB_LIGHTING.md IDs)
//  byte 2      Brightness      0..5
//  byte 3      Speed           0..5
//  bytes 6..8  Colour          R,G,B
//  byte 22     PollingIndex    0..3  = 1000/500/250/125 Hz
//  byte 34     FrameCount      1..25
//  bytes 35..41 Time           BCD  SS MM HH WD DD MM YY  (WD 0=Sun..6=Sat)
//  byte 43     FrameIntervalMS 0x64=100 … one byte, so max 255 ms

func (b *SettingsBlock) SetTime(t time.Time)          // stamp 35..41
func SettingsWriteSteps(b SettingsBlock) []protocol.Step
    // -> {0x01}, {0x06 offset 0, b.Raw[:]}, {0x02}
```

**Always stamp `time.Now()` into bytes 35..41 on every write**, matching
the observed app behaviour (it re-sends the whole block with a fresh
timestamp whenever any setting changes). That keeps the RTC fresh and
means "set the clock" is just a settings write with no other field
touched.

Doc conflict to resolve first: `docs/SCREEN.md` says byte 22 is frame
count; `docs/PROTOCOL.md` says byte 22 is the polling-rate index and byte
34 is the frame count. **`PROTOCOL.md` is authoritative** — it was diffed
setting-by-setting against `change_polling_*.pcapng`. Verify once against
that capture with `parse_screen_capture.py --all-cmds` before wiring the
polling control, then fix `SCREEN.md`.

### 5b. Read-modify-write vs. blind write

A settings GUI wants to show the device's *current* brightness/effect/etc.
That needs the `0x05` read. **The `0x05` request byte layout is not yet
decoded** — neither client implements it. Task 0 for the writing session:

1. Locate the **session-3 wireless capture** (referenced in
   `~/scratch/redragon/RE_STATUS.md`; it recorded a `0x05` read returning
   the pre-correction time `15:44:52 2026-08-13`). Decode the `0x05`
   request/response with `re_notes/parse_usbmon.py` or
   `parse_screen_capture.py --cmd 0x05 --all-cmds`. Do **not** open the
   pcapng raw.
2. If found: implement `SettingsReadSteps()` + a reply assembler, do true
   RMW. Best UX.
3. If the capture is gone: fall back to writing a **known-safe full
   block**. The wired screen-config captures (`docs/SCREEN.md`) show a
   template that is proven safe on wired hardware — start from that
   constant, patch only the field the user changed plus the timestamp.
   Accept that the GUI can't pre-populate current values; persist the
   last-written block to `fyne.Preferences` instead.

Until RMW works, the Clock tab is still safe on wired (time-only field
change on a safe template); gate the other tabs behind M2.

### 5c. `screen.go` — TFT upload sequence

From `docs/SCREEN.md` "Upload wire sequence":

```go
const (
    ScreenW, ScreenH = 240, 135
    FrameBytes       = ScreenW * ScreenH * 2   // 64800
    FrameSlot        = 0x10000                  // device-offset stride
    chunkFull        = 0x38                     // 56
)

// frames: each exactly FrameBytes of BE RGB565 from internal/screen.
// Emits: {0x01}, settings write (FrameCount, FrameIntervalMS set), {0x02},
//        {0x23}, then 0x21 chunks with a **24-bit LE** device offset that
//        runs continuously across all frames (offset k*FrameSlot + i),
//        56 bytes each, 48 for a slot's final chunk, then {0x02}.
func UploadSteps(frames [][]byte, intervalMS int, base SettingsBlock) []protocol.Step
```

`BuildReport` currently writes a 16-bit offset (bytes 5-6). Add a
`BuildReportWide(cmd, offset uint32, chunk)` (or a `Step.WideOffset bool`)
that puts a 24-bit LE offset in bytes 5-7, for `0x21` only. Checksum
formula is unchanged (`sum(byte[3:8+len]) & 0xFFFF`).

## 6. `internal/k724` — device layer

Lift from `cmd/setclock/main.go`, largely unchanged:

```go
type Target struct{ Path, Product string; VID, PID uint16; Wired bool }

func Enumerate() ([]Target, error)          // VID 0x320f, vendor usage page only
func Open(t Target) (*Device, error)        // OpenPath + 0xAA probe
func (d *Device) Transact(s protocol.Step) (reply []byte, err error)
                                            // Write + ReadWithTimeout(1s) + ReplyOK
func (d *Device) RunSteps(steps []protocol.Step, progress func(i, n int)) error
func (d *Device) Close()
```

`Enumerate` marks `Wired` from the PID (`0x511b` wired, `0x511c`
wireless). The screen path is **wired-only** ("Only the wired mode upload
is supported!" — `docs/SCREEN.md`); the UI uses `Wired` to enable/disable
the Screen tab.

## 7. UI structure (`cmd/k724`)

- `app.NewWithID("…")` (needs a stable app ID for `fyne.Preferences` and
  `fyne package`), one `Window`, `container.NewAppTabs`.
- Top toolbar: device `widget.Select` (populated from `Enumerate`, label
  "Wired keyboard" / "Wireless receiver"), a Refresh button, a status
  label (connected / not found / permission denied).
- Tabs:
  - **Clock** — "Sync to system time" button; optional manual
    `widget.Select`s for date/time; shows the last value written.
  - **Lighting** — effect `widget.Select` (names+IDs from
    `docs/RGB_LIGHTING.md`; byte 1 of the block was confirmed stepping
    `0x01→0x13` in `light_presets.pcapng`, so writing byte 1 directly is
    valid), brightness `widget.Slider` 0–5, speed slider 0–5,
    `dialog.NewColorPicker` for the custom colour (bytes 6–8, full 0–255,
    no 0xF7 clamp on this field).
  - **Polling** — `widget.Select` 1000/500/250/125 Hz → index 0–3.
  - **Screen** — `dialog.ShowFileOpen` (`.png .jpg .jpeg .bmp .gif`),
    a `canvas.Image` preview (cycle GIF frames on a `time.Ticker`),
    frame-interval slider (100–255 ms — one byte), an Upload button with
    a `widget.ProgressBar` and a Cancel. Disabled unless a wired target
    is selected. Cap frame count at 3 for now with a note that 4–25 is
    documented but unverified (`docs/SCREEN.md` "Animation limits").
- **Threading:** every HID call runs on one worker goroutine (serialise
  through a channel of closures — the device is a single mutable
  resource). Push widget updates back with `fyne.Do`. Never call
  `Transact` from a widget callback directly.
- **Errors:** `dialog.ShowError`. On Linux `EACCES`/permission errors,
  show the udev-rule dialog from §9 with the exact file contents and
  path.

## 8. Screen encoder (`internal/screen`)

```go
func Frame(img image.Image) []byte   // scale to 240x135 (draw.CatmullRom,
                                     // preserve aspect + centre-crop), then
                                     // BE RGB565 row-major top-left, 64800 B
func Frames(r io.Reader) ([][]byte, error)  // sniff PNG/JPEG/BMP/GIF; GIF -> N frames
```

RGB565: `v = (r&0xF8)<<8 | (g&0xFC)<<3 | (b>>3)`, big-endian on the wire
(`F8 00` = red). Zero-fill the `64800..65535` slot padding.

## 9. Permissions / udev / install

`packaging/70-redragon-k724.rules`:

```
KERNEL=="hidraw*", SUBSYSTEM=="hidraw", ATTRS{idVendor}=="320f", ATTRS{idProduct}=="511b", TAG+="uaccess", MODE="0660"
KERNEL=="hidraw*", SUBSYSTEM=="hidraw", ATTRS{idVendor}=="320f", ATTRS{idProduct}=="511c", TAG+="uaccess", MODE="0660"
```

`TAG+="uaccess"` grants the logged-in user an ACL on the node (the modern
mechanism; plain `MODE="0666"` was noted as insufficient in the memory
file — cross-check the exact rule that worked in `RE_STATUS.md` before
finalising). Install: `sudo cp` to `/etc/udev/rules.d/`, then
`sudo udevadm control --reload && sudo udevadm trigger`. `install.sh`
does this and drops the binary in `~/.local/bin` plus a `.desktop` file.

- **macOS:** HID needs no driver. Unverified: whether opening the vendor
  usage-page collection (`0xff1c`) trips the "Input Monitoring" prompt —
  **test this on a Mac early in M0.** Unsigned app → Gatekeeper; document
  right-click-Open / `xattr -dr com.apple.quarantine`. Notarisation is a
  later nicety, not a blocker.
- **Windows:** HID works without admin. The vendor collection is separate
  from the OS-claimed keyboard collection, so no exclusive-access
  problem. Unsigned → SmartScreen "More info → Run anyway"; document.

## 10. Build & packaging

- Dev build deps (NOT shipped to users): a C compiler; on Linux
  `libgl1-mesa-dev xorg-dev libudev-dev`; on macOS Xcode CLT; on Windows
  mingw-w64. `go install fyne.io/fyne/v2/cmd/fyne@latest`.
- `fyne package -os {linux,darwin,windows} -icon icon.png -app-id …`.
- **CI:** `.github/workflows/build.yml`, matrix `{ubuntu-latest,
  macos-latest, windows-latest}`, native build per runner (do not try to
  cross-compile cgo), run `go test ./...` + `go vet`, `fyne package`,
  upload artifacts, attach to the GitHub Release on a `v*` tag.
- Cross-compiling from one machine is possible with `fyne-cross` (Docker)
  but the CI matrix is simpler and is the recommended path.

## 11. Testing

- `internal/protocol`: table tests for `BCD`, `Checksum` (against packet
  hexes quoted in `docs/PROTOCOL.md`/`SCREEN.md`), `BuildReportWide`
  (24-bit offset example `04 30 02 21 38 d8 fd 02 …`, offset `0x02fdd8`),
  `SettingsBlock` accessors (against the observed block hex in
  `docs/PROTOCOL.md`), `SettingsWriteSteps`/`UploadSteps` step counts.
- `internal/screen`: golden test. Decode `../captures/grad0.bmp`, run
  `Frame`, compare to a golden `docs/testdata/grad0.bin` produced by
  `parse_screen_capture.py` (it already confirms 64800/64800-byte match
  against the device capture). Round-trip `Frame` → RGB → PNG and diff
  against the python PNG.
- Device layer: no automated tests. Manual checklist against hardware:
  enumerate → probe → set clock → verify on-screen → set a preset →
  set brightness → set polling → upload 1 frame → upload 3-frame anim.
  Keep `cmd/setclock` as the minimal CLI smoke test.
- CI runs `go test ./...`, `go vet`; add `staticcheck` if convenient.

## 12. Milestones

- **M0 — plumbing.** `cmd/k724` skeleton, `internal/k724` device layer
  moved out of `setclock`, device dropdown + probe + status line +
  permission dialog. Test macOS Input-Monitoring behaviour here.
- **M1 — clock.** Settings-block API (§5a). "Sync to system time".
  Time-only field change on a safe template → works on wired *and*
  wireless. Re-point `cmd/setclock` at the shared code.
- **M2 — lighting + polling.** Full settings RMW (needs Task 0, the
  `0x05` decode). Effect / brightness / speed / colour / polling
  controls. Main value-add.
- **M3 — screen.** `internal/screen` encoder + `UploadSteps`, file
  picker, preview, progress bar + cancel. Wired-only.
- **M4 — packaging.** CI matrix, `fyne package`, release artifacts,
  `install.sh`, udev rule, signing docs, README update.

## 13. Open questions / risks

- **`0x05` read framing unknown** — blocks true RMW (§5b). Decode from
  the session-3 capture first; fall back to safe-template writes.
- **byte 22 = polling, byte 34 = frame count** — trust `PROTOCOL.md`,
  verify once, fix `SCREEN.md`.
- **Screen upload is wired-only** — disable the tab for the wireless
  receiver.
- **>3 animation frames** — slot stride and `0x06` block shape only
  verified for 2 and 3 frames. Cap at 3 initially.
- **macOS vendor-collection HID access** — may need "Input Monitoring";
  test on real hardware in M0.
- **Effect-mode IDs** — the ID→name table in `docs/RGB_LIGHTING.md` is a
  UI mapping, but block byte 1 itself is confirmed on the wire, so
  writing it is safe; worst case a name is mislabelled.
- **Per-key custom colours (`0x0A`/`0x0B`)** — out of scope for v1.

## 14. First-session task list

0. Decode the `0x05` read from the session-3 wireless capture (§5b). Time-box it.
1. `go get fyne.io/fyne/v2@latest golang.org/x/image@latest`;
   `go install fyne.io/fyne/v2/cmd/fyne@latest`.
2. `internal/k724/device.go` — move `findCandidates`/`probe`/`openDevice`/
   `sendAndConfirm` out of `cmd/setclock/main.go`; export `Target`,
   `Enumerate`, `Open`, `Device.Transact`, `Device.RunSteps`, `Device.Close`.
3. `internal/protocol/settings.go` — `SettingsBlock` + accessors +
   `SettingsWriteSteps`; `SettingsReadSteps`/parser if Task 0 succeeded.
4. `internal/protocol` — add `BuildReportWide` (24-bit offset) for `0x21`.
5. `internal/screen/encode.go` — `Frame`, `Frames`.
6. `internal/protocol/screen.go` — `UploadSteps`.
7. `cmd/k724/main.go` — Fyne app: toolbar + `AppTabs` (Clock, Lighting,
   Polling, Screen), worker goroutine, `fyne.Do` marshalling.
8. Tests: `settings_test.go`, `encode_test.go` + `docs/testdata/grad0.bin`.
9. `packaging/70-redragon-k724.rules`, `install.sh`, `.github/workflows/build.yml`.
10. Update `README.md` (new GUI section) and clear the "Known issue" once
    M1/M2 make wired safe.
