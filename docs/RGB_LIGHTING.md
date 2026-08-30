# RGB lighting

This document covers two separate things: the per-key lighting HID
report that pushes live color data to the keyboard, and the list of
named lighting effect modes the app exposes in its UI.

The **command `0x0b`** section immediately below is **confirmed** by a
live capture (`write_light_a-r_s-g_d-b_q-w_e-bk.pcapng`). Everything
after it — the 17-byte `0xFF` `HidD_SetOutputReport` path and the mode
ID table — is still a **hypothesis** from static analysis (Ghidra
decompilation of `FUN_004950a0`, plus JSON/XML inspection), unless
marked otherwise. See `docs/PROTOCOL.md` for the confidence key.

## Per-key custom colours: command `0x0b` (confirmed)

`write_light_a-r_s-g_d-b_q-w_e-bk.pcapng` captures the Windows app's
"Custom" effect being edited on the **wired** keyboard (`320f:511b`),
one key at a time: A→red, S→green, D→blue, Q→white, E→black. It is all
on the ordinary `0x04`-marker command channel — **not** the 17-byte
`0xFF` `HidD_SetOutputReport` path below, which no capture has ever
shown in use.

Each "apply" is two phases:

1. `0x01` → seven `0x0b` chunks → `0x02`. The chunks carry a **384-byte
   table = 128 entries × 3 bytes (R, G, B)**, one entry per key/LED, at
   climbing device offsets `0x00 / 0x38 / 0x70 / 0xa8 / 0xe0 / 0x118 /
   0x150` (56 bytes each, last chunk 48). Same chunking as the `0x09`
   key-remap table.
2. `0x01` → a normal `0x06` settings-block write → `0x02`, with byte 1
   (effect) set to `0x13` = 19 = "Custom" and bytes 6-8 (global colour)
   set to the last colour the user picked.

**Entry order = the `0x09` key-remap table's order.** Every `0x09` entry
in `button_write_j_default_key.pcapng` is `20 00 <hid-usage>`
(`0x29` Esc, `0x3a`-`0x45` F1-F12, `0x14` Q, `0x04` A, …), so that
table names all 128 slots. The five keys the colour capture touched
landed on exactly the slots that map predicts: Q=33, E=35, A=49, S=50,
D=51. `internal/protocol/keymap.go` encodes the resulting layout for a
75 % ANSI board; slots 88/89/111/127 and the volume knob at 13 carry
`0xa0…` special records rather than plain keys.

**Per-key brightness is folded into the RGB.** The `0x0b` entry is only
3 bytes — there is no per-key alpha byte on the wire — but the saved
profile (`profile1.json`) keeps a 4th value per key:
`CustomLightMode.LightColorInfo[*].Alpha`, which takes intermediate
values (that profile has a mix of `255` and `201`), not just 0/255. So
the Windows app stores a per-key brightness and flattens it before
sending: `wire_channel ≈ round(channel * alpha / 255)`. The exact
rounding is unconfirmed — every `0x0b` byte in the capture is `0x00` or
`0xff` because the edit session never used a partial brightness — but
this is the only place the value can go. (This is a *client-side* scale,
unrelated to the `0xf7` clamp in `FUN_004950a0`, which is on the
separate 17-byte channel below.) The `LightColorInfo` array has 170
entries to the wire table's 128; the extra 42 are unpopulated LED slots
(see the `0xAA`/170 note under "What was not resolved").

There is no observed read-back for the `0x0b` table, so a client must
keep its own authoritative copy. `internal/protocol/customkeys.go`
(`KeyColorTable`, `KeyColorWriteSteps`) and `k724.Device.ApplyKeyColors`
implement both phases; the GUI keeps an RGBA model (RGB + per-key
brightness), derives the wire table by the scale above, and persists the
model to `fyne.Preferences`.

## The per-key lighting report is a separate channel

