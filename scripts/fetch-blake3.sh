#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION_SOURCE="$ROOT/internal/blake3version/version.go"
VERSION=$(sed -n 's/^[[:space:]]*const Version = "\([^"]*\)".*/\1/p' "$VERSION_SOURCE" | head -n 1)
if [ -z "$VERSION" ]; then
    printf 'Could not read the required BLAKE3 version from %s.\n' "$VERSION_SOURCE" >&2
    exit 1
fi
ARCHIVE="blake3-${VERSION}.crate"
URL="https://static.crates.io/crates/blake3/${ARCHIVE}"
EXPECTED_SHA256="0aa83c34e62843d924f905e0f5c866eb1dd6545fc4d719e803d9ba6030371fce"
DESTINATION="$ROOT/third_party/blake3"
CACHE_DIRECTORY="$ROOT/build/downloads"
ARCHIVE_PATH="$CACHE_DIRECTORY/$ARCHIVE"

if [ -f "$DESTINATION/c/blake3.h" ] && grep -q "BLAKE3_VERSION_STRING \"$VERSION\"" "$DESTINATION/c/blake3.h" 2>/dev/null; then
    printf 'BLAKE3 %s is already available.\n' "$VERSION"
    exit 0
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

temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/viper-patcher-blake3.XXXXXX")
trap 'rm -rf "$temporary_directory"' EXIT INT TERM
tar -xzf "$ARCHIVE_PATH" -C "$temporary_directory"
rm -rf "$DESTINATION"
mv "$temporary_directory/blake3-${VERSION}" "$DESTINATION"
printf 'Extracted BLAKE3 %s to %s.\n' "$VERSION" "$DESTINATION"
