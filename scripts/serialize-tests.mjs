// Inserts t.Serial() into every top-level Test function whose body contains a
// marker string.
//
// The fork runs tests in parallel unless one takes the serial barrier. Some
// tests cannot share a process at all, and each kind announces itself in the
// source:
//
//   ResetWarnCount   the warnings budget counts a whole run, so its counters
//                    are process-wide and a test that resets them owns them
//   analysistest.    x/tools chdirs into the fixture, and nothing here can
//                    pass it a directory instead
//
// Usage: node scripts/serialize-tests.mjs <marker> <dir>...

import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const [marker, ...roots] = process.argv.slice(2);
if (!marker || roots.length === 0) {
	console.error("usage: serialize-tests.mjs <marker> <dir>...");
	process.exit(2);
}

function* testFiles(dir) {
	for (const name of readdirSync(dir)) {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) {
			yield* testFiles(path);
		} else if (name.endsWith("_test.go")) {
			yield path;
		}
	}
}

const testFunc = /^func (Test[A-Za-z0-9_]*)\(([a-zA-Z_][A-Za-z0-9_]*) \*testing\.T\) \{$/;

let changed = 0;
let serialized = 0;
for (const root of roots) {
	for (const path of testFiles(root)) {
		const lines = readFileSync(path, "utf8").split("\n");
		const starts = [];
		for (let i = 0; i < lines.length; i++) {
			if (/^func /.test(lines[i])) starts.push(i);
		}
		const insertAt = [];
		for (let s = 0; s < starts.length; s++) {
			const from = starts[s];
			const to = s + 1 < starts.length ? starts[s + 1] : lines.length;
			const m = testFunc.exec(lines[from]);
			if (!m) continue;
			const body = lines.slice(from + 1, to);
			if (!body.some((l) => l.includes(marker))) continue;
			if ((lines[from + 1] ?? "").trim() === `${m[2]}.Serial()`) continue;
			insertAt.push([from + 1, `\t${m[2]}.Serial()`]);
		}
		if (insertAt.length === 0) continue;
		for (const [at, text] of insertAt.reverse()) lines.splice(at, 0, text);
		writeFileSync(path, lines.join("\n"));
		changed++;
		serialized += insertAt.length;
		console.log(`${path}: ${insertAt.length}`);
	}
}
console.log(`${serialized} test(s) serialized across ${changed} file(s)`);
