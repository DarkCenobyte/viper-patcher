#!/usr/bin/env sh
set -eu

DESTINATION=${1:-dist/third-party-licenses/go-modules}
mkdir -p "$DESTINATION"

go list -m -f '{{if .Dir}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' all |
while IFS='|' read -r module_path module_version module_directory; do
    [ -n "$module_directory" ] || continue
    [ -n "$module_version" ] || continue
    safe_name=$(printf '%s_%s' "$module_path" "$module_version" | tr '/:@' '____')
    module_destination="$DESTINATION/$safe_name"
    copied=0
    for candidate in "$module_directory"/LICENSE* "$module_directory"/COPYING* "$module_directory"/NOTICE*; do
        [ -f "$candidate" ] || continue
        mkdir -p "$module_destination"
        cp "$candidate" "$module_destination/"
        copied=1
    done
    if [ "$copied" -eq 0 ]; then
        printf 'Warning: no top-level license file found for %s %s.\n' "$module_path" "$module_version" >&2
    fi
done
