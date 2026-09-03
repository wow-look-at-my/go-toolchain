// Inserts t.Serial() into every top-level Test function whose body calls
// os.Chdir.
//
// The fork runs tests in parallel unless they opt out, and a test that changes
// the working directory changes it for every test running beside it -- which
// reads as "getwd: no such file or directory" or a repo-relative open failing
// in some unrelated test. t.Chdir would take the barrier by itself; os.Chdir
// does not, so the test has to say so.
//
// Usage: node scripts/serialize-chdir-tests.mjs src/cmd src/vet

import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const roots = process.argv.slice(2);
if (roots.length === 0) {
	console.error("usage: serialize-chdir-tests.mjs <dir>...");
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
		// Each top-level func runs to the next line starting a new one.
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
			if (!body.some((l) => l.includes("os.Chdir"))) continue;
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
