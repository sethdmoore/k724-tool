# TFT screen and the GIF/frame upload path

The K724-RGB-PRO has a small TFT screen that shows a static image or a
short animation. This document covers the on-disk frame format and the
wire format of a frame upload.

**The upload path is now confirmed by a live `usbmon` capture.** Session
4 captured the Windows app uploading images over the **wired** keyboard
(`320f:511b`) and the decoded pixel payload matches the source bitmaps
byte for byte. The static-analysis hypothesis from session 3 (command
`0x21`, RGB565, one buffer per frame) was correct. Claims below are
marked **confirmed** where the capture backs them and **hypothesis**
where only static analysis does. See `docs/PROTOCOL.md` for the
confidence key.

Capture files: `../captures/screen.pcapng` (three-frame R/G/B bucket
fill, uploaded twice at 100 ms and 200 ms frame delay) and
`../captures/gradient.pcapng` (two 45-degree grayscale gradient frames,
`grad0.bmp` / `grad1.bmp`, 200 ms delay). Decoder:
`../../k724-tool/python/parse_screen_capture.py`.

## Screen resolution (confirmed)

**240 x 135**, 32,400 pixels. Confirmed three ways: every frame's pixel
payload is exactly `32400 * 2 = 64800` bytes; the decoded gradient
frames render the right way up with all four corners correct; and a
byte-for-byte compare of the decoded gradient frames against
`grad0.bmp` / `grad1.bmp` is a 100.0% match (64800/64800 bytes).

Note the earlier `180 x 180` guess from `ScreenFrame.json`'s pixel
count was wrong but *numerically coincidental*: `180 * 180` and
`240 * 135` are both 32,400.

## Pixel format (confirmed)

- **Big-endian RGB565**, one 16-bit word per pixel, packed
  `RRRRRGGG GGGBBBBB` with the high byte first on the wire.
  - Pure red `255,0,0` → `0xF800` → bytes `F8 00`.
  - Pure green `0,255,0` → `0x07E0` → bytes `07 E0`.
  - Pure blue `0,0,255` → `0x001F` → bytes `00 1F`.
- **Row-major, top-left origin.** Pixel `(x, y)` is at byte
  `(y * 240 + x) * 2` within the frame. (Source BMPs are bottom-up;
  the app flips them before upload.)
- No per-frame header. The pixel data starts at byte 0 of the frame
  slot.

`parse_screen_capture.py --width 240 --height 135` unpacks each frame
back to a PNG with this format.

## Frame slot layout in device memory (confirmed)

Command `0x21` carries a 24-bit device offset (see below). Frames are
written into a flat device address space at a fixed stride:

| Frame index | Device offset | Payload |
|---|---|---|
| 0 | `0x00000` | 64800 bytes RGB565, then padding to `0x10000` |
| 1 | `0x10000` | same |
| 2 | `0x20000` | same |
| ... | `k * 0x10000` | ... |

- **Stride is `0x10000` (65536) bytes per frame.** A 3-frame upload
  writes offsets `0` through `0x2FFD0 + 48 = 0x30000` in one
  unbroken run of `0x21` chunks; a 2-frame upload writes through
  `0x20000`. There is **no protocol delimiter between frames** — the
  frame boundary is purely the `0x10000` offset stride.
- Bytes `64800 .. 65535` of each slot are **padding**. When the frame
  was drawn in the editor (the R/G/B bucket-fill capture) the padding
  is all zero. When the frame came from an imported image file (the
  gradient capture) the padding holds ~726 bytes of non-zero data that
  is **byte-identical between the two frames of that upload** and looks
  like uninitialised application heap (repeating little-endian
  pointer/float-shaped words, e.g. `b8 bf 26 11`). Treat it as stale
  scratch memory, not protocol data — a client should zero-fill it.
  This is the same class of "leftover-buffer artifact" noted for the
  one anomalous checksum in `docs/PROTOCOL.md`.

## Upload wire sequence (confirmed)

For one image or animation upload, in order:

1. `0x01` — begin.
2. `0x06` write, 49-byte payload at device offset 0 — the
   **screen-config block** (see below).
3. `0x02` — commit the config block.
4. `0x23` — no payload. "Begin bulk image transfer" marker. This is
   the delimiter a decoder uses to find the start of a pixel stream.
5. Many `0x21` chunks. Device offset starts at 0 and increases by the
   chunk length each packet. Chunk length is `0x38` (56) for every
   chunk except the last of each `0x10000` slot, which is `0x30` (48)
   — `64800 = 1157 * 56 + 8`, so slots do not divide evenly by 56 and
   the app just sends a short final chunk. The offset runs
   continuously across all frames (it does **not** reset to 0 per
   frame).
6. `0x02` — commit the upload.

Every `0x21` request gets a response whose byte 3 is `0x21` and which
echoes the same offset (confirmed in the capture). The standard
checksum formula from `docs/PROTOCOL.md` holds for `0x21` with no
exceptions (0 mismatches across 7022 `0x21` packets in `screen.pcapng`).

### Command `0x21` framing (confirmed)

`0x21` is the only write command with a **24-bit (3-byte)** device
offset. It reuses the 64-byte report frame from `docs/PROTOCOL.md` but
byte 7 is the offset's high byte, not a reserved zero:

