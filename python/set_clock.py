#!/usr/bin/env python3
"""Set the Redragon K724-RGB-PRO's onboard clock from the local time.

Protocol confirmed by a live usbmon capture (see RE_STATUS.md, session 3):
a 64-byte interrupt-OUT HID report, command ID 0x06, sent as 3 chunks.

The default target is the wired keyboard (vid=0x320f pid=0x511b). Use
--wireless to target the 2.4 GHz receiver (vid=0x320f pid=0x511c) instead.
--vid and --pid override both defaults.

Requires: pip install hidapi
"""
import argparse
import datetime
import sys
import time

TEST_TIME = datetime.datetime(2000, 1, 1, 23, 59, 59).timetuple()

# Wired keyboard identity ("REDRAGON Gaming KB"). This is the default target.
WIRED_VID = 0x320F
WIRED_PID = 0x511B

# 2.4 GHz wireless receiver identity ("REDRAGON 2.4G Wireless Receiver").
# Select it with --wireless.
WIRELESS_VID = 0x320F
WIRELESS_PID = 0x511C

TEMPLATE_PREFIX = bytes.fromhex(
    "000503020001cccccc06000000b400ff00ff0000ff00000000000000000000ff00000a"
)
TEMPLATE_SUFFIX = bytes.fromhex("00640000000100")
READ_TIMEOUT_MS = 1000


def bcd(value):
    return ((value // 10) << 4) | (value % 10)


def checksum(body):
    return sum(body[3:]) & 0xFFFF


def build_report(cmd, offset, chunk):
    body = bytearray(64)
    body[0] = 0x04
    body[3] = cmd
    body[4] = len(chunk)
    body[5] = offset & 0xFF
    body[6] = (offset >> 8) & 0xFF
    body[8:8 + len(chunk)] = chunk
    cksum = checksum(body[:8 + len(chunk)])
    body[1] = cksum & 0xFF
    body[2] = (cksum >> 8) & 0xFF
    return bytes(body)


def clock_payload(when=None):
    t = when or time.localtime()
    device_weekday = (t.tm_wday + 1) % 7  # Python Mon=0..Sun=6 -> device Sun=0..Sat=6
    fields = bytes(
        [
            bcd(t.tm_sec),
            bcd(t.tm_min),
            bcd(t.tm_hour),
            bcd(device_weekday),
            bcd(t.tm_mday),
            bcd(t.tm_mon),
            bcd(t.tm_year % 100),
        ]
    )
    buf = TEMPLATE_PREFIX + fields + TEMPLATE_SUFFIX
    assert len(buf) == 49, len(buf)
    return buf


def find_candidates():
    import hid

    candidates = []
    for info in hid.enumerate():
        usage_page = info.get("usage_page", 0)
        if usage_page in (0x0001, 0x0007):  # generic desktop / standard keyboard usage pages, skip
            continue
        candidates.append(info)
    return candidates


def probe(path):
    import hid

    dev = hid.Device(path=path)
    try:
        dev.write(build_report(0xAA, 0, b""))
        reply = dev.read(64, timeout=READ_TIMEOUT_MS)
        if reply and reply[0] == 0x04 and reply[3] == 0xAA:
            return dev
    except OSError:
        pass
    dev.close()
    return None


def open_device(vid, pid, path):
    if path:
        dev = probe(path)
        if dev:
            return dev
        raise SystemExit(f"device at path {path!r} did not answer the 0xAA ping")
    if vid and pid:
        candidates = [i for i in find_candidates() if i["vendor_id"] == vid and i["product_id"] == pid]
    else:
        candidates = find_candidates()
    for info in candidates:
        dev = probe(info["path"])
        if dev:
            print(
                f"using {info.get('product_string')!r} "
                f"(vid=0x{info['vendor_id']:04x} pid=0x{info['product_id']:04x} "
                f"interface={info.get('interface_number')})",
                file=sys.stderr,
            )
            return dev
    raise SystemExit(
        "no HID interface answered the 0xAA ping — pass --vid/--pid/--path explicitly, "
        "or list candidates with --list"
    )


def send_and_confirm(dev, cmd, offset, chunk):
    report = build_report(cmd, offset, chunk)
    dev.write(report)
    reply = dev.read(64, timeout=READ_TIMEOUT_MS)
    if not reply or reply[0] != 0x04 or reply[3] != cmd:
        raise SystemExit(f"no valid reply for cmd 0x{cmd:02x} offset {offset}")


def set_clock(dev, when=None):
    payload = clock_payload(when)
    send_and_confirm(dev, 0xAA, 0, b"")
    send_and_confirm(dev, 0x01, 0, b"")
    send_and_confirm(dev, 0x01, 0, b"")
    send_and_confirm(dev, 0x06, 0, payload[0:24])
    send_and_confirm(dev, 0x06, 24, payload[24:48])
    send_and_confirm(dev, 0x06, 48, payload[48:49])
    send_and_confirm(dev, 0x02, 0, b"")


def main():
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "--vid",
        type=lambda s: int(s, 0),
        default=None,
        help="override the vendor ID (takes priority over the default and --wireless)",
    )
    parser.add_argument(
        "--pid",
        type=lambda s: int(s, 0),
        default=None,
        help="override the product ID (takes priority over the default and --wireless)",
    )
    parser.add_argument("--path", type=str, default=None, help="exact hidapi device path")
    parser.add_argument(
        "--wireless",
        action="store_true",
        help=(
            "target the 2.4 GHz wireless receiver "
            f"(vid=0x{WIRELESS_VID:04x} pid=0x{WIRELESS_PID:04x}) instead of the "
            f"wired keyboard (vid=0x{WIRED_VID:04x} pid=0x{WIRED_PID:04x})"
        ),
    )
    parser.add_argument("--list", action="store_true", help="list candidate HID interfaces and exit")
    parser.add_argument("--dry-run", action="store_true", help="print the reports without opening a device")
    parser.add_argument(
        "--test",
        action="store_true",
        help="set an obviously fake time (2000-01-01 23:59:59) to confirm the write took effect",
    )
    args = parser.parse_args()
    when = TEST_TIME if args.test else None

    # Pick the target device identity. --vid/--pid are explicit overrides and
    # take priority over --wireless and the wired default.
    if args.vid is None and args.pid is None and not args.path:
        if args.wireless:
            args.vid, args.pid = WIRELESS_VID, WIRELESS_PID
        else:
            args.vid, args.pid = WIRED_VID, WIRED_PID

    if args.list:
        for info in find_candidates():
            print(
                f"vid=0x{info['vendor_id']:04x} pid=0x{info['product_id']:04x} "
                f"path={info['path']!r} product={info.get('product_string')!r} "
                f"interface={info.get('interface_number')} usage_page=0x{info.get('usage_page', 0):04x}"
            )
        return

    if args.dry_run:
        payload = clock_payload(when)
        for cmd, offset, chunk in [
            (0xAA, 0, b""),
            (0x01, 0, b""),
            (0x01, 0, b""),
            (0x06, 0, payload[0:24]),
            (0x06, 24, payload[24:48]),
            (0x06, 48, payload[48:49]),
            (0x02, 0, b""),
        ]:
            print(build_report(cmd, offset, chunk).hex())
        return

    dev = open_device(args.vid, args.pid, args.path)
    try:
        set_clock(dev, when)
        print("clock set OK" + (" (test value: 2000-01-01 23:59:59)" if args.test else ""))
    finally:
        dev.close()


if __name__ == "__main__":
    main()
