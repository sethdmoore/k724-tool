# K724-RGB-PRO command ID reference

This table lists every command ID found on the `0x04`-marker command
channel described in `docs/PROTOCOL.md`. See that file for the report
framing, and the confidence key below for what each label means.

## Confidence key

- **Confirmed** — a live USB capture backs the claim directly.
- **Hypothesis** — static reverse engineering (Ghidra decompilation,
  `strings`, JSON/XML inspection) backs the claim, but no capture has
  verified it.
- **Unknown** — neither method has produced an answer.

A command ID can be confirmed to exist on the wire while its exact
purpose or byte-level meaning stays a hypothesis. The table marks the
two levels separately where useful.

## Source

Two capture sets now exist:

- Session 3, through the `320f:511c` **wireless receiver**: app launch,
  a settings/status dump, the clock read-and-correct sequence from
  `docs/PROTOCOL.md`, and a device status query. Decoded with
  `re_notes/parse_usbmon.py`.
- Session 4, through the `320f:511b` **wired keyboard** (`../captures/`):
  app launch, a USB polling-rate change, light-preset cycling, and
  several TFT screen image/animation uploads. Decoded with
  `python/parse_screen_capture.py`.

Command builder functions were identified by decompiling the 18 known
callers of the transaction functions `FUN_004817f0` and `FUN_00482300`
(see `docs/PROTOCOL.md` for that call chain).

## Command table

