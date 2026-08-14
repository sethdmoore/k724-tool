# k724-tool

A Linux tool for the Redragon K724-RGB-PRO keyboard. The Windows control
app has no Linux equivalent. This repo holds the reverse-engineered wire
protocol and clean-room clients that replace it.

Device identity (confirmed with `lsusb`):
- wired keyboard: `320f:511b`
- 2.4 GHz wireless receiver: `320f:511c`

## Layout

- `python/set_clock.py` — the first working client, `hidapi`-based.
  Confirmed against real hardware.
- `cmd/setclock/` — a Go port, `internal/protocol/` — the shared wire
  protocol code.
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

## Status

The clock-set command (`0x06`) is confirmed against real hardware
**over the wireless receiver only**. See `docs/` for the rest of the
protocol (RGB lighting, the TFT screen/GIF path) — confirmed where a
live capture backs it, marked as a hypothesis where it does not.

## Known issue: the wired connection is not safe yet

The clock-set payload was captured only over the 2.4 GHz wireless
receiver. A run of `set_clock.py`/`setclock` against the **wired**
keyboard, using that same payload, forced every key's RGB to solid
white. The onboard controls could not clear it; a full power cycle
was needed.

Both clients default to the wireless receiver for this reason.
Targeting the wired keyboard needs an explicit `--wired`/`-wired`
flag, and prints a warning with a 3-second delay before it runs.

The likely cause: the fixed template bytes in the clock payload (see
`docs/PROTOCOL.md`) were captured with the receiver in the path, which
may translate or normalize traffic before it reaches the keyboard's
own firmware. Sent directly over the wired link, the same bytes may
land on a different firmware code path. This is not confirmed — the
fix is a live `usbmon` capture of the real Windows app talking to the
keyboard over a **wired** connection (the same technique documented in
`docs/PROTOCOL.md` for the wireless capture), to see what the wired
protocol actually looks like and diff it against what is implemented
here.

Until that capture happens, treat `--wired`/`-wired` as unverified and
avoid it on hardware you care about.
