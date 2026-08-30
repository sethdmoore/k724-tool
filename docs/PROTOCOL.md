# K724-RGB-PRO wire protocol — base framing and the clock command

This document is the authoritative reference for the confirmed part of
the protocol. Nearly every claim in this file is **confirmed**: a live
`usbmon` capture of the real Windows control app talking to real
hardware backs it. The few exceptions are marked **hypothesis**
explicitly, inline, where they occur. See `docs/COMMANDS.md` for the
full command-ID table, `docs/RGB_LIGHTING.md` for the separate
lighting-report path, and `docs/SCREEN.md` for the TFT screen format
and its open questions.

## Confidence key

Each of the four documents in this directory marks claims with one of
three labels:

- **Confirmed** — a live USB capture backs the claim directly.
- **Hypothesis** — static reverse engineering (Ghidra decompilation,
  `strings`, JSON/XML inspection) backs the claim, but no capture has
  verified it. Treat this as plausible, not proven.
- **Unknown** — neither method has produced an answer.

## Device identity (confirmed)

- Wired keyboard: USB VID/PID `320f:511b`.
- 2.4 GHz wireless receiver: USB VID/PID `320f:511c`.

The clock protocol was captured and verified through the wireless
receiver. On the receiver, the command interface is `interface=1`,
usage page `0xff1c` (device node `/dev/hidraw16` at capture time —
the exact node number changes per machine and per replug). The report
format itself is a property of the keyboard firmware, so it applies to
the wired connection too, but the wired interface number and usage
page have not been separately checked.

## Transport

The control app talks to the device with the Windows HID API. The
clock command channel is a raw interrupt OUT transfer written with
`WriteFile` (through wrapper function `FUN_004cec00`), not
`HidD_SetOutputReport`. `HidD_SetOutputReport` is used by a separate,
unrelated path (see `docs/RGB_LIGHTING.md`). On Linux, `hidapi`'s
`write()`/`read()` calls get to the same endpoint and reproduce this
channel correctly — confirmed working in `python/set_clock.py` against
real hardware.

## 64-byte report format (confirmed)

Every report on this channel, in the two directions, is 64 bytes:

| Byte | Field |
|---|---|
| 0 | `0x04`, fixed marker |
| 1-2 | 16-bit little-endian checksum |
| 3 | command ID |
| 4 | chunk length |
| 5-6 | 16-bit little-endian device buffer offset |
| 7 | `0x00`, reserved |
| 8... | chunk data, zero-padded to 64 bytes total |

### Checksum

```
checksum = sum(byte[3 : 8 + chunklen]) & 0xFFFF
```

Store the result little-endian at byte 1-2. This formula matched 41 of
42 request packets in the capture. The one exception traces to a
leftover-buffer artifact in the app itself, not a real protocol
requirement — do not treat it as a second checksum variant.

### Request/response behavior

The device replies to each request with a report whose byte 3 (command
ID) matches the request's byte 3. `python/set_clock.py` relies on this
match and works correctly against real hardware, so treat the
sequence-match rule as **confirmed**.

A retry policy (up to two retries, 1000-5000 ms timeout) appears in the
decompiled app code (`FUN_004817f0`, `FUN_00482300`) but the capture
does not show a retry happening. Treat the specific retry count and
timeout window as **hypothesis** — an app-side robustness detail, not
a proven device requirement. A single request with a 1000 ms timeout
was enough in practice.

## Session sequence around a write

The full captured session, in order, was:

1. `0xAA` ping, twice, about 3 seconds apart (an app startup retry, not
   necessary for a single transaction).
2. `0x01`.
3. An unrelated settings dump: `0x03` (device descriptor, 2 chunks),
   `0x07` (a 3-bytes-per-entry table, see `docs/COMMANDS.md`, 14
   chunks), `0x1B` (another table, 5 chunks).
4. `0x02` — commit/apply, closing the dump above.
5. `0x05` — the clock read described above (the "before" value).
6. `0x01` again.
7. The `0x06` clock write, sent as three chunks (see below).
8. `0x02` — commit/apply, closing the write.
9. `0xAA`, then `0x1A` (a status query, see `docs/COMMANDS.md`), then
   `0xAA` again every roughly 3 seconds for the rest of the capture.

