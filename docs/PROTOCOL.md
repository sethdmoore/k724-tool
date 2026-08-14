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

## Command `0x06`: clock write (confirmed)

The clock write payload is 49 bytes, sent in a single burst of three
chunks:

| Chunk | Device offset | Length | Payload |
|---|---|---|---|
| 1 | 0 | 24 | `buf[0:24]` |
| 2 | 24 | 24 | `buf[24:48]` |
| 3 | 48 | 1 | `buf[48:49]` = `0x00` |

### Payload layout (`buf[0:49]`)

| Offset | Value | Meaning |
|---|---|---|
| 0-34 | `00 05 03 02 00 01 cc cc cc 06 00 00 00 b4 00 ff 00 ff 00 00 ff 00 00 00 00 00 00 00 00 00 00 00 ff 00 00 0a` | Fixed. Device- and session-independent. Copy verbatim. |
| 35 | Second, BCD | e.g. `0x51` = 51 |
| 36 | Minute, BCD | |
| 37 | Hour, BCD (24-hour) | |
| 38 | Weekday, BCD | `0` = Sunday .. `6` = Saturday (confirmed: Thursday → `4`) |
| 39 | Day of month, BCD | |
| 40 | Month, BCD | |
| 41 | Year, 2-digit BCD | 2026 → `0x26` |
| 42-48 | `00 64 00 00 00 01 00` | Fixed. Unchanged between the read and the write in the capture. Copy verbatim. |

BCD encoding: `byte = (value / 10) * 0x10 + (value % 10)`.

### What the capture proved

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
- The 35-byte and 7-byte "unknown template" regions turned out to be
  fixed, reproducible values, not device-specific state — copy them
  verbatim.
- The weekday encoding is a plain `0` = Sunday .. `6` = Saturday BCD
  value, not the ambiguous ATL `CTime` "+1 then -1" encoding that the
  decompiled source suggested.

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
wired keyboard. Treat the wired connection as unverified until a live
capture of the real Windows app talking to the keyboard over a wired
connection confirms whether this payload needs to differ. See the
README's "Known issue" section.
