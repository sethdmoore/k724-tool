# TFT screen and the GIF/frame upload path

The K724-RGB-PRO has a small TFT screen that shows a static image or a
short animation. This document covers the on-disk frame format and the
state of the investigation into how a frame gets to the device through
USB.

Nothing in this file is **confirmed** by a live USB capture. Every
claim below is a **hypothesis** from static analysis, unless marked
otherwise. See `docs/PROTOCOL.md` for the confidence key.

## `ScreenFrame.json` structure

```
{
  "Frame": [
    {
      "BkGroundColor": { "Red": 0, "Green": 0, "Blue": 0 },
      "PenColor": { "Red": 255, "Green": 38, "Blue": 42 },
      "Pixel": [
        { "Red": 0, "Green": 0, "Blue": 0, "Flag": 0 },
        ...
      ]
    }
  ]
}
```

- `Frame` is an array. The shipped default file has exactly one
  frame, a blank black 180x180 image.
- Each frame has 32,400 `Pixel` entries — confirmed to be `180 x 180`
  by direct count (`sqrt(32400) = 180`), in what is assumed to be
  row-major order (not directly confirmed).
- `BkGroundColor` and `PenColor` are per-frame metadata, not part of
  the pixel grid. `PenColor` matches the "Pen:" tool color shown in
  the screen editor UI (`Skin/lan_en.xml`, `pen_color_text`). It is
  the drawing tool's current color, not a device setting.
- Each `Pixel` entry's `Flag` field is `0` in every entry of the
  shipped default file, so its purpose could not be inferred from this
  data alone. It stays **unknown**.

`Keyboard.json`'s `Device[0].FrameIntervalTime` (`100` in the shipped
default) is almost certainly the per-frame delay in milliseconds for
animation playback, based on the field name, but this is not
confirmed against device behavior.

## Screen editor UI (from `Skin/lan_en.xml` and `Skin/screen.xml`)

The editor supports:

- A pen tool and an eraser tool.
- Frame operations: add, delete, clear, and reverse, each for a
  single frame or for all frames.
- Import of a picture or a GIF animation.
- Export of the current frame, or the whole animation, back to a
  picture or a GIF.
- Upload to the device.

Two UI strings distinguish a single-frame upload from a
full-animation upload: `uploading_frame_text` ("uploading frame") and
`uploading_all_frame_text` ("uploading all frame"). Another string
(`save_over_pictrue_count`) states a hard limit: "Saves a maximum of
25 frames of images." A third (`upload_erro_text`) states "Only the
wired mode upload is supported!" The screen upload path is gated to
the wired connection and is not expected to work through the 2.4 GHz
receiver.

## What earlier static analysis ruled out

`RE_STATUS.md` names five functions as the screen editor's image
import/export routines: `FUN_004afcc0`, `FUN_004b3940`, `FUN_004b4030`,
`FUN_004b4930`, `FUN_004b5590`. This investigation re-decompiled all
five and searched their code specifically for calls to the two
command-transaction functions (`FUN_004817f0`, `FUN_00482300`) and to
`WriteFile`/`HidD_SetOutputReport` directly. None of the five call any
of these. `FUN_004afcc0` uses OpenCV (`cv::imread`, `cv::transpose`,
`cv::resize`, `cv::flip`) to load a picture file from disk.
`FUN_004b4930` writes local files in a `ScreenFames` (sic)
directory. **None of the five talk to the device.** This confirms and
strengthens RE_STATUS's original conclusion, rather than leaving it as
an open guess.

The per-key RGB lighting sender (`FUN_004950a0`, see
`docs/RGB_LIGHTING.md`) was also independently confirmed, again, to be
unrelated: it sends fixed 17-byte `0xFF`-marker reports with no image
data shape, nothing like a pixel buffer.

## The strongest lead found in this investigation: `FUN_004b1220` / `FUN_004b15d0`