| ID | Shape | Purpose | Confidence |
|---|---|---|---|
| `0x01` | No payload | Sent at the start of a session and again right before a write batch. Builder: `FUN_00482530`. | Confirmed on the wire. Exact role ("begin transaction") is a hypothesis. |
| `0x02` | No payload, 10 ms delay before send | Commit/apply, sent once after each write batch. Builder: `FUN_00482740`. | Confirmed on the wire and confirmed necessary for a write to take effect (used by `python/set_clock.py`). |
| `0x03` | Multi-chunk read | Reads a device descriptor block. The 37-byte reply carries the wired keyboard's VID/PID (`320f:511b`, little-endian) **and the KB / AP firmware versions** at fixed offsets — see "Command `0x03`: the descriptor block" below. Builder: `FUN_00482b70`. | Confirmed on the wire: identical across all five wired captures, VID/PID decode confirmed, and the version fields cross-checked against the Windows app's `V%04x` format string and its Settings-window firmware table. The tag bytes and the trailing 6 bytes are not decoded. |
| `0x05` | Multi-chunk read at a device offset | Generic "read N bytes at offset." Used to read the keyboard's stored clock before a correction (see `docs/PROTOCOL.md`). Builder: `FUN_00484b10`. | Confirmed. |
| `0x06` | Multi-chunk write at a device offset | Generic "write N bytes at offset." Writes the 49-byte settings block at offset 0 that carries the RTC timestamp **and** the screen frame count + frame interval (see `docs/PROTOCOL.md` for the clock view and `docs/SCREEN.md` for the screen-config view — they are the same block). Builder: `FUN_00484890`. | Confirmed on the wire in two captures. The block's fixed-template bytes differ wired vs wireless. |
| `0x07` | Multi-chunk read | Reads a table with a 3-byte-per-entry layout. The capture's bytes for the first entries (`20 00 29`, `20 00 3a`, `20 00 3b` ...) match `Keyboard.json`'s `Device[].KeyList[].Assignment` field exactly (e.g. `Assignment: 2097193` = `0x200029`). Builder: `FUN_004835e0`. | Confirmed on the wire that this command exists and returns this table. The claim that it is the live key-assignment table (matched against `Keyboard.json`) is a strong hypothesis. |
| `0x09` | Write, chunked at 56 bytes (offsets `0x00/0x38/0x70/0xa8/0xe0/0x118/0x150`, 384 bytes total) | **Key-remap table save** ("Button Settings"). **Confirmed on the wire** (`button_write_j_default_key.pcapng` etc.): 128 entries × 3 bytes, entry `n` = the action for physical key slot `n`. Plain keys are `20 00 <hid-usage>` (`0x29` Esc, `0x3a`-`0x45` F1-F12, `0x14` Q, `0x04` A, …); modifiers and specials use other prefixes (`20 02 00` LShift, `a0 2a 00` Fn-ish). This table's order is the canonical key-slot order, reused by `0x0B`. Builder: `FUN_00482e50`. |
| `0x0A` | Read, chunked at 56 bytes | Per-key RGB lighting profile load (RE_STATUS, traced to `FUN_004a4550`/`FUN_004ae870`/`FUN_004ae510`). Builder: `FUN_00483b30`. | **Confirmed on the wire** (session 4): the app issues a 342-byte `0x0A` read at startup, all-zero request body. Purpose (lighting profile) still a hypothesis. |
| `0x0B` | Write, chunked at 56 bytes (offsets `0x00/0x38/0x70/0xa8/0xe0/0x118/0x150`, 384 bytes total) | **Per-key RGB colour table save** (the "Custom" lighting effect). **Confirmed on the wire** (`write_light_a-r_s-g_d-b_q-w_e-bk.pcapng`): 128 entries × 3 bytes (R, G, B), one per key slot in the same order as `0x09`. Setting A/S/D/Q/E to red/green/blue/white/black changed entries 49/50/51/33/35. The app follows the `0x0B` batch with a `0x06` settings write that sets effect = `0x13` (Custom) and the global colour (bytes 6-8) to the last-picked colour. Builder: `FUN_00484340`. See `docs/RGB_LIGHTING.md`. |
| `0x0C` | — | Listed with a "(?)" in the original static-analysis notes — existence as a real command builder was not confirmed even in that pass. | Unknown. |
| `0x12` | Write loop, uses the alternative transaction function `FUN_00482300` (shorter timeout than `FUN_004817f0`), gated by a flag on the device object | Unidentified. Only caller: `FUN_004a1ff0`. Builder: `FUN_004845e0`. | Hypothesis that it exists. Purpose unknown. Possibly firmware/AP-update related, given the distinct timeout path, but this is not confirmed. |
| `0x15` | Write loop, offset always starts at `0` | Unidentified. Callers: `FUN_004a0d10`, `FUN_004bcb40`. Builder: `FUN_00483110`. | Hypothesis that it exists. Purpose unknown. |
| `0x17` | Write loop, fixed 56-byte (`0x38`) chunks | Macro "burst-fire" (`ShootData_`) save-to-device, called only from `FUN_004bdc00` (confirmed by this session's decompile — matches RE_STATUS). Builder: `FUN_004833a0`. | Hypothesis (static analysis only). |
| `0x1A` | Single read, a 2-byte value from the caller placed in the offset field | Confirmed on the wire: request all-zero, response `64 01 00 00 00 00`. `0x64` (100) is consistent with a percentage or status value, but the field is not identified. Builder: `FUN_00484da0`. | Confirmed on the wire. Meaning of the response is a hypothesis. |
| `0x1B` | Multi-chunk read, item count divided by 3 in the read loop | Confirmed on the wire: response is a table of ascending index-like bytes (`00 01 02 03 04 05 ... 0c ff ff ff 10 11 ...`) grouped naturally into 3-byte entries. Possibly a key-position-to-LED-index map, but not confirmed. Builder: `FUN_00483880`. | Confirmed on the wire. Meaning of the table is a hypothesis. |
| `0x1C` | No payload, same shape as `0x01`/`0x02`/`0x23` | Unidentified. Only caller: `FUN_004cc920`. Not seen in this capture. Builder: `FUN_00482960`. | Hypothesis that it exists. Purpose unknown. |
| `0x21` | Write loop with a **24-bit (3-byte) little-endian device offset** at report bytes 5-7, unlike every other write command's 16-bit offset. Chunk length `0x38` (48 for a frame slot's final chunk). | **The TFT screen frame push. Confirmed** by a live capture (`docs/SCREEN.md`): the payload is big-endian RGB565, 240x135, 64800 bytes per frame, one frame per `0x10000`-byte device-offset slot, streamed continuously with no inter-frame delimiter. Decoded gradient frames match the source BMPs byte for byte. Builder: `FUN_00483fd0`, callers `FUN_004b1220` / `FUN_004b15d0`. |
| `0x23` | No payload, same shape as `0x01`/`0x02`/`0x1C` | **Confirmed** on the wire: sent once, immediately before the run of `0x21` chunks, after the `0x06` config write and its `0x02` commit. "Begin bulk image transfer" marker — the delimiter that marks the start of a pixel stream. Builder: `FUN_00483dc0`. |
| `0xAA` | No payload | Ping / connection probe. Sent at the start of a session and then repeated roughly every 3 seconds for the life of the app (confirmed in the capture as a periodic heartbeat, not just a one-time probe). Builder: `FUN_00484f90`. | Confirmed. |

