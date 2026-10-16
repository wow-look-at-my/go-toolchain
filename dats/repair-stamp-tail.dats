# Tests for .github/scripts/repair-stamp-tail.sh, which the action runs on a
# downloaded release before it probes it. A stamp written after the GOSTDEMB
# record hides the record from the go command, and the repair moves it back.

tests:
	- desc: a stamp after the record moves before it, and the record ends the file again
	  cmd: |
		set -eu
		f=$(mktemp)
		{ printf 'binary body bytes'; printf 'GOSTDEMB'; head -c 56 /dev/zero; printf '{"release":"1"}'; printf '\000\000\000\017'; printf 'BUILDHOST-STAMP1'; } > "$f"
		before=$(wc -c < "$f" | tr -d ' ')
		bash .github/scripts/repair-stamp-tail.sh "$f"
		echo "size $(wc -c < "$f" | tr -d ' ') was $before"
		echo "last record $(tail -c 64 "$f" | head -c 8)"
		echo "stamp before it $(tail -c 99 "$f" | head -c 15)"
		echo "body $(head -c 17 "$f")"
	  outputs:
		stdout:
			- "moved the buildhost stamp before the GOSTDEMB record"
			- "size 116 was 116"
			- "last record GOSTDEMB"
			- "stamp before it {\"release\":\"1\"}"
			- "body binary body bytes"

	- desc: a file with the record last, and a file with no stamp, are left as they are
	  cmd: |
		set -eu
		good=$(mktemp)
		{ printf 'binary body bytes'; printf '{"release":"1"}'; printf '\000\000\000\017'; printf 'BUILDHOST-STAMP1'; printf 'GOSTDEMB'; head -c 56 /dev/zero; } > "$good"
		cp "$good" "$good.orig"
		bash .github/scripts/repair-stamp-tail.sh "$good"
		cmp "$good" "$good.orig" && echo "record-last unchanged"
		plain=$(mktemp)
		printf 'a binary with no stamp and no record' > "$plain"
		cp "$plain" "$plain.orig"
		bash .github/scripts/repair-stamp-tail.sh "$plain"
		cmp "$plain" "$plain.orig" && echo "plain unchanged"
	  outputs:
		stdout:
			- "record-last unchanged"
			- "plain unchanged"