The same pattern appears around the dump and around the write:
`0x01` → (one or more reads/writes) → `0x02`. `python/set_clock.py`
uses a simplified sequence — `0xAA` → `0x01` → `0x01` (sent twice in a
row, instead of once per cycle with a dump in between) → the three
`0x06` chunks → `0x02` — and drops the unrelated dump entirely. This
simplified form is confirmed working against real hardware, so
sending `0x01` twice back to back is not necessary to match the
capture exactly. The device accepts it too. The periodic `0xAA` after
step 9 is a keep-alive. A short-lived script can omit it.

## Command `0x03` at offset 0: the descriptor block (confirmed)

The wired open sequence sends `0x01`, then a bare 37-byte `0x03` read. The
reply is byte-identical across all five wired captures and carries, at fixed
offsets in the reply body: the keyboard's USB VID/PID (`320f:511b`,
little-endian, at body offset 20) and two `{tag, version_hi, version_lo}`
firmware records at body offset 24 — receiver `V0100` then keyboard `V0206`,
each a big-endian `uint16` shown as `V%04x` (the Windows app's own format
string). Full byte map and the version-parsing rationale are in
[`COMMANDS.md`](COMMANDS.md) → "Command `0x03`: the descriptor block".

This tool's protocol was only ever confirmed on **KB V0206 / AP V0100**;
`internal/protocol/descriptor.go` reads the versions and the client warns on a
mismatch.

## Command `0x06` at offset 0: the global settings block (confirmed)

**The "clock write" is one view of a 49-byte global settings block.**
Session 4's wired captures (`../captures/`) show the app re-sending this
same `0x06`-to-offset-0 write whenever *any* of several unrelated
settings change — lighting preset, brightness, effect speed, custom
colour, USB polling rate, screen frame count, screen frame interval —
always with the current wall-clock time stamped into bytes 35-41. It is
a settings blob, and the RTC is one field in it.

### Settings block layout (`buf[0:49]`)

Byte offsets confirmed by diffing the block across captures where one
setting changed at a time (`change_polling_*.pcapng` for byte 22,
`light_presets.pcapng` for bytes 1-3 and 6-8, `screen.pcapng` /
`gradient.pcapng` for bytes 34 and 43):

| Offset | Field | Evidence |
|---|---|---|
| 0 | `0x00` fixed | |
| 1 | Lighting effect / preset index | Stepped `0x01`→`0x13` as presets were cycled in `light_presets.pcapng`. |
| 2 | Brightness (0-5) | Changed `04`→`00`→`01`→`03`→`05` during the "colour individual keys" phase. Default `0x04`. |
| 3 | Effect speed (0-5) | Changed `02`→`01`→`00`→`05` in the same phase. Default `0x02`. |
| 4-5 | `0x00` fixed | |
| 6-8 | Custom colour, 24-bit **RGB** (R, G, B) | Set to `ff a8 00`, `ff 00 00`, `00 d7 0f`, `5e 01 d2`, `00 00 ff` … as the user picked key colours. |
| 9 | `0x06` fixed | |
| 10-12 | `0x00` fixed | |
| 13 | `0xB4` (= 180) fixed | Purpose unknown; constant in every capture. |
| 14-18 | `00 ff 00 ff 00` fixed | |
| 19 | "Exchange key": WASD ↔ arrow-cluster swap | `0`↔`1` was the only non-clock diff in `toggle_wasd_arrow_key_exchange.pcapng` (and the matching pair in `other_settings.pcapng`). |
| 20 | N-key rollover | `0`↔`1` in `toggle_n_key_off_then_on.pcapng`. Older wired captures (`open_redragon.pcapng`) hold `0xff` here — treat any non-zero as "on". |
| 21 | Windows / Super key lock | `0`↔`1` in `toggle_super_lock_on_then_off.pcapng`. |
| 22 | USB polling-rate index | `00`/`01`/`02`/`03` = 1000/500/250/125 Hz, stepped 1:1 in `change_polling_*.pcapng`. |
| 23-32 | `00 00 00 00 00 00 00 00 ff 00` fixed | |
| 33 | `0x02` fixed | |
| 34 | Screen animation frame count | `01` at startup, `02` for the 2-frame gradient upload, `03` for the 3-frame upload. |
| 35 | Second, BCD | Increments with wall-clock time across every capture. |
| 36 | Minute, BCD | |
| 37 | Hour, BCD (24-hour) | |
| 38 | Weekday, BCD | `0` = Sunday .. `6` = Saturday (`0x05` = Friday, 2026-08-28). |
| 39 | Day of month, BCD | |
| 40 | Month, BCD | |
| 41 | Year, 2-digit BCD | 2026 → `0x26` |
| 42 | `0x00` fixed | |
| 43-44 | Screen frame interval, ms — **16-bit little-endian** | `0x64 00` = 100, `0xC8 00` = 200, `0x50 0xC3` = 50000 (`write_light_*.pcapng`, and a live `0x05` read). Earlier notes read byte 43 alone; nothing had exceeded 255 ms until the 50000 ms capture. |
| 45-46 | `00 00` fixed | |
| 47 | `0x01` fixed | |
| 48 | `0x02` fixed (wired) / `0x00` (wireless session-3 capture) | |

