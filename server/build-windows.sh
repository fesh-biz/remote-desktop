#!/usr/bin/env bash
# Builds the portable Windows server. Usage: ./build-windows.sh [version] [output]
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-dev}"
OUT="${2:-../dist/RemoteDesktop.exe}"
NUM="${VERSION#v}"
IFS=. read -r V1 V2 V3 <<<"${NUM}.0.0"
[[ "$V1" =~ ^[0-9]+$ ]] || V1=0
[[ "$V2" =~ ^[0-9]+$ ]] || V2=0
[[ "$V3" =~ ^[0-9]+$ ]] || V3=0
if command -v x86_64-w64-mingw32-windres >/dev/null; then
  sed -e "s/@V1@/$V1/g; s/@V2@/$V2/g; s/@V3@/$V3/g; s/@VERSION@/$VERSION/g" winres/app.rc.in > winres/app.rc
  (cd winres && x86_64-w64-mingw32-windres -O coff -i app.rc -o ../rsrc_windows_amd64.syso)
else
  echo "windres not found: building without icon/manifest" >&2
fi
mkdir -p "$(dirname "$OUT")"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=$VERSION" -o "$OUT" .
echo "built $OUT"
