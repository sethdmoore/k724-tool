# k724-tool

A Linux tool for the Redragon K724-RGB-PRO keyboard. The Windows control
app has no Linux equivalent. This repo holds the reverse-engineered wire
protocol and clean-room clients that replace it.

Device identity (confirmed with `lsusb`):
- wired keyboard: `320f:511b`
- 2.4 GHz wireless receiver: `320f:511c`

## Wired vs wireless

Both endpoints speak the same 64-byte report protocol, but the **wired
keyboard is the better-understood target**. Almost everything in `docs/`
was reverse-engineered from wired captures — the 49-byte settings-block
byte map, the screen-upload sequence, the `0x03` descriptor/firmware
block, and the per-key colour table all come from wired `.pcapng`s
(mostly the session-4 set). There is exactly **one** wireless capture
(session 3), and it is also the one whose stale `0xCCCCCC` colour bytes
are the "all keys white" hazard described under "Known issue" below.

The read-modify-write clients (`cmd/setclock` and the GUI) are safe on
**either** endpoint: they read the live settings block first and change
only the field you asked for, so nothing gets blind-written. The one
thing that is still wired-specific is the connection probe — the wired
keyboard must never be sent the `0xAA` ping (see "Connection probe"
under GUI). `cmd/setclock` still defaults to the wireless receiver for
historical reasons; pass `-wired` when the keyboard is on USB.

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
  safe on either endpoint). `setclock -list`, `setclock`, `setclock -wired`.
  No flags targets the wireless receiver; `-wired` targets the keyboard,
  which is the path with more capture data behind it (see "Wired vs
  wireless" above).
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

One window, six tabs:

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
  25 frames. Frame delay runs 50–50000 ms (slider for the fast range,
  entry for an exact value; the device does not appear to play back
  faster than 50 ms, so the tool won't send less). The preview is the
  decoded RGB565 frame at a chosen zoom with nearest-neighbour scaling,
  so it shows the crop and 16-bit colour exactly as the screen will.
  Upload needs the wired keyboard.
- **Info** — a read-only "about this keyboard + about this tool" panel:
  connection identity (product, VID:PID, wired/wireless, HID path), the
  KB/AP firmware versions the mismatch banner already checks, battery
  charge, and k724-tool's own build revision. Battery reads command
  `0x1A`; that byte map is a hypothesis, not a confirmed field (see
  `docs/COMMANDS.md`), and the tab says so.

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

## Testing without hardware

`internal/k724` has a built-in simulator: an in-process stand-in for the
keyboard's vendor HID endpoint that answers `0x01`/`0x02`/`0x03`/`0x05`/
`0x06`/`0x0b`/`0x1A`/`0x21`/`0x23`/`0xAA` the way the documented byte
offsets say the real firmware would. It implements the same small interface `Device`
drives the real hidapi handle through, so probing, the settings
read-modify-write, the per-key colour table, and the screen upload chunking
all run through the exact same code as a real keyboard — only the transport
underneath changes. It is not a firmware emulator (no timing quirks, no
dropped reports, no undecoded fields modelled), which is enough to develop
and test client-side logic without a physical unit.

Set `K724_SIM=1` and both the GUI and `cmd/setclock` pick it up with no
other changes — `Enumerate` lists a simulated wired keyboard and wireless
receiver (`sim://wired`, `sim://wireless`) alongside anything real:

```
K724_SIM=1 go run ./cmd/k724                       # GUI against a virtual keyboard
K724_SIM=1 go run ./cmd/setclock -path sim://wired  # CLI, wired
```

`K724_SIM_KB_VERSION` / `K724_SIM_AP_VERSION` (hex, e.g. `0100`) override the
simulated firmware versions the `0x03` descriptor reports, to test the
firmware-mismatch banner without a real out-of-date unit. `K724_SIM_BATTERY`
(`0`–`100`) overrides the simulated battery percentage, defaulting to `100`.
See `internal/k724/sim.go` and `sim_test.go`.

The device layer logs through `internal/applog`, which defaults to `INFO`
(connect/disconnect, a decoded settings write only when something besides
the clock actually changed, upload start/finish, errors) and drops the
per-command hex dumps and step-sequence bookkeeping to `DEBUG`. Set
`K724_LOG_LEVEL=debug` to see everything — useful together with `K724_SIM`
to watch the full wire-level exchange without touching hardware.

## Status

- **Clock** — `cmd/setclock` and the GUI both do the read-modify-write:
  read the block with `0x05`, stamp the time, write back with `0x06`.
  The settings-block byte map that makes the RMW safe is confirmed
  against four wired captures. The one live end-to-end clock round-trip
  in a capture was over the wireless receiver (session 3), before the
  RMW clients existed — that same capture is where the dangerous
  `0xCCCCCC` colour bytes came from.
- **Lighting / polling** — the settings-block fields are confirmed on
  the wire (`docs/PROTOCOL.md`); the GUI writes them via read-modify-
  write. Not yet round-tripped against hardware through the GUI.
- **Screen** — the upload sequence and RGB565 format are confirmed
  byte-for-byte against the wired screen captures (`docs/SCREEN.md`);
  the encoder has a golden test against those exact device bytes.
- **Firmware check** — the `0x03` reply is identical across all five wired
  captures; the KB/AP version offsets are cross-checked against the Windows
  app's `V%04x` format string and its Settings table, and
  `internal/protocol` has a golden test against the captured bytes. Confirmed
  for the read; the KB-vs-AP record assignment is a strong hypothesis.
- **Battery** — command `0x1A` is confirmed to exist and answer
  `64 01 00 00 00 00` (wireless) / `64 02 00 00 00 00` (wired). `0x64` = 100
  lining up with "100%" is a hypothesis: only one capture of this reply
  exists, and it was never taken at a level other than 100%, so the exact
  byte-to-percent mapping is unconfirmed. The Info tab shows it with that
  caveat.

## Known issue: `python/set_clock.py` still does a blind write

This is the one place the wired/wireless split still matters. The Python
script sends the clock as a fixed 49-byte payload captured over the
wireless receiver. That payload's bytes 6–8 are a custom **RGB colour**
of `0xCCCCCC`; the wired firmware applies it and forces every key to
near-white (recoverable only by a power cycle). The Go clients
(`cmd/setclock`, the GUI) don't have this problem on either endpoint —
they read the current block first and change only the timestamp. If you
must use the Python script, keep it on the wireless receiver.