The clock command channel (`docs/PROTOCOL.md`) uses a `0x04`-marker
64-byte report written with `WriteFile`. Per-key RGB lighting uses a
completely different transport: a 17-byte (`0x11`) report sent with
`HidD_SetOutputReport`, built entirely inside one function,
`FUN_004950a0`. This function has exactly 12 `HidD_SetOutputReport`
call sites (decompiled and counted directly in this investigation,
matching the count in `RE_STATUS.md`), and no other function in the
binary calls `HidD_SetOutputReport`. Its only caller is an indirect
vtable call, consistent with a `SendLighting`-style virtual method
on the device class.

There is no checksum field on this channel. Do not apply the
`docs/PROTOCOL.md` checksum formula here.

### Report format

Each of the 12 reports is 17 bytes:

| Byte | Field |
|---|---|
| 0 | `0xFF`, fixed marker |
| 1 | Low command byte |
| 2 | High command byte |
| 3 | `0x00`, unused in every observed send |
| 4-10 (7 bytes) or 4-9 (6 bytes) | Color data |

### The 12 sends

`FUN_004950a0` first builds three 13-byte color arrays from a
per-LED loop (source data read 3 bytes at a time — the code's own
field order implies R, then G, then B — and each value is scaled by
a per-LED multiplier and clamped to a maximum of `0xf7`, not `0xff`).
Each 13-byte array is split into a 7-byte chunk and a 6-byte chunk
(the report can only carry 7 payload bytes at most), and each split
pair is sent twice, with two different low-command-byte tags:

| Send | Command bytes | Data |
|---|---|---|
| 1 | `0x22, 0xAA` | R-array bytes 0-6 |
| 2 | `0x22, 0xAD` | R-array bytes 7-12 |
| 3 | `0x44, 0xAA` | R-array bytes 0-6 (repeated) |
| 4 | `0x44, 0xAD` | R-array bytes 7-12 (repeated) |
| 5 | `0x22, 0xAB` | G-array bytes 0-6 |
| 6 | `0x22, 0xAE` | G-array bytes 7-12 |
| 7 | `0x44, 0xAB` | G-array bytes 0-6 (repeated) |
| 8 | `0x44, 0xAE` | G-array bytes 7-12 (repeated) |
| 9 | `0x22, 0xAC` | B-array bytes 0-6 |
| 10 | `0x22, 0xAF` | B-array bytes 7-12 |
| 11 | `0x44, 0xAC` | B-array bytes 0-6 (repeated) |
| 12 | `0x44, 0xAF` | B-array bytes 7-12 (repeated) |

The repeated sends (`0x22` then `0x44` for the same data) are
unexplained. A plausible guess is that they address two different
physical zones or two halves of a longer LED chain with identical
color data, but this investigation did not trace it more, and it
stays **unknown**.

The per-LED loop that fills the color arrays has an explicit skip for
LED index `0xAA` (170 decimal): `if ((char)local_54 != -0x56) { ... }`.
This is worth noting because `Keyboard.json`'s
`Device[].CustomLightMode.LightColorInfo[0]` array also has exactly
170 entries (see below). Whether these two facts are directly related
(one combined 170-LED addressable space) or a coincidence was not
resolved here — treat it as an open question, not a conclusion.

## `Keyboard.json` lighting fields

`Device[0].LightInfo` holds the active lighting configuration:

```
"LightInfo": {
  "Blue": 255, "Green": 255, "Red": 255,
  "Light": 5,
  "MultiColor": true,
  "SelectItem": 10,
  "Speed": 2,
  "ZZCCLedMode": 0,
  "OldLedmode": 1
}
```

`SelectItem` is the active effect mode's numeric ID. `Speed` and
`Light` are almost certainly speed and brightness sliders. The two
fields are small integers in a UI-scale range, not raw device values.
None of these field names or ranges are confirmed against wire
traffic.

`Device[0].CustomLightMode.LightColorInfo` holds a 170-entry array of
per-LED `{Alpha, Red, Green, Blue}` colors, used for the app's
"Custom" effect mode (mode ID `19`, see below).

