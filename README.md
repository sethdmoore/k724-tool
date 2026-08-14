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

The clock-set command (`0x06`) is fully confirmed against real
hardware. See `docs/` for the rest of the protocol (RGB lighting, the
TFT screen/GIF path) — confirmed where a live capture backs it, marked
as a hypothesis where it does not.
