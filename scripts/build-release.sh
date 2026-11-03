#!/usr/bin/env bash
# Build cross-platform BeeChief release binaries and archives (Linux, Windows, macOS).
set -euo pipefail

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo "v0.5.0")}"
OUT_DIR="${2:-${OUT_DIR:-dist}}"
PKG="github.com/meistro57/beechief/internal/report"
LDFLAGS="-s -w -X ${PKG}.DefaultVersion=${VERSION}"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "windows/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

echo "==> Building BeeChief ${VERSION} release binaries into ${OUT_DIR}/"

for target in "${TARGETS[@]}"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  STAGE_NAME="beechief-${VERSION}-${GOOS}-${GOARCH}"
  STAGE_DIR="${OUT_DIR}/${STAGE_NAME}"
  mkdir -p "${STAGE_DIR}"

  BIN_NAME="beechief"
  if [ "${GOOS}" = "windows" ]; then
    BIN_NAME="beechief.exe"
  fi

  echo "  • ${GOOS}/${GOARCH} -> ${BIN_NAME}"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${STAGE_DIR}/${BIN_NAME}" ./cmd/beechief

  cp README.md LICENSE "${STAGE_DIR}/"
  mkdir -p "${STAGE_DIR}/configs"
  cp configs/hosts.example.yaml "${STAGE_DIR}/configs/hosts.example.yaml"

  if [ "${GOOS}" = "windows" ]; then
    if command -v zip >/dev/null 2>&1; then
      (cd "${OUT_DIR}" && zip -qr "${STAGE_NAME}.zip" "${STAGE_NAME}")
    else
      tar -C "${OUT_DIR}" -czf "${OUT_DIR}/${STAGE_NAME}.tar.gz" "${STAGE_NAME}"
    fi
  else
    tar -C "${OUT_DIR}" -czf "${OUT_DIR}/${STAGE_NAME}.tar.gz" "${STAGE_NAME}"
  fi

  rm -rf "${STAGE_DIR}"
done

(
  cd "${OUT_DIR}"
  sha256sum *.tar.gz *.zip 2>/dev/null > SHA256SUMS.txt || shasum -a 256 *.tar.gz *.zip > SHA256SUMS.txt
)

echo "==> Built artifacts in ${OUT_DIR}/:"
ls -lh "${OUT_DIR}"