### Wired vs wireless prefix — and the README's white-RGB bug

The session-3 wireless capture's block began
`00 05 03 02 00 01 cc cc cc 06 …`. Read against the table above that is
preset `0x05`, brightness `0x03`, speed `0x02`, and **colour
`0xCCCCCC`** (near-white grey) at bytes 6-8. Sending that
wireless-captured payload verbatim to the **wired** keyboard is
therefore an instruction to set every key to `0xCCCCCC` — which is
exactly the "all keys forced to solid white" symptom in the README's
"Known issue". The `cc cc cc` bytes are not opaque template padding;
they are the RGB colour field, and session 3 happened to capture them
mid-transfer or from a stale buffer. A wired clock-set that leaves
lighting alone must fill bytes 1-8 (and 22) with the device's current
values — read them back first with a `0x05` read of offset 0 — instead
of copying the session-3 constants.

## Command `0x06`: chunking of the settings-block write

The settings-block payload is 49 bytes. The session-3 wireless capture
sent it as three chunks:

| Chunk | Device offset | Length | Payload |
|---|---|---|---|
| 1 | 0 | 24 | `buf[0:24]` |
| 2 | 24 | 24 | `buf[24:48]` |
| 3 | 48 | 1 | `buf[48:49]` = `0x00` |

The three-chunk split is just an MTU detail. Session 4's wired captures
send the identical 49-byte payload as a single `0x06` chunk of length
49 at offset 0, and the device accepts it. See the settings-block table
above for the byte-by-byte layout; BCD encoding is
`byte = (value / 10) * 0x10 + (value % 10)`.

### What the session-3 capture proved

The capture recorded a real clock-drift correction end to end. A
command `0x05` read (generic "read N bytes at device offset") returned
the keyboard's stored time before the fix: `15:44:52`, 2026-08-13,
weekday `4` (Thursday). About 0.12 seconds later, the `0x06` write
above sent the corrected value from the PC: `18:24:51`, same date.
The keyboard's RTC had drifted about 2 hours 40 minutes behind real
time before the app corrected it.

This closed three open questions left by static analysis alone:

- The device write offsets are small, byte-granular values (`0`, `24`,
  `48`), not multiples of 64. An earlier hypothesis from static
  analysis (offset = `param_1 * 64`) was wrong.
- The weekday encoding is a plain `0` = Sunday .. `6` = Saturday BCD
  value, not the ambiguous ATL `CTime` "+1 then -1" encoding that the
  decompiled source suggested.
- What session 3 recorded as a fixed "unknown template" around the time
  fields is really the live values of unrelated settings (lighting,
  polling rate, screen). Session 4 identified them — see the settings-
  block table above. Copying the session-3 constants verbatim is what
  makes a wired write dangerous.

## Known-good client — wireless only

`python/set_clock.py` and `cmd/setclock` (both `hidapi`-based)
implement this protocol and are confirmed working **against the 2.4
GHz wireless receiver**: the device echoed every write correctly, and
the `0xAA` ping successfully auto-selected the correct HID interface
out of several candidates for the same VID/PID.

**The wired keyboard is a different story.** Sending this exact,
wireless-captured payload to the wired connection forced every key's
RGB to solid white, recoverable only by a full power cycle — not by
the onboard controls. Both clients default to the wireless receiver
and require an explicit opt-in flag (with a warning) to target the
wired keyboard. Session 4's wired capture explains why (see "the
README's white-RGB bug" above): the payload's bytes 1-8 are live
lighting settings, and the session-3 constants set the custom colour to
`0xCCCCCC`. A safe wired client must `0x05`-read offset 0 first and
preserve bytes 1-8 and 22. The clients do not do this yet — the
`--wired` warning stands. See the README's "Known issue" section.
