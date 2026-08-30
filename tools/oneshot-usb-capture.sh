#!/usr/bin/env bash
#
# oneshot-usb-capture.sh
#
# TRIGGER button (mouse 5 / BTN_EXTRA), three presses, one stage each:
#   press 1 -> attach the USB device to the VM (no capture yet)
#   press 2 -> start the dumpcap usbmon capture
#   press 3 -> SIGINT the capture, detach the device, exit
# ABORT button (mouse 4 / BTN_SIDE): pressed at ANY point -> SIGINT the capture
#   (if running), detach the device (if attached), exit. Ctrl-C does the same.
#
# One evtest stream feeds a state machine, so the abort button works in every
# stage. No timers.
#
# The "redirect" is `virsh attach-device DOMAIN --file device.xml`, libvirt's
# supported hot-plug interface (what virt-manager calls internally). The XML is
# a device *description* handed to libvirtd; it is written to a real temp file
# here so you can inspect it (path printed on startup).

set -u

# ────────────────────────────── configuration ───────────────────────────────
VM_NAME="${VM_NAME:-bloatware}"                 # libvirt domain
LIBVIRT_URI="${LIBVIRT_URI:-qemu:///system}"

USB_VENDOR_ID="${USB_VENDOR_ID:-320f}"          # vendor:product, hex, no 0x
USB_PRODUCT_ID="${USB_PRODUCT_ID:-511b}"        # REDRAGON Gaming KB

# usbmon iface. The "3" in the "3-17" port path is the bus, so usbmon3.
# Use usbmon0 to capture every bus at once. Confirm with: lsusb -t
CAPTURE_IFACE="${CAPTURE_IFACE:-usbmon3}"
CAPTURE_FILE="${CAPTURE_FILE:-./write_light_a-r_s-g_d-b_q-w_e-bk.pcapng}"

WARMUP_SECONDS="${WARMUP_SECONDS:-1}"           # sanity-check dumpcap after start
DETACH_WHEN_DONE="${DETACH_WHEN_DONE:-1}"       # 1 = hand the device back to host

# Buttons live on the host mouse (which stays on the host).
#   find the node : ls -l /dev/input/by-id/ | grep -i mouse
#   find the code : run `evtest <dev>`, press the button, read "code NNN (BTN_x)"
# BTN_EXTRA (276) = forward thumb / "mouse 5".  BTN_SIDE (275) = back / "mouse 4".
TRIGGER_DEV="${TRIGGER_DEV:-/dev/input/by-id/usb-Logitech_USB_Receiver-event-mouse}"
TRIGGER_BTN="${TRIGGER_BTN:-BTN_EXTRA}"         # advances the stages
ABORT_BTN="${ABORT_BTN:-BTN_SIDE}"              # stop + exit from any stage
# ────────────────────────────────────────────────────────────────────────────

virsh() { command virsh --connect "$LIBVIRT_URI" "$@"; }

dumpcap_pid=""
hostdev_file=""
ev_pid=""
attached=0
cleaned=0

cleanup() {
  (( cleaned )) && return
  cleaned=1

  [[ -n "$ev_pid" ]] && kill "$ev_pid" 2>/dev/null

  if [[ -n "$dumpcap_pid" ]] && kill -0 "$dumpcap_pid" 2>/dev/null; then
    echo ">> SIGINT dumpcap ($dumpcap_pid)"
    kill -INT "$dumpcap_pid" 2>/dev/null
    wait "$dumpcap_pid" 2>/dev/null
  fi

  if (( attached )) && (( DETACH_WHEN_DONE )); then
    echo ">> detaching device from ${VM_NAME}"
    virsh detach-device "$VM_NAME" --file "$hostdev_file" --live 2>/dev/null || true
  fi

  [[ -n "$hostdev_file" ]] && rm -f -- "$hostdev_file"
}
trap 'cleanup; exit 130' INT TERM
trap cleanup EXIT

# ─────────────────────────────── preflight ──────────────────────────────────
for c in evtest stdbuf virsh dumpcap; do
  command -v "$c" >/dev/null || { echo "!! missing command: $c" >&2; exit 1; }
done
[[ -r "$TRIGGER_DEV" ]] || { echo "!! cannot read $TRIGGER_DEV" >&2; exit 1; }

modprobe usbmon 2>/dev/null || true

[[ "$(virsh domstate "$VM_NAME" 2>/dev/null)" == "running" ]] || {
  echo "!! domain '${VM_NAME}' is not running" >&2; exit 1; }

hostdev_file="$(mktemp --tmpdir oneshot-usb-hostdev.XXXXXX.xml)"
cat >"$hostdev_file" <<EOF
<hostdev mode='subsystem' type='usb' managed='yes'>
  <source>
    <vendor id='0x${USB_VENDOR_ID}'/>
    <product id='0x${USB_PRODUCT_ID}'/>
  </source>
</hostdev>
EOF

echo ">> domain ...... ${VM_NAME} (${LIBVIRT_URI})"
echo ">> device ...... ${USB_VENDOR_ID}:${USB_PRODUCT_ID}"
echo ">> hostdev xml . ${hostdev_file}"
echo ">> capture ..... ${CAPTURE_IFACE} -> ${CAPTURE_FILE}"
echo ">> trigger ..... ${TRIGGER_BTN} (advance)   abort: ${ABORT_BTN} (any time)"

# ────────────────────── event loop / state machine ─────────────────────────
# stdbuf -oL: evtest block-buffers when piped, so `read` would stall until
# ~4KB of events accumulate. $! after the process substitution is its PID.
exec {evfd}< <(stdbuf -oL evtest "$TRIGGER_DEV" 2>/dev/null)
ev_pid=$!

stage=0
stopped_cleanly=0
echo
echo ">> stage 1 — press ${TRIGGER_BTN} to ATTACH ${USB_VENDOR_ID}:${USB_PRODUCT_ID} to ${VM_NAME}"

while IFS= read -r -u "$evfd" line; do
  case "$line" in
    *"($ABORT_BTN), value 1"*)
      echo ">> ${ABORT_BTN} pressed — stopping"
      stopped_cleanly=1
      break
      ;;
    *"($TRIGGER_BTN), value 1"*)
      case "$stage" in
        0)
          echo ">> attaching device to ${VM_NAME}"
          virsh attach-device "$VM_NAME" --file "$hostdev_file" --live
          attached=1
          stage=1
          echo ">> stage 2 — press ${TRIGGER_BTN} to START capturing ${CAPTURE_IFACE} -> ${CAPTURE_FILE}"
          ;;
        1)
          rm -f -- "$CAPTURE_FILE"
          echo ">> starting dumpcap"
          dumpcap -q -i "$CAPTURE_IFACE" -w "$CAPTURE_FILE" </dev/null &
          dumpcap_pid=$!
          sleep "$WARMUP_SECONDS"
          if ! kill -0 "$dumpcap_pid" 2>/dev/null; then
            echo "!! dumpcap exited early — need the 'wireshark' group / readable /dev/${CAPTURE_IFACE}" >&2
            dumpcap_pid=""
            break
          fi
          stage=2
          echo ">> stage 3 — capturing; press ${TRIGGER_BTN} to STOP and exit"
          ;;
        2)
          echo ">> stop requested"
          stopped_cleanly=1
          break
          ;;
      esac
      ;;
  esac
done

(( stopped_cleanly )) || echo "!! ${TRIGGER_DEV} stopped producing events" >&2
# cleanup() on EXIT does: kill evtest, SIGINT dumpcap, detach, remove temp XML
