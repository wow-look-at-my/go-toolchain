#!/usr/bin/env node
// Reports a t.Serial() call that sits inside a Go string literal.
//
// A fixture holds Go source, and its own `func TestFoo(t *testing.T) {` starts
// at column zero inside the literal. A rewrite that finds functions by scanning
// for that prefix therefore edits the fixture instead of the test around it,
// which both corrupts the fixture and leaves the real test parallel.
//
// Usage: node scripts/serial-in-fixture.mjs [--fix] <dir>...

import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const args = process.argv.slice(2);
const fix = args[0] === '--fix';
const roots = fix ? args.slice(1) : args;
if (roots.length === 0) {
	console.error('usage: serial-in-fixture.mjs [--fix] <dir>...');
	process.exit(2);
}

function* goFiles(dir) {
	for (const name of readdirSync(dir)) {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) yield* goFiles(path);
		else if (name.endsWith('.go')) yield path;
	}
}

let found = 0;
for (const root of roots) {
	for (const path of goFiles(root)) {
		let inRaw = false;
		const lines = readFileSync(path, 'utf8').split('\n');
		const keep = [];
		let dropped = 0;
		for (const [i, line] of lines.entries()) {
			if (inRaw && /^\s*[a-zA-Z_][A-Za-z0-9_]*\.(?:Serial\(\)|Chdir\(.*\))$/.test(line)) {
				console.log(`${path}:${i + 1}: ${line.trim()}`);
				found++;
				dropped++;
			} else {
				keep.push(line);
			}
			// A backtick opens or closes the literal; an even count leaves it as it was.
			for (const _ of line.matchAll(/`/g)) inRaw = !inRaw;
		}
		if (fix && dropped > 0) writeFileSync(path, keep.join('\n'));
	}
}
console.log(`${found} call(s) inside a literal`);
process.exit(found === 0 ? 0 : 1);
