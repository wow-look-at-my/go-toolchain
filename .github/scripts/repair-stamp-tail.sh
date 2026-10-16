#!/usr/bin/env bash
# Moves a buildhost stamp that sits after the GOSTDEMB record of FILE back before it.
set -euo pipefail

file="$1"
size=$(wc -c < "$file" | tr -d ' ')
if [ "$size" -lt 84 ]; then
	exit 0
fi
# The stamp ends the file with a 4-byte big-endian payload length and a 16-byte magic.
magic=$(tail -c 16 "$file" | od -An -tx1 -v | tr -d ' \n')
if [ "$magic" != "4255494c44484f53542d5354414d5031" ]; then
	exit 0
fi
read -r b0 b1 b2 b3 < <(tail -c 20 "$file" | head -c 4 | od -An -tu1 -v)
stamp=$((b0 * 16777216 + b1 * 65536 + b2 * 256 + b3 + 20))
if [ "$size" -lt $((stamp + 64)) ]; then
	exit 0
fi
record=$(tail -c $((stamp + 64)) "$file" | head -c 8 | od -An -tx1 -v | tr -d ' \n')
if [ "$record" != "474f535444454d42" ]; then
	exit 0
fi
tmp="$file.repair"
{
	head -c $((size - stamp - 64)) "$file"
	tail -c "$stamp" "$file"
	tail -c $((stamp + 64)) "$file" | head -c 64
} > "$tmp"
cat "$tmp" > "$file"
rm -f "$tmp"
echo "repaired $file: moved the buildhost stamp before the GOSTDEMB record"
