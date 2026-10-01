# Explains why APEs differ: sizes, how many bytes differ, and the bytes
# around the earliest differences, so a red identical job says where to look.
set -uo pipefail

a="$1"
b="$2"
ls -l "$a" "$b"
if cmp -s "$a" "$b"; then
	echo "same bytes"
	exit 0
fi
echo "differing bytes: $(cmp -l "$a" "$b" | wc -l)"
echo "first differing offsets:"
cmp -l "$a" "$b" | head -20
first=$(cmp -l "$a" "$b" | head -1 | awk '{print $1}')
for f in "$a" "$b"; do
	echo "== $f around byte $first"
	xxd -s "$((first > 64 ? first - 64 : 0))" -l 160 "$f"
done
for f in "$a" "$b"; do
	echo "== $f build settings"
	grep -a -o -E 'build[[:space:]]+(vcs\.[a-z]+|-[a-zA-Z]+|GO[A-Z]+)=[^[:space:]]*' "$f" | sort -u | head -30
done
echo "APE header lines:"
head -c 4096 "$a" | strings | head -12
head -c 4096 "$b" | strings | head -12
echo "strings in only one of the two (< $a, > $b):"
diff <(strings -n 8 "$a" | sort -u) <(strings -n 8 "$b" | sort -u) | grep '^[<>]' | head -120