| Byte | Field |
|---|---|
| 0 | `0x04` marker |
| 1-2 | 16-bit LE checksum, `sum(byte[3 : 8+chunklen]) & 0xFFFF` |
| 3 | `0x21` |
| 4 | chunk length (`0x38`, or `0x30` for a slot's final chunk) |
| 5-7 | **24-bit little-endian device offset** |
| 8... | chunk data, zero-padded to 64 bytes total |

Example high-offset request from `screen.pcapng`:
`04 30 02 21 38 d8 fd 02 ...` → checksum `0x0230`, cmd `0x21`, len
`0x38`, offset `0x02fdd8` = 196056.

## The `0x06` screen-config block (confirmed fields, partial)

A 49-byte `0x06` write to device offset 0, sent just before the `0x23`
+ `0x21` burst (and also once at app startup, pushing whatever screen
state is currently loaded). Observed payloads, variable bytes in
**bold**:

```
00 13 05 00 00 00 00 00 ff 06 00 00 00 b4 00 ff 00 ff 00 00 ff 00 [PR]
00 00 00 00 00 00 00 00 00 ff 00 02 [FC] [SS MM HH WD DD MM YY] 00 [INT]
00 00 00 01 02
```

`[PR]` = byte 22, USB polling-rate index (left at whatever the polling tab
last set). `[FC]` = byte 34, frame count. `[INT]` = bytes 43-44, frame
interval (16-bit LE — see below).

| Payload offset | Field | Evidence |
|---|---|---|
| 22 | USB polling-rate index — **not** frame count | Re-checked against `screen.pcapng`: byte 22 is `0x03` in every screen write, matching the 125 Hz polling rate left set by the earlier `change_polling` capture, and it does **not** track the frame count. `docs/PROTOCOL.md` is right; the earlier "frame count at 22 and 34" note was wrong. |
| 34 | **Frame count** | `0x01` at startup, `0x03` for the 3-frame animation, `0x02` for the 2-frame gradient. This is the only frame-count byte. |
| 35 | Second, BCD | Varies with wall-clock time across the captures. |
| 36 | Minute, BCD | Same 7-field `SS MM HH WD DD MM YY` BCD block as the clock write in `docs/PROTOCOL.md`. |
| 37 | Hour, BCD (24 h) | Capture times 13:53 / 13:54 / 14:07 / 14:13 line up with the file mtimes. |
| 38 | Weekday, BCD | `0x05` = Friday (2026-08-28), matches `0`=Sun..`6`=Sat. |
| 39 | Day, BCD | `0x28` = 28. |
| 40 | Month, BCD | `0x08` = August. |
| 41 | Year, BCD | `0x26` = 2026. |
| 43-44 | **Frame interval, ms — 16-bit little-endian** | `0x64 00` = 100 and `0xC8 00` = 200 in the early captures, which read as a single byte. `write_light_a-r_s-g_d-b_q-w_e-bk.pcapng` carries `50 c3` = `0xC350` = 50000 here, matching the Windows app's "Interval time 50000 ms" field, and a live `0x05` read of the block on KB V0206 confirmed `50 c3` at 43-44. So the field is two bytes; the earlier single-byte reading only held because nothing had exceeded 255 ms. This is `Keyboard.json`'s `FrameIntervalTime` on the wire. |
| all others | Fixed template | Identical across all six of the original screen captures. |

**This overlaps the "clock write" from `docs/PROTOCOL.md`.** Both are
`0x06` writes to offset 0, both 49 bytes, both carry the same 7-field
BCD timestamp at the same offsets 35-41, and both end
`... 00 [INT] 00 00 00 01 xx`. The wireless "clock" capture from
session 3 and this wired "screen-config" capture look like **the same
config block** with the timestamp as one field among frame-count and
frame-interval. The RTC still updates from the timestamp field (session
3 saw the keyboard's clock visibly correct), but "command `0x06` = set
clock" is too narrow — it is a combined screen/clock settings write.
The fixed-template bytes differ between the wired and wireless captures
(`00 13 05 ...` vs `00 05 03 02 00 01 cc cc cc ...`), which is the most
likely cause of the wired clock-set misfire documented in the README —
see the note there.

## Animation limits

The screen editor's `save_over_pictrue_count` string says "Saves a
maximum of 25 frames of images." Uploads of 2 and 3 frames were
captured. Re-uploading the same frames with only the interval changed
produces a byte-identical pixel stream, so nothing in the `0x21` data
depends on frame timing — that lives entirely in the `0x06` block.

## Wired vs wireless

The capture is over the **wired** keyboard (`320f:511b`), matching the
editor's `upload_erro_text` string, "Only the wired mode upload is
supported!" The screen path is not expected to work through the
`320f:511c` receiver, and no attempt to send it there has been made.

## Still open

- The two `0x06` template bytes that differ wired vs wireless
  (`docs/PROTOCOL.md` prefix `00 05 03 02 00 01 cc cc cc` vs this
  `00 13 05 00 00 00 00 00 ff`) are not decoded. Resolving them is the
  path to a safe wired clock-set (README "known issue").
- `0x0A` / `0x0B` fire at app startup in both captures with a
  3-bytes-per-entry body (`00 00 ff` repeating, ~342 entries) — this
  is the per-key RGB restore, not screen data, but the exact body
  format is not written up in `docs/RGB_LIGHTING.md` yet.
- Whether an upload of >3 frames changes the slot stride or the
  `0x06` block shape (only 2- and 3-frame uploads captured).
- The `0x03` / `0x07` / `0x1B` reads at the very start of every
  session return all-zero request bodies here (they are reads); their
  response bodies were not re-examined in this session.

## Reproduce

```
cd k724-tool/python
python3 parse_screen_capture.py ../captures/screen.pcapng   --width 240 --height 135 --out ./decoded
python3 parse_screen_capture.py ../captures/gradient.pcapng --width 240 --height 135 --out ./decoded
```

`decoded/burstNN.png` is each uploaded frame. The collapsed
"unique frames" list printed to stdout is the de-duplicated command
sequence.