Searching for callers of command `0x21` — the one write command whose
device offset field is 3 bytes (24-bit) instead of the usual 2 bytes
(16-bit), see `docs/COMMANDS.md` — turned up exactly two functions:
`FUN_004b1220` and `FUN_004b15d0`. The two functions sit in the same
address range as the five screen functions above (between
`FUN_004afcc0` at `0x4afcc0` and `FUN_004b3940` at `0x4b3940`), which
was not true of any other unidentified command's callers.

Decompiling the two functions directly shows a strong, consistent
picture:

- The two functions look up a UI control literally named
  `screen_view_tab` and confirm its runtime type is
  `DuiLib::CScreenViewControlUI` before doing anything else.
- The two functions compute a buffer size as `width * height * 2`
  (bytes), where `width`/`height` are read from object fields. Two
  bytes per pixel is consistent with a 16-bit color format, not the
  3-bytes-per-pixel (`Red`/`Green`/`Blue`) format `ScreenFrame.json`
  uses on disk.
- The two functions run a per-pixel conversion loop that is, byte for
  byte, a standard RGB565 pack: it takes 3 source bytes, keeps the top
  5 bits of the first, the top 6 bits of the second, and the top 5
  bits of the third, and packs them into 2 destination bytes as
  `RRRRRGGG GGGBBBBB` (5-6-5 bits, big-endian byte order). This is a
  common wire format for small SPI TFT controllers.
- The two functions call `FUN_00483fd0` — the builder for command
  `0x21` — passing the freshly built RGB565 buffer and its byte length
  directly as the payload, immediately followed by command `0x02`
  (commit, see `docs/COMMANDS.md`).
- The two functions post a custom window message (`0x4901`) as an
  upload-progress callback — the same message ID that `FUN_00483fd0`
  itself posts internally during a chunked send, tying the two
  together.
- The two functions call `FUN_00482530` (command `0x01`) and
  `FUN_00483dc0` (command `0x23`) immediately before the `0x21` bulk
  write, matching the general "prepare, then bulk-write, then commit"
  shape seen elsewhere in the protocol.
- `FUN_004b15d0` also loops through a frame count field, building
  and concatenating one RGB565 buffer per frame before the single
  `0x21` write — matching the "upload all frame" UI string, while
  `FUN_004b1220`'s single-buffer version matches "uploading frame".
- After the upload, `FUN_004b1220` calls `FUN_004b4930` and
  `FUN_004b15d0` calls `FUN_004b5590` — two of the five originally
  named "screen editor" functions. Given `FUN_004b4930` writes local
  cache files (see above), this looks like post-upload local
  bookkeeping, not a second device call.

**This is a well-supported hypothesis, not a confirmed finding.** No
live capture has observed a screen/GIF upload. But it is a specific,
falsifiable claim: a live capture of the app's "Upload" button in the
View Screen tab would show command `0x21` writes whose payload, when
unpacked as big-endian RGB565, reproduces the uploaded image.

## Remaining open questions

- The exact device screen resolution. `180 x 180` is inferred only
  from `ScreenFrame.json`'s pixel count, not read from the binary's
  `width`/`height` fields directly.
- Whether `ScreenFrame.json`'s on-disk `Pixel[].Flag` field carries
  any wire-visible meaning, or is purely an editor-side bookkeeping
  bit (for example, "pixel touched by the pen tool").
- The source bitmap's layout that `FUN_004b1220`/`FUN_004b15d0` read
  from (row stride `0x780` = 1920 bytes, column stride 8 bytes, base
  offset `0x3df2c`) was not traced back to a named structure. It is
  presumably a larger, live-rendered editor canvas being downsampled
  to the device's actual resolution, not `ScreenFrame.json` read
  directly, but this was not confirmed.
- Command `0x23`'s exact role (paired with `0x21` in the two
  functions, but never decompiled in isolation beyond "no payload").
- Whether commands `0x09`, `0x12`, or `0x15` (see `docs/COMMANDS.md`)
  play any role in the screen path. They were checked and ruled out
  as direct callers of the screen functions, but their own callers
  were not fully traced.

A live capture of a screen upload — the same technique that resolved
the clock protocol in session 3 — is the clear next step to move this
from hypothesis to confirmed.