## Lighting effect mode names and IDs

`DefaultData/default_light.json` maps a numeric `value` to a string
ID for each effect the UI can show. The string IDs are resolved to
display text in `Skin/lan_en.xml`. The first list in that file (31
entries, IDs `0`-`30`) is the fullest one and is almost certainly the
one that applies to this keyboard's full per-key RGB matrix. The
document also contains three shorter lists (used for other, simpler
device types in the same product family) that are not reproduced
here.

| ID | String ID | Display name |
|---|---|---|
| 0 | `light_mode_wave_text` | Corrugated (listed `visible: false` here — a duplicate entry at ID 10 is visible) |
| 1 | `light_mode_static_text` | Normal |
| 2 | `light_mode_breath_text` | Breath |
| 3 | `light_mode_mix_color_text` | Spectrum |
| 4 | `light_mode_traverse_text` | Traverse |
| 5 | `light_mode_rain_text` | Rain |
| 6 | `light_mode_stone_text` | Ripples |
| 7 | `light_mode_stars_text` | Stars |
| 8 | `light_mode_reaction_text` | Reaction |
| 9 | `light_mode_flow_text` | Stream |
| 10 | `light_mode_wave_text` | Corrugated |
| 11 | `light_mode_cartoon_text` | Cartoon |
| 12 | `light_mode_wave_bar_text` | Wave |
| 13 | `light_mode_vortex_text` | Serpentine |
| 14 | `light_mode_roll_text` | Roll |
| 15 | `light_mode_firework_open_text` | Flowers |
| 16 | `light_mode_scan_text` | Scan |
| 17 | `light_mode_zzcc_text` | Surmount |
| 18 | `light_mode_speed_text` | Speed |
| 19 | `light_mode_custom_text` | Custom |
| 20 | `light_mode_off_text` | Off |
| 21 | `light_mode_audiorecord_text` | Audio wave |
| 22 | `light_mode_capturecolor_text` | Light And Shadow |
| 23 | `light_mode_4color_blink_text` | Four-color color flashing (`visible: false`) |

IDs `24`-`30` use the same string IDs as earlier entries, marked
`visible: false`, and are omitted here as likely disabled or legacy
entries.

Additional effect names appear in `Skin/lan_en.xml` with no numeric ID
attached in `default_light.json`: `light_mode_pulse_text` (Pulse),
`light_mode_blink_text` (Blink), `light_mode_radar_text` (Radar),
`light_mode_snake_text` (Snake), `light_mode_cloud_text` (Cloud),
`light_mode_emanate_text` (Emanate), `light_mode_4color_breath_text`
(Four-color Respiration). These are confirmed to exist as UI strings
but their numeric IDs, if any apply to this keyboard, are unknown.

**None of these mode IDs are confirmed to be the byte value the wire
protocol expects.** They are UI combo-box index values from
`default_light.json`, one layer removed from the actual device
command. Treat the ID column as a hypothesis for what `SelectItem` in
`Keyboard.json` likely holds, not as a proven wire-format value.

## What was not resolved

- How a mode selection in `Keyboard.json`/`LightInfo` turns into wire
  bytes. `FUN_004950a0` only sends live per-LED color arrays. It does
  not appear to special-case the module's fixed animation effects
  (breath, wave, and so on). Effect selection is likely a separate
  command on the `0x04`-marker channel (perhaps one of the
  unidentified IDs in `docs/COMMANDS.md`), computed device-side from a
  mode number, but no candidate command was traced to a `LightInfo`
  read in this investigation.
- The meaning of the repeated `0x22`/`0x44` send pairs.
- Whether the 170-LED loop bound and `CustomLightMode`'s 170-entry
  array are the same address space.

A live capture of a lighting-mode change (not just `python/set_clock.py`'s
clock write) would resolve all three questions directly, the same way
the session 3 capture resolved the clock protocol's open questions.