## Command `0x03`: the descriptor block

The wired open sequence sends `0x01`, then a bare `0x03` read of 37 bytes at
offset 0. Every wired capture (`../captures/open_redragon.pcapng`,
`change_polling_*`, `light_presets`, `gradient`,
`new_captures/button_write_j_default_key.pcapng`) returns the **same** reply
body:

```
offset  bytes            meaning
------  ---------------  ---------------------------------------------------
 0- 1   aa 55            reply tag
 2      00
 3      03               command-id echo
 4      80               flags?
 5      18               payload length (0x18 = 24)
 6      00
 7      02               record count (2)
 8-19   00 x12           reserved / zero
20-23   0f 32 1b 51      USB VID/PID, little-endian -> 320f:511b
24-26   00 01 00         firmware record 0: tag 0x00, version 0x0100 -> "V0100"
27-29   06 02 06         firmware record 1: tag 0x06, version 0x0206 -> "V0206"
30-35   f0 87 03 12 55 00  not decoded (serial or checksum)
```

(Offsets above are into the 37-byte reply **body**; add 8 for the position in
the raw 64-byte report.)

Each firmware record is `{tag, version_hi, version_lo}`. The version is a
big-endian `uint16` rendered `V%04x` — the exact format string the Windows
control app uses (visible with `strings -e l K724-RGB-PRO.exe`, alongside a
literal `V0100` default). That app's Settings window shows the same pair:

| Name | Version Information | Update Status |
|------|--------------------|----------------|
| KB   | V0206              | Latest Version |
| AP   | V0100              | Latest Version |

so record 1 (`V0206`) is the **keyboard** firmware and record 0 (`V0100`) is
the **2.4 GHz receiver / AP** firmware. Only one keyboard on one firmware
revision backs these offsets, so the split into `{tag, version}` and the
record-to-device assignment are a strong hypothesis, not proven.

`internal/protocol/descriptor.go` parses this block;
`protocol.SupportedKBVersion` / `SupportedAPVersion` hold `0x0206` / `0x0100`,
and `k724.Device.Firmware()` compares a connected unit against them. The GUI
shows a ⚠️ banner and `cmd/setclock` prints a ⚠️ line on any mismatch.

Secondary signal: command `0x1A` returns `64 01 …` through the receiver
(session 3) and `64 02 …` through the wired keyboard — the second byte tracks
the connected device's **major** version (AP `01`, KB `02`). Not used by the
tool; noted here as corroboration.

## Gaps

IDs never seen as a command builder in either the capture or the
static trace of `FUN_004817f0`/`FUN_00482300` callers: `0x04`, `0x08`,
`0x0D`-`0x11`, `0x13`-`0x14`, `0x16`, `0x18`-`0x19`, `0x1D`-`0x20`,
`0x22`. These can be unused, or built by a code path this
investigation did not get to (the approximately 13 "page dispatcher"
functions listed in `RE_STATUS.md` that were not individually
decompiled).
