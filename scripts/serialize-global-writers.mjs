#!/usr/bin/env node
// Serializes every test that assigns one of its own package's variables.
//
// A package-level variable is the run's, not the test's, so a test that swaps
// one swaps it for every test beside it. Save-mutate-restore reads as local and
// is not: the restore runs a whole test later than the write.
//
// This reads the package's variable names off its non-test files, then drives
// scripts/serialize-tests.mjs once per name. A name a test declares itself is
// not a global, so only an assignment with no := for that name counts.
//
// Usage: node scripts/serialize-global-writers.mjs <package dir>...

import { readFileSync, readdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

const dirs = process.argv.slice(2);
if (dirs.length === 0) {
	console.error('usage: serialize-global-writers.mjs <package dir>...');
	process.exit(2);
}

// Reads the package-level variable names a directory's non-test files declare.
function globalNames(dir) {
	const names = new Set();
	for (const name of readdirSync(dir)) {
		if (!name.endsWith('.go') || name.endsWith('_test.go')) continue;
		let inBlock = false;
		for (const line of readFileSync(join(dir, name), 'utf8').split('\n')) {
			if (inBlock) {
				if (line === ')') inBlock = false;
				else for (const m of line.matchAll(/^\t([a-zA-Z_][A-Za-z0-9_]*)(?:,\s*([a-zA-Z_][A-Za-z0-9_]*))*\s+\S/g))
					for (const g of m.slice(1)) if (g) names.add(g);
				continue;
			}
			if (line === 'var (') inBlock = true;
			const single = /^var ([a-zA-Z_][A-Za-z0-9_]*)\b/.exec(line);
			if (single) names.add(single[1]);
		}
	}
	return names;
}

for (const dir of dirs) {
	const names = [...globalNames(dir)].sort();
	console.log(`${dir}: ${names.length} package variables`);
	for (const name of names) {
		const out = execFileSync('node', ['scripts/serialize-tests.mjs', `${name} = `, dir], { encoding: 'utf8' });
		for (const line of out.split('\n')) if (line && !line.includes('across 0 file')) console.log(`  ${name}: ${line}`);
	}
}
