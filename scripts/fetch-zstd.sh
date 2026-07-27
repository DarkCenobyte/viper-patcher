#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION_SOURCE="$ROOT/internal/zstdversion/version.go"
VERSION=$(sed -n 's/^[[:space:]]*const Version = "\([^"]*\)".*/\1/p' "$VERSION_SOURCE" | head -n 1)
if [ -z "$VERSION" ]; then
    printf 'Could not read the required zstd version from %s.\n' "$VERSION_SOURCE" >&2
    exit 1
fi
ARCHIVE="zstd-${VERSION}.tar.gz"
URL="https://github.com/facebook/zstd/releases/download/v${VERSION}/${ARCHIVE}"
EXPECTED_SHA256="eb33e51f49a15e023950cd7825ca74a4a2b43db8354825ac24fc1b7ee09e6fa3"
DESTINATION="$ROOT/third_party/zstd"
CACHE_DIRECTORY="$ROOT/build/downloads"
ARCHIVE_PATH="$CACHE_DIRECTORY/$ARCHIVE"

if [ -f "$DESTINATION/lib/zstd.h" ]; then
    major=$(sed -n 's/^#define ZSTD_VERSION_MAJOR[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$DESTINATION/lib/zstd.h" | head -n 1)
    minor=$(sed -n 's/^#define ZSTD_VERSION_MINOR[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$DESTINATION/lib/zstd.h" | head -n 1)
    release=$(sed -n 's/^#define ZSTD_VERSION_RELEASE[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$DESTINATION/lib/zstd.h" | head -n 1)
    actual_version="${major:-unknown}.${minor:-unknown}.${release:-unknown}"
    if [ "$actual_version" = "$VERSION" ]; then
        printf 'zstd %s is already available.\n' "$VERSION"
        exit 0
    fi
    printf 'Removing unexpected zstd source version %s.\n' "${actual_version:-unknown}"
    rm -rf "$DESTINATION"
fi

mkdir -p "$CACHE_DIRECTORY" "$ROOT/third_party"
if [ ! -f "$ARCHIVE_PATH" ]; then
    printf 'Downloading %s...\n' "$URL"
    curl --fail --location --retry 3 --output "$ARCHIVE_PATH" "$URL"
fi

if command -v sha256sum >/dev/null 2>&1; then
    actual_sha256=$(sha256sum "$ARCHIVE_PATH" | awk '{print $1}')
else
    actual_sha256=$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')
fi
if [ "$actual_sha256" != "$EXPECTED_SHA256" ]; then
    printf 'SHA-256 mismatch for %s.\nExpected: %s\nActual:   %s\n' "$ARCHIVE_PATH" "$EXPECTED_SHA256" "$actual_sha256" >&2
    exit 1
fi

temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/viper-patcher-zstd.XXXXXX")
trap 'rm -rf "$temporary_directory"' EXIT INT TERM
tar -xzf "$ARCHIVE_PATH" -C "$temporary_directory"
rm -rf "$DESTINATION"
mv "$temporary_directory/zstd-${VERSION}" "$DESTINATION"
printf 'Extracted zstd %s to %s.\n' "$VERSION" "$DESTINATION"
