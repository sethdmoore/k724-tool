# k724-tool

A Linux tool for the Redragon K724-RGB-PRO keyboard. The Windows control
app has no Linux equivalent. This repo holds the reverse-engineered wire
protocol and clean-room clients that replace it.

Device identity (confirmed with `lsusb`):
- wired keyboard: `320f:511b`
- 2.4 GHz wireless receiver: `320f:511c`

## Firmware compatibility

Everything here was reverse-engineered from one keyboard on one firmware
revision. In the Windows app's **Settings** screen that is the table:

| Name | Version Information | Update Status |
| -- | -- | -- |
| KB | V0206 | Latest Version |
| AP | V0100 | Latest Version |

`KB` is the keyboard's own firmware, `AP` is the 2.4 GHz receiver. This tool
targets **KB V0206 / AP V0100**.

The tool reads those versions itself: the `0x03` descriptor block returned
during the wired connect carries both (see
[`docs/COMMANDS.md`](docs/COMMANDS.md) → "Command `0x03`: the descriptor
block"). When a connected keyboard reports anything else:

- the **GUI** shows a ⚠️ banner under the device picker —
  *"Firmware mismatch: this unit reports KB … / AP …, but this tool was
  reverse-engineered on KB V0206 / AP V0100. It may not work as expected."*
- **`cmd/setclock`** prints the same ⚠️ line to stderr and still proceeds.

The warning is advisory — nothing is blocked. A different firmware may move a
settings-block byte or change the screen-upload sequence, so treat a mismatch
as "verify against a fresh USB capture before trusting a write." The 2.4 GHz
receiver does not report a version on its probe, so a wireless connection is
never version-checked.

## Layout

- `cmd/k724/` — the **desktop GUI** (Fyne). Clock, lighting, USB polling
  rate, and TFT-screen image/animation upload in one window. See
  "GUI" below.
- `cmd/setclock/` — a Go CLI that just sets the clock (read-modify-write,
  wired-safe). `setclock -list`, `setclock`, `setclock -wired`.
- `python/set_clock.py` — the original `hidapi` client. Still does a
  blind write — see the note below.
- `internal/protocol/` — the pure wire-protocol code: report framing,
  the checksum, the 49-byte settings block, and the screen-upload step
  sequence. No cgo, no hardware.
- `internal/screen/` — pure image → 240×135 big-endian RGB565 encoder.
- `internal/k724/` — the device layer: enumerate, open, probe, transact.
  The only package that uses cgo (through `go-hid`) and touches hardware.
- `packaging/` — udev rule, `install.sh`, `.desktop` entry, icon.
- `docs/` — the full protocol write-up, from a live USB capture and
  static analysis of the Windows app (`Ghidra`, `strings`):
  - [`docs/PROTOCOL.md`](docs/PROTOCOL.md) — the 64-byte report
    framing and the confirmed clock command. Start here.
  - [`docs/COMMANDS.md`](docs/COMMANDS.md) — every known command ID,
    with a confirmed/hypothesis/unknown label for each.
  - [`docs/RGB_LIGHTING.md`](docs/RGB_LIGHTING.md) — the per-key
    lighting report format and the app's effect-mode names.
  - [`docs/SCREEN.md`](docs/SCREEN.md) — the TFT screen frame format
    and the state of the frame-upload investigation.
  - [`docs/MISSING_FEATURES.md`](docs/MISSING_FEATURES.md) — a running
    list of Windows-app features the tool does not have yet.

## GUI

```
go run ./cmd/k724        # or: packaging/install.sh
```

On Hyprland/Wayland, build with `-tags wayland` so the window gets a
real app-id (`com.github.k724tool.k724`); otherwise it runs under
XWayland with a generic class.

One window, four tabs:

- **Clock** — "Sync to system time", or type a specific
  `YYYY-MM-DD HH:MM:SS` and set that.
- **Lighting** — effect, brightness (0–5), speed (0–5), custom colour.
  Loads the keyboard's current values on connect; Apply writes back
  **only the controls you changed** (the colour bytes are never
  blind-written — that was the all-keys-white bug). Below that, a
  **per-key colour** grid for the "Custom" effect: set a paint colour
  and its brightness, click keys, Apply — the tool writes the 128-entry `0x0b` table and
  switches the keyboard to Custom. Each key keeps its own brightness
  (folded into its colour on the wire, as the Windows app does). The
  keyboard can't report the table back, so the tool remembers it between
  sessions. Wired only.
- **Polling** — 1000 / 500 / 250 / 125 Hz.
- **Screen** — add one or more PNG/JPEG/BMP/GIF files (a GIF expands to
  its frames). Frames sit on a horizontal timeline you can reorder;
  removing one drops it into a restorable "Removed frames" strip. Up to
  25 frames. Frame delay goes up to 50000 ms (slider for the fast range,
  entry for an exact value). The preview is the decoded RGB565 frame at a
  chosen zoom with nearest-neighbour scaling, so it shows the crop and
  16-bit colour exactly as the screen will. Upload needs the wired
  keyboard.

All of Clock / Lighting / Polling are **read-modify-write**: the tool
reads the 49-byte settings block with command `0x05`, changes only the
field you touched (plus a fresh timestamp, matching the Windows app),
and writes it back with `0x06`. A ~15 ms pause before the `0x02` commit
mirrors the Windows app's timing. Each read first flushes any report
left queued by the previous operation, so replies can't drift one slot
out of step.

Device operations run one at a time. While any write — or a screen
upload — is in flight, every tab's action controls and the device
picker are disabled; they re-enable when it finishes. This keeps two
writes from being queued on top of each other (e.g. a clock change
during an upload) and stops rapid clicks from piling up work.

The `0x05` read is issued bare, with no `0x01` in front of it, matching
`open_redragon.pcapng` — `0x01`/`0x02` bracket only the `0x06` write. A
leading `0x01` opens a transaction the read path never commits.

Connection probe: the wireless receiver is probed with a `0xAA` ping
(the session-3 sequence). The **wired keyboard is never pinged** — every
wired capture opens with `0x01` then a `0x03` descriptor read and no
`0xAA` at all. Sending `0xAA` to the wired keyboard drops its onboard
lighting to solid white until a power cycle, which no later settings
write clears, so the wired probe is `0x01` + a `0x03` descriptor read
whose reply must contain the keyboard's VID/PID. That same reply also
carries the KB / AP firmware versions, which the tool checks against the
ones it was built for — see "Firmware compatibility" above.

Linux needs a udev rule for non-root HID access; `packaging/install.sh`
installs it, or copy `packaging/70-redragon-k724.rules` to
`/etc/udev/rules.d/` yourself. macOS and Windows need nothing.

Build deps (developers only, not end users): a C compiler, and on Linux
`libgl1-mesa-dev xorg-dev libudev-dev`.

## Status

- **Clock** — `cmd/setclock` and the GUI both do the read-modify-write:
  read the block with `0x05`, stamp the time, write back with `0x06`.
  The clock write itself is confirmed on real hardware (over the
  wireless receiver); the byte map that makes the RMW safe on wired is
  confirmed against four wired captures.
- **Lighting / polling** — the settings-block fields are confirmed on
  the wire (`docs/PROTOCOL.md`); the GUI writes them via read-modify-
  write. Not yet round-tripped against hardware through the GUI.
- **Screen** — the upload sequence and RGB565 format are confirmed
  byte-for-byte against a wired capture (`docs/SCREEN.md`); the encoder
  has a golden test against those exact device bytes.
- **Firmware check** — the `0x03` reply is identical across all five wired
  captures; the KB/AP version offsets are cross-checked against the Windows
  app's `V%04x` format string and its Settings table, and
  `internal/protocol` has a golden test against the captured bytes. Confirmed
  for the read; the KB-vs-AP record assignment is a strong hypothesis.

## Known issue: `python/set_clock.py` still does a blind write

The Python script sends the clock as a fixed 49-byte payload captured
over the wireless receiver. That payload's bytes 6–8 are a custom
**RGB colour** of `0xCCCCCC`; the wired firmware applies it and forces
every key to near-white (recoverable only by a power cycle). Use
`cmd/setclock` or the GUI, which read the current block first and change
only the timestamp. If you must use the Python script, keep it on the
wireless receiver.
