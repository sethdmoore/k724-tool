#!/usr/bin/env bash
# Install the k724 GUI for the current user on Linux:
#   - build the binary into ~/.local/bin
#   - install the udev rule (needs sudo)
#   - drop a .desktop entry and icon into ~/.local/share
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bindir="${HOME}/.local/bin"
appsdir="${HOME}/.local/share/applications"
icondir="${HOME}/.local/share/icons/hicolor/256x256/apps"

echo "==> building k724"
mkdir -p "$bindir"
( cd "$here" && go build -o "$bindir/k724" ./cmd/k724 )

echo "==> installing udev rule (sudo)"
sudo cp "$here/packaging/70-redragon-k724.rules" /etc/udev/rules.d/
sudo udevadm control --reload
sudo udevadm trigger

echo "==> installing desktop entry"
mkdir -p "$appsdir" "$icondir"
cp "$here/packaging/k724.desktop" "$appsdir/"
cp "$here/packaging/icon.png" "$icondir/k724.png"

echo
echo "Done. Make sure ${bindir} is on your PATH, then unplug and replug the"
echo "keyboard (or receiver) so the new udev rule takes effect."
