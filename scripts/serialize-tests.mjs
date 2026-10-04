// Inserts t.Serial() into every top-level Test function whose body contains a marker string.

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

// A test, and a helper the test hands its own T to: both run as the test, so either can take the barrier on its behalf.
const testFunc = /^func ([A-Za-z0-9_]*)\(([a-zA-Z_][A-Za-z0-9_]*) \*testing\.T[,)]/;

let changed = 0;
let serialized = 0;
for (const root of roots) {
	for (const path of testFiles(root)) {
		const lines = readFileSync(path, "utf8").split("\n");
		// A fixture holds Go source, and its own func starts at the left margin inside the raw string.
		const starts = [];
		let inRaw = false;
		for (let i = 0; i < lines.length; i++) {
			if (!inRaw && /^func /.test(lines[i])) starts.push(i);
			for (const _ of lines[i].matchAll(/`/g)) inRaw = !inRaw;
		}
		const insertAt = [];
		for (let s = 0; s < starts.length; s++) {
			const from = starts[s];
			const to = s + 1 < starts.length ? starts[s + 1] : lines.length;
			const m = testFunc.exec(lines[from]);
			if (!m) continue;
			const body = lines.slice(from + 1, to);
			if (!body.some((l) => l.includes(marker))) continue;
			// Only a hold taken at the top covers the whole test. t.Chdir.
			const holds = new RegExp(`\\b${m[2]}\\.(Serial\\(\\)|Chdir\\(|Setenv\\()`);
			if (body.slice(0, 2).some((l) => holds.test(l))) continue;
			// A test cannot be both.
			const parallel = new RegExp(`^\\s*${m[2]}\\.Parallel\\(\\)`).test(body[0] ?? "") ? 1 : 0;
			// A helper marks itself up front, so the barrier goes under that line.
			const after = new RegExp(`^\\s*${m[2]}\\.Helper\\(\\)$`).test(body[0] ?? "") ? 1 : 0;
			insertAt.push([from + 1 + after, parallel, `\t${m[2]}.Serial()`]);
		}
		if (insertAt.length === 0) continue;
		for (const [at, replaced, text] of insertAt.reverse()) lines.splice(at, replaced, text);
		writeFileSync(path, lines.join("\n"));
		changed++;
		serialized += insertAt.length;
		console.log(`${path}: ${insertAt.length}`);
	}
}
console.log(`${serialized} test(s) serialized across ${changed} file(s)`);
