#!/bin/sh
set -u

PROJECT_NAME="hoard"
OWNER="spiffcs"
REPO="${PROJECT_NAME}"
GITHUB_DOWNLOAD_PREFIX="https://github.com/${OWNER}/${REPO}/releases/download"

# signature verification options
COSIGN_BINARY="${COSIGN_BINARY:-cosign}"
VERIFY_SIGN=false

# ------------------------------------------------------------------------
# logging
# ------------------------------------------------------------------------

log_info() {
  echo "[info] $*" >&2
}

log_err() {
  echo "[error] $*" >&2
}

# ------------------------------------------------------------------------
# platform detection
# ------------------------------------------------------------------------

get_os() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    darwin|linux) echo "$os" ;;
    *)
      log_err "unsupported OS: $os"
      return 1
      ;;
  esac
}

get_arch() {
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      log_err "unsupported architecture: $arch"
      return 1
      ;;
  esac
}

# ------------------------------------------------------------------------
# http helpers
# ------------------------------------------------------------------------

http_download() {
  local_file="$1"
  url="$2"

  if command -v curl >/dev/null 2>&1; then
    code=$(curl -w '%{http_code}' -sL -o "$local_file" "$url")
    if [ "$code" != "200" ]; then
      log_err "failed to download $url (HTTP $code)"
      return 1
    fi
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$local_file" "$url" || return 1
  else
    log_err "curl or wget required"
    return 1
  fi
}

# ------------------------------------------------------------------------
# github release helpers
# ------------------------------------------------------------------------

get_latest_tag() {
  url="https://github.com/${OWNER}/${REPO}/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    tag=$(curl -sI "$url" | grep -i "^location:" | sed 's/.*tag\///' | tr -d '\r\n')
  elif command -v wget >/dev/null 2>&1; then
    tag=$(wget --spider -S "$url" 2>&1 | grep -i "^  location:" | sed 's/.*tag\///' | tr -d '\r\n')
  fi

  if [ -z "$tag" ]; then
    log_err "failed to get latest release tag"
    return 1
  fi
  echo "$tag"
}

# ------------------------------------------------------------------------
# checksum verification
# ------------------------------------------------------------------------

hash_sha256() {
  target="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$target" | cut -d ' ' -f 1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$target" | cut -d ' ' -f 1
  else
    log_err "sha256sum or shasum required"
    return 1
  fi
}

verify_checksum() {
  archive="$1"
  checksums="$2"

  archive_name=$(basename "$archive")
  want=$(grep "$archive_name" "$checksums" | cut -d ' ' -f 1)

  if [ -z "$want" ]; then
    log_err "checksum not found for $archive_name"
    return 1
  fi

  got=$(hash_sha256 "$archive")
  if [ "$want" != "$got" ]; then
    log_err "checksum mismatch: expected $want, got $got"
    return 1
  fi
}

# ------------------------------------------------------------------------
# signature verification
# ------------------------------------------------------------------------

verify_signature() {
  checksums_file="$1"
  bundle_file="$2"

  if ! command -v "$COSIGN_BINARY" >/dev/null 2>&1; then
    log_err "cosign not found (required for signature verification)"
    return 1
  fi

  if ! "$COSIGN_BINARY" verify-blob "$checksums_file" \
      --bundle "$bundle_file" \
      --certificate-identity-regexp "^https://github.com/${OWNER}/${REPO}/.*" \
      --certificate-oidc-issuer "https://token.actions.githubusercontent.com" >/dev/null 2>&1; then
    log_err "signature verification failed"
    return 1
  fi

  log_info "signature verification succeeded"
}

# ------------------------------------------------------------------------
# main
# ------------------------------------------------------------------------

main() {
  install_dir="./bin"

  # parse arguments
  while getopts "b:vh" arg; do
    case "$arg" in
      b) install_dir="$OPTARG" ;;
      v) VERIFY_SIGN=true ;;
      h)
        cat <<EOF
Install ${PROJECT_NAME} from GitHub releases

Usage: $0 [-v] [-b DIR] [TAG]
  -b DIR  installation directory (default: ./bin)
  -v      verify cosign signature
  TAG     version to install (default: latest)
EOF
        exit 0
        ;;
      *) exit 1 ;;
    esac
  done
  shift $((OPTIND - 1))

  # get version
  tag="${1:-}"
  if [ -z "$tag" ]; then
    log_info "fetching latest release..."
    tag=$(get_latest_tag) || exit 1
  fi

  version="${tag#v}"
  os=$(get_os) || exit 1
  arch=$(get_arch) || exit 1

  log_info "installing ${PROJECT_NAME} ${tag} (${os}/${arch})"

  # setup temp directory
  tmp_dir=$(mktemp -d)
  trap 'rm -rf "$tmp_dir"' EXIT

  download_url="${GITHUB_DOWNLOAD_PREFIX}/${tag}"
  archive_name="${PROJECT_NAME}_${version}_${os}_${arch}.tar.gz"

  # download checksums
  log_info "downloading checksums..."
  http_download "$tmp_dir/checksums.txt" "$download_url/checksums.txt" || exit 1

  # verify signature if requested
  if [ "$VERIFY_SIGN" = true ]; then
    log_info "downloading signature bundle..."
    http_download "$tmp_dir/checksums.txt.sigstore.json" "$download_url/checksums.txt.sigstore.json" || exit 1
    verify_signature "$tmp_dir/checksums.txt" "$tmp_dir/checksums.txt.sigstore.json" || exit 1
  fi

  # download archive
  log_info "downloading ${archive_name}..."
  http_download "$tmp_dir/$archive_name" "$download_url/$archive_name" || exit 1

  # verify checksum
  log_info "verifying checksum..."
  verify_checksum "$tmp_dir/$archive_name" "$tmp_dir/checksums.txt" || exit 1

  # extract and install
  log_info "extracting..."
  tar -xzf "$tmp_dir/$archive_name" -C "$tmp_dir"

  mkdir -p "$install_dir"
  install "$tmp_dir/${PROJECT_NAME}" "$install_dir/"

  log_info "installed ${install_dir}/${PROJECT_NAME}"
}

main "$@"
