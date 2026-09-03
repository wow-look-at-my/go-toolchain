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
//   os.Chdir         the test moves the process, which moves it for every
//                    test beside it. Prefer giving the code under test a
//                    root argument; reach for this only where the suite is
//                    short enough that running it serially costs nothing.
//   a package global the test assigns a variable its own package reads, so
//                    every test reading it sees the write
//
// A test already holding the barrier is skipped. t.Chdir and t.Setenv take it
// themselves, so a test spelling either one is already serial and needs no
// second take.
//
// Usage: node scripts/serialize-tests.mjs <marker> <dir>...
//
// scripts/serial-in-fixture.mjs audits the result: it fails on a barrier call
// that landed inside a Go string literal, which is what an earlier version of
// this script did to vet's fixtures.

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
		// A fixture holds Go source, and its own func starts at column zero
		// inside the raw string. Editing that corrupts the fixture and leaves
		// the test around it parallel, so the literal is skipped entirely.
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
			// Only a hold taken at the top covers the whole test. t.Chdir and
			// t.Setenv take the barrier where they are called, so a test that
			// reads shared state before one of them still raced up to there.
			const holds = new RegExp(`\\b${m[2]}\\.(Serial\\(\\)|Chdir\\(|Setenv\\()`);
			if (body.slice(0, 2).some((l) => holds.test(l))) continue;
			// A test cannot be both. The barrier is the stronger claim, so an
			// opt-in to parallelism gives way to it, comment and all.
			const parallel = new RegExp(`^\\s*${m[2]}\\.Parallel\\(\\)`).test(body[0] ?? "") ? 1 : 0;
			insertAt.push([from + 1, parallel, `\t${m[2]}.Serial()`]);
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
