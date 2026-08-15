#!/usr/bin/env bash
# Regenerates THIRD_PARTY_LICENSES.md.
#
# Apache-2.0 and the BSD licenses require reproducing their copyright notices
# in binary redistributions, not just source. Baobar ships binaries via GitHub
# Releases and Homebrew, so the notices have to travel inside the archives.
#
# The dependency set differs per platform — Windows links go-ole and go-toast,
# Linux links esiqveland/notify, macOS neither — so this unions all three
# rather than reporting for whatever host it happens to run on.
#
# Usage: tools/gen-third-party-licenses.sh [output-file]
set -euo pipefail

OUT="${1:-THIRD_PARTY_LICENSES.md}"
SELF="github.com/BrutalSystems/baobar"

if ! command -v go-licenses >/dev/null 2>&1; then
	echo "go-licenses not found. Install it with:" >&2
	echo "  go install github.com/google/go-licenses@latest" >&2
	exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Collect "module,url,license" for every platform we release for.
: >"$work/all.csv"
for os in darwin linux windows; do
	GOOS="$os" go-licenses report ./cmd/baobar 2>/dev/null >>"$work/all.csv" || {
		echo "go-licenses failed for GOOS=$os" >&2
		exit 1
	}
	GOOS="$os" go-licenses save ./cmd/baobar --save_path="$work/texts-$os" --force 2>/dev/null || true
done

sort -u -t, -k1,1 "$work/all.csv" | grep -v "^${SELF}," >"$work/mods.csv"

{
	echo "# Third-party licenses"
	echo
	echo "Baobar is distributed as a binary that includes the following"
	echo "open-source components. Each is reproduced below as its license"
	echo "requires. Baobar's own license is in [LICENSE](LICENSE)."
	echo
	echo "Regenerate with \`tools/gen-third-party-licenses.sh\`."
	echo
	echo "| Component | License |"
	echo "| --- | --- |"
	while IFS=, read -r mod url lic; do
		[ -z "$mod" ] && continue
		echo "| [$mod]($url) | $lic |"
	done <"$work/mods.csv"
	echo

	while IFS=, read -r mod url lic; do
		[ -z "$mod" ] && continue
		echo "---"
		echo
		echo "## $mod"
		echo
		echo "License: $lic — <$url>"
		echo
		text=""
		for os in darwin linux windows; do
			for candidate in "$work/texts-$os/$mod"/*; do
				if [ -f "$candidate" ]; then
					text="$candidate"
					break 2
				fi
			done
		done
		if [ -n "$text" ]; then
			echo '```'
			cat "$text"
			echo '```'
		else
			echo "_License text not retrieved; see the URL above._"
		fi
		echo
	done <"$work/mods.csv"
} >"$OUT"

echo "wrote $OUT ($(grep -c '^## ' "$OUT") components)"
