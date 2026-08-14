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

The capture is a real session: app launch, a settings/status dump, the
clock read-and-correct sequence from `docs/PROTOCOL.md`, and a device
status query, all through the `320f:511c` wireless receiver. It is
decoded with `re_notes/parse_usbmon.py`. Command builder functions were
identified by decompiling the 18 known callers of the transaction
functions `FUN_004817f0` and `FUN_00482300` (see `docs/PROTOCOL.md`
for that call chain).

## Command table

| ID | Shape | Purpose | Confidence |
|---|---|---|---|
| `0x01` | No payload | Sent at the start of a session and again right before a write batch. Builder: `FUN_00482530`. | Confirmed on the wire. Exact role ("begin transaction") is a hypothesis. |
| `0x02` | No payload, 10 ms delay before send | Commit/apply, sent once after each write batch. Builder: `FUN_00482740`. | Confirmed on the wire and confirmed necessary for a write to take effect (used by `python/set_clock.py`). |
| `0x03` | Multi-chunk read | Reads a device descriptor block. The capture's response bytes decode to the VID/PID pair `320f:511b` (the wired keyboard's own IDs) embedded at a fixed offset, even though the capture ran through the `511c` receiver. Builder: `FUN_00482b70`. | Confirmed on the wire, including the VID/PID decode. The rest of the block is not decoded. |
| `0x05` | Multi-chunk read at a device offset | Generic "read N bytes at offset." Used to read the keyboard's stored clock before a correction (see `docs/PROTOCOL.md`). Builder: `FUN_00484b10`. | Confirmed. |
| `0x06` | Multi-chunk write at a device offset | Generic "write N bytes at offset." Used for the clock write (see `docs/PROTOCOL.md`). Builder: `FUN_00484890`. | Confirmed. |
| `0x07` | Multi-chunk read | Reads a table with a 3-byte-per-entry layout. The capture's bytes for the first entries (`20 00 29`, `20 00 3a`, `20 00 3b` ...) match `Keyboard.json`'s `Device[].KeyList[].Assignment` field exactly (e.g. `Assignment: 2097193` = `0x200029`). Builder: `FUN_004835e0`. | Confirmed on the wire that this command exists and returns this table. The claim that it is the live key-assignment table (matched against `Keyboard.json`) is a strong hypothesis. |
| `0x09` | Write, gated on a caller flag equal to `0` | Unidentified. Callers: `FUN_004a06e0`, `FUN_004bc1d0` (the two are on RE_STATUS's list of unexplored "page dispatcher" functions). Builder: `FUN_00482e50`. | Hypothesis that it exists and is a write. Purpose unknown. |
| `0x0A` | Read, offset computed as `x * y` | Per-key RGB lighting profile load (RE_STATUS, traced to `FUN_004a4550`/`FUN_004ae870`/`FUN_004ae510`). Builder: `FUN_00483b30`. | Hypothesis (static analysis only, not seen in this capture). |
| `0x0B` | Write, offset computed as `x * y` | Per-key RGB lighting profile save. Same trace as `0x0A`. Builder: `FUN_00484340`. | Hypothesis. |
| `0x0C` | — | Listed with a "(?)" in the original static-analysis notes — existence as a real command builder was not confirmed even in that pass. | Unknown. |
| `0x12` | Write loop, uses the alternative transaction function `FUN_00482300` (shorter timeout than `FUN_004817f0`), gated by a flag on the device object | Unidentified. Only caller: `FUN_004a1ff0`. Builder: `FUN_004845e0`. | Hypothesis that it exists. Purpose unknown. Possibly firmware/AP-update related, given the distinct timeout path, but this is not confirmed. |
| `0x15` | Write loop, offset always starts at `0` | Unidentified. Callers: `FUN_004a0d10`, `FUN_004bcb40`. Builder: `FUN_00483110`. | Hypothesis that it exists. Purpose unknown. |
| `0x17` | Write loop, fixed 56-byte (`0x38`) chunks | Macro "burst-fire" (`ShootData_`) save-to-device, called only from `FUN_004bdc00` (confirmed by this session's decompile — matches RE_STATUS). Builder: `FUN_004833a0`. | Hypothesis (static analysis only). |
| `0x1A` | Single read, a 2-byte value from the caller placed in the offset field | Confirmed on the wire: request all-zero, response `64 01 00 00 00 00`. `0x64` (100) is consistent with a percentage or status value, but the field is not identified. Builder: `FUN_00484da0`. | Confirmed on the wire. Meaning of the response is a hypothesis. |
| `0x1B` | Multi-chunk read, item count divided by 3 in the read loop | Confirmed on the wire: response is a table of ascending index-like bytes (`00 01 02 03 04 05 ... 0c ff ff ff 10 11 ...`) grouped naturally into 3-byte entries. Possibly a key-position-to-LED-index map, but not confirmed. Builder: `FUN_00483880`. | Confirmed on the wire. Meaning of the table is a hypothesis. |
| `0x1C` | No payload, same shape as `0x01`/`0x02`/`0x23` | Unidentified. Only caller: `FUN_004cc920`. Not seen in this capture. Builder: `FUN_00482960`. | Hypothesis that it exists. Purpose unknown. |
| `0x21` | Write loop with a **24-bit (3-byte) device offset**, unlike every other write command's 16-bit offset. Reports upload progress through a custom window message (`0x4901`) and sleeps 500 ms at the start of a transfer. | The strongest candidate for the TFT screen frame push — see `docs/SCREEN.md`. Its only callers, `FUN_004b1220` and `FUN_004b15d0`, are the screen editor's "upload frame" and "upload all frames" functions: they resolve a `screen_view_tab` / `CScreenViewControlUI` control, convert pixel data to RGB565, and pass the resulting buffer straight into this command. Builder: `FUN_00483fd0`. | Hypothesis (static analysis only, not seen in this capture) but well supported — see `docs/SCREEN.md` for the full chain of evidence. |
| `0x23` | No payload, same shape as `0x01`/`0x02`/`0x1C` | Sent immediately before the `0x21` bulk write in the two functions `FUN_004b1220` and `FUN_004b15d0` — likely a "prepare for bulk write" signal, paired with `0x21`. Builder: `FUN_00483dc0`. | Hypothesis that it exists and pairs with `0x21`. Not seen in this capture. |
| `0xAA` | No payload | Ping / connection probe. Sent at the start of a session and then repeated roughly every 3 seconds for the life of the app (confirmed in the capture as a periodic heartbeat, not just a one-time probe). Builder: `FUN_00484f90`. | Confirmed. |

## Gaps

IDs never seen as a command builder in either the capture or the
static trace of `FUN_004817f0`/`FUN_00482300` callers: `0x04`, `0x08`,
`0x0D`-`0x11`, `0x13`-`0x14`, `0x16`, `0x18`-`0x19`, `0x1D`-`0x20`,
`0x22`. These can be unused, or built by a code path this
investigation did not get to (the approximately 13 "page dispatcher"
functions listed in `RE_STATUS.md` that were not individually
decompiled).
