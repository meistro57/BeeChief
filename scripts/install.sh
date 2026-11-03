#!/usr/bin/env bash
# BeeChief install & update script for Linux and macOS.
#
# Usage:
#   ./scripts/install.sh [--from-source] [--version v0.5.0] [--prefix ~/.local/bin]
#   curl -fsSL https://raw.githubusercontent.com/meistro57/beechief/main/scripts/install.sh | bash
set -euo pipefail

REPO="${BEECHIEF_REPO:-meistro57/beechief}"
VERSION="${BEECHIEF_VERSION:-latest}"
PREFIX="${BEECHIEF_PREFIX:-}"
FROM_SOURCE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --from-source)
      FROM_SOURCE=1
      shift
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    --prefix)
      PREFIX="$2"
      shift 2
      ;;
    -h|--help)
      echo "Usage: $0 [--from-source] [--version <tag>] [--prefix <dir>]"
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 1
      ;;
  esac
done

if [ -z "${PREFIX}" ]; then
  if [ "$(id -u)" -eq 0 ]; then
    PREFIX="/usr/local/bin"
  else
    PREFIX="${HOME}/.local/bin"
  fi
fi

mkdir -p "${PREFIX}"

detect_os() {
  case "$(uname -s)" in
    Linux*)  echo "linux" ;;
    Darwin*) echo "darwin" ;;
    *)       echo "unsupported" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)             echo "unsupported" ;;
  esac
}

install_from_source() {
  if ! command -v go >/dev/null 2>&1; then
    echo "Error: 'go' is required for --from-source installation." >&2
    exit 1
  fi
  if [ -f "go.mod" ] && [ -d "cmd/beechief" ]; then
    VER_TAG="$(git describe --tags --always --dirty 2>/dev/null || echo "v0.5.0")"
    echo "==> Building BeeChief (${VER_TAG}) from local source..."
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/meistro57/beechief/internal/report.DefaultVersion=${VER_TAG}" \
      -o "${PREFIX}/beechief" ./cmd/beechief
  else
    echo "==> Installing BeeChief via go install..."
    GOBIN="${PREFIX}" go install "github.com/${REPO}/cmd/beechief@${VERSION}"
  fi
}

install_from_release() {
  OS="$(detect_os)"
  ARCH="$(detect_arch)"
  if [ "${OS}" = "unsupported" ] || [ "${ARCH}" = "unsupported" ]; then
    echo "Platform $(uname -s)/$(uname -m) not prebuilt; falling back to source build..."
    install_from_source
    return
  fi

  if [ "${VERSION}" = "latest" ]; then
    VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/' || true)"
  fi
  if [ -z "${VERSION}" ] || [ "${VERSION}" = "latest" ]; then
    echo "No published release found or offline; falling back to local source build..."
    install_from_source
    return
  fi

  ARCHIVE="beechief-${VERSION}-${OS}-${ARCH}.tar.gz"
  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "${TMP_DIR}"' EXIT

  echo "==> Downloading ${ARCHIVE} from ${BASE_URL}..."
  if ! curl -fsSL "${BASE_URL}/${ARCHIVE}" -o "${TMP_DIR}/${ARCHIVE}"; then
    echo "Download failed; falling back to source build..."
    install_from_source
    return
  fi

  if curl -fsSL "${BASE_URL}/SHA256SUMS.txt" -o "${TMP_DIR}/SHA256SUMS.txt" 2>/dev/null; then
    echo "==> Verifying SHA-256 checksum..."
    (
      cd "${TMP_DIR}"
      grep " ${ARCHIVE}\$" SHA256SUMS.txt | sha256sum -c - >/dev/null 2>&1 || \
        grep " ${ARCHIVE}\$" SHA256SUMS.txt | shasum -a 256 -c - >/dev/null 2>&1
    )
  fi

  tar -C "${TMP_DIR}" -xzf "${TMP_DIR}/${ARCHIVE}"
  install -m 0755 "${TMP_DIR}/beechief-${VERSION}-${OS}-${ARCH}/beechief" "${PREFIX}/beechief"
}

if [ "${FROM_SOURCE}" -eq 1 ]; then
  install_from_source
else
  install_from_release
fi

chmod +x "${PREFIX}/beechief"
echo "==> Installed: $("${PREFIX}/beechief" --version)"
echo "    Binary location: ${PREFIX}/beechief"

CFG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/beechief"
if [ ! -f "${CFG_DIR}/hosts.yaml" ] && [ -f "configs/hosts.example.yaml" ]; then
  mkdir -p "${CFG_DIR}"
  cp configs/hosts.example.yaml "${CFG_DIR}/hosts.yaml"
  chmod 0600 "${CFG_DIR}/hosts.yaml"
  echo "==> Created starter config at ${CFG_DIR}/hosts.yaml"
fi
