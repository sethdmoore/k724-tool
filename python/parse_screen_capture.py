#!/usr/bin/env python3
"""Decode a usbmon capture of a K724-RGB-PRO TFT-screen / GIF upload.

Stdlib only -- no dpkt / scapy / tshark, same constraint as
re_notes/parse_usbmon.py, which this extends.

What it does:
  1. Parses a pcapng usbmon capture (LINKTYPE_USB_LINUX_MMAPPED).
  2. Pulls every report on the 0x04-marker command channel.
  3. Collapses runs of byte-identical reports, so the 0xAA/0x1A
     heartbeat spam becomes one line each and only the *unique frames*
     of the session are left to read.
  4. Reassembles the chunked bulk-write command (default 0x21, the
     screen-upload candidate) into one buffer per burst, by device
     offset, and writes each burst to <out>/burstNN.bin.
  5. With --width/--height, unpacks each burst as big-endian RGB565
     and writes <out>/burstNN.png so you can eyeball whether it is the
     image you uploaded.

Use:
  python3 parse_screen_capture.py screen.pcapng
  python3 parse_screen_capture.py screen.pcapng --cmd 0x21 --out ./decoded
  python3 parse_screen_capture.py screen.pcapng --width 180 --height 180
"""
import argparse
import os
import struct
import sys
import zlib


# ---------------------------------------------------------------------------
# pcapng / usbmon parsing (lifted from re_notes/parse_usbmon.py)
# ---------------------------------------------------------------------------
def parse_pcapng(path):
    with open(path, "rb") as f:
        data = f.read()
    off = 0
    byteorder = "<"
    packets = []
    n = len(data)
    while off + 8 <= n:
        block_type, block_len = struct.unpack(byteorder + "II", data[off:off + 8])
        if block_len < 12 or off + block_len > n:
            break
        body = data[off + 8:off + block_len - 4]
        if block_type == 0x0A0D0D0A:
            bom, = struct.unpack("<I", body[0:4])
            byteorder = "<" if bom == 0x1A2B3C4D else ">"
        elif block_type == 0x00000006:  # Enhanced Packet Block
            _iface, ts_high, ts_low, cap_len, _orig = struct.unpack(
                byteorder + "IIIII", body[0:20]
            )
            ts = (ts_high << 32) | ts_low  # usbmon: microseconds
            packets.append((ts / 1e6, body[20:20 + cap_len]))
        off += block_len
    return packets


def decode_urb(pkt):
    """Return (dir_char, payload_bytes). dir_char: 'S' submit, 'C' callback."""
    if len(pkt) < 64:
        return None, b""
    typ = chr(pkt[8])
    len_cap, = struct.unpack("<I", pkt[36:40])
    return typ, pkt[64:64 + len_cap]


# ---------------------------------------------------------------------------
# command-channel decode
# ---------------------------------------------------------------------------
class Report:
    __slots__ = ("t", "dirn", "cmd", "clen", "off", "cksum", "body", "raw")

    def __init__(self, t, dirn, raw, wide_offset):
        self.t = t
        self.dirn = dirn
        self.raw = raw
        self.cmd = raw[3]
        self.clen = raw[4]
        if wide_offset:
            self.off = raw[5] | (raw[6] << 8) | (raw[7] << 16)
        else:
            self.off = raw[5] | (raw[6] << 8)
        self.cksum, = struct.unpack("<H", raw[1:3])
        self.body = bytes(raw[8:8 + self.clen])


def collect_reports(packets, wide_cmd):
    out = []
    for t, pkt in packets:
        typ, p = decode_urb(pkt)
        if len(p) < 8 or p[0] != 0x04:
            continue
        dirn = "REQ" if typ == "S" else "RSP"
        wide = p[3] == wide_cmd
        out.append(Report(t, dirn, bytes(p[:64]), wide))
    return out


def print_unique(reports):
    """One line per run of byte-identical reports."""
    print("=== unique frames (identical consecutive reports collapsed) ===")
    prev_key = None
    count = 0
    first_t = last_t = 0.0
    t0 = reports[0].t if reports else 0.0

    def flush():
        if prev_key is None:
            return
        dirn, cmd, clen, off, body = prev_key
        span = f"{first_t - t0:8.3f}s"
        if count > 1:
            span += f" ..{last_t - t0:8.3f}s x{count}"
        print(
            f"{span:32} {dirn} cmd=0x{cmd:02x} len={clen:<3} off={off:<7} "
            f"body={body.hex()}"
        )

    for r in reports:
        key = (r.dirn, r.cmd, r.clen, r.off, r.body)
        if key == prev_key:
            count += 1
            last_t = r.t
            continue
        flush()
        prev_key = key
        count = 1
        first_t = last_t = r.t
    flush()


FRAME_SLOT = 0x10000  # device-offset stride between animation frames


def reassemble_bursts(reports, cmd):
    """Group cmd's REQ chunks into one buffer per upload.

    Confirmed model (docs/SCREEN.md): each upload is a continuous run of
    chunks whose device offset climbs from 0; a 0x23 (begin-bulk) report
    or an offset that restarts at 0 marks a new upload. Chunks are laid
    down at their device offset, so a dropped packet shows as a gap.
    """
    bursts = []
    cur = None
    for r in reports:
        if r.dirn != "REQ":
            continue
        if r.cmd == 0x23:  # explicit begin-bulk delimiter
            cur = {"chunks": [], "buf": bytearray(), "t": r.t}
            bursts.append(cur)
            continue
        if r.cmd != cmd:
            continue
        if cur is None or (r.off == 0 and len(cur["buf"]) > 0):
            cur = {"chunks": [], "buf": bytearray(), "t": r.t}
            bursts.append(cur)
        if r.off > len(cur["buf"]):
            cur["buf"].extend(b"\x00" * (r.off - len(cur["buf"])))  # gap
        cur["buf"][r.off:r.off + len(r.body)] = r.body
        cur["chunks"].append((r.off, len(r.body)))
    return [b for b in bursts if b["chunks"]]


# ---------------------------------------------------------------------------
# RGB565 (big-endian) -> PNG, stdlib only
# ---------------------------------------------------------------------------
def rgb565_be_to_rgb(buf, width, height):
    px = bytearray(width * height * 3)
    n = min(len(buf) // 2, width * height)
    for i in range(n):
        v = (buf[2 * i] << 8) | buf[2 * i + 1]
        r5 = (v >> 11) & 0x1F
        g6 = (v >> 5) & 0x3F
        b5 = v & 0x1F
        px[3 * i] = (r5 << 3) | (r5 >> 2)
        px[3 * i + 1] = (g6 << 2) | (g6 >> 4)
        px[3 * i + 2] = (b5 << 3) | (b5 >> 2)
    return bytes(px)


def write_png(path, rgb, width, height):
    def chunk(tag, data):
        return (
            struct.pack(">I", len(data))
            + tag
            + data
            + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    raw = bytearray()
    stride = width * 3
    for y in range(height):
        raw.append(0)  # filter: none
        raw.extend(rgb[y * stride:(y + 1) * stride])
    png = b"\x89PNG\r\n\x1a\n"
    png += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(bytes(raw), 9))
    png += chunk(b"IEND", b"")
    with open(path, "wb") as f:
        f.write(png)


# ---------------------------------------------------------------------------
def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("pcap")
    ap.add_argument(
        "--cmd",
        type=lambda s: int(s, 0),
        default=0x21,
        help="bulk-write command ID to reassemble (default 0x21)",
    )
    ap.add_argument("--out", default="./decoded", help="output dir for bursts")
    ap.add_argument("--width", type=int, default=0)
    ap.add_argument("--height", type=int, default=0)
    ap.add_argument(
        "--all-cmds",
        action="store_true",
        help="also print the raw per-report list, not just the collapsed view",
    )
    args = ap.parse_args()

    packets = parse_pcapng(args.pcap)
    if not packets:
        sys.exit("no packets parsed -- is this a pcapng usbmon capture?")
    reports = collect_reports(packets, args.cmd)
    if not reports:
        sys.exit(
            "no 0x04-marker command-channel reports found. Capture the right "
            "interface: the command channel, not the boot-keyboard interface."
        )

    if args.all_cmds:
        t0 = reports[0].t
        for r in reports:
            print(
                f"t+{r.t - t0:8.3f}s {r.dirn} cmd=0x{r.cmd:02x} "
                f"len={r.clen:<3} off={r.off:<7} cksum=0x{r.cksum:04x} "
                f"body={r.body.hex()}"
            )
        print()

    print_unique(reports)

    seen_cmds = sorted({r.cmd for r in reports})
    print("\ncommand IDs seen:", ", ".join(f"0x{c:02x}" for c in seen_cmds))

    bursts = reassemble_bursts(reports, args.cmd)
    if not bursts:
        print(f"\nno cmd=0x{args.cmd:02x} write bursts in this capture.")
        return

    os.makedirs(args.out, exist_ok=True)
    t0 = reports[0].t
    pix = args.width * args.height * 2 if (args.width and args.height) else 0
    print(f"\n=== cmd 0x{args.cmd:02x} uploads ===")
    for i, b in enumerate(bursts):
        buf = bytes(b["buf"])
        raw_path = os.path.join(args.out, f"upload{i:02d}.bin")
        with open(raw_path, "wb") as f:
            f.write(buf)
        chunk_lens = sorted({n for _, n in b["chunks"]})
        nframes = max(1, -(-len(buf) // FRAME_SLOT))  # ceil
        print(
            f"upload {i:02d}  t+{b['t'] - t0:8.3f}s  {len(b['chunks'])} chunks  "
            f"chunk_len={chunk_lens}  total={len(buf)} bytes  "
            f"~{nframes} frame(s) of 0x{FRAME_SLOT:x}  -> {raw_path}"
        )
        if not pix:
            continue
        for fi in range(nframes):
            slot = buf[fi * FRAME_SLOT:(fi + 1) * FRAME_SLOT]
            frame = slot[:pix]
            rgb = rgb565_be_to_rgb(frame, args.width, args.height)
            png_path = os.path.join(args.out, f"upload{i:02d}_frame{fi:02d}.png")
            write_png(png_path, rgb, args.width, args.height)
            pad_nz = sum(1 for x in slot[pix:] if x)
            note = "" if len(frame) == pix else f"  SHORT: {len(frame)}/{pix} bytes"
            pnote = f"  ({pad_nz} non-zero padding bytes -- stale heap, ignore)" if pad_nz else ""
            print(f"           frame {fi}: -> {png_path}{note}{pnote}")

    if not pix:
        print("\npass --width/--height (e.g. --width 240 --height 135) to render PNGs.")


if __name__ == "__main__":
    main()
