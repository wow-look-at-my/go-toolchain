#!/usr/bin/env node
// Rewrites the save-chdir-restore idiom in a test onto t.Chdir.
//
// The fork's testing package runs tests in parallel unless a test takes the
// serial barrier. t.Chdir takes it; a raw os.Chdir does not, so a test that
// moves the process races every test beside it that reads the working
// directory. t.Chdir also restores on its own, which is what lets the manual
// save and the deferred restore go.
//
// The three lines need not be adjacent and need not be in order, so this works
// per function: one getwd, one chdir and one restore of that saved name become
// a single call, and a function holding more than one of any of them is left
// for a human.
//
// Prefer a root argument where the code under test merely reads a directory
// (see docs/GOMOD.md). Use this where the entry point takes its input from the
// working directory, and only in a suite short enough to run serially.
//
// Usage: node scripts/chdir-to-tchdir.mjs <file>...

import { readFileSync, writeFileSync } from 'node:fs';

const files = process.argv.slice(2);
if (files.length === 0) {
	console.error('usage: chdir-to-tchdir.mjs <file>...');
	process.exit(2);
}

const SAVE = /^[\t ]*(?<name>[A-Za-z_][A-Za-z0-9_]*), (?:_|err) := os\.Getwd\(\)$/;
const SAVE_CHECK = /^[\t ]*require\.(?:NoError|Nil)\([A-Za-z_][A-Za-z0-9_]*, err\)$/;
const ENTER = /^(?<indent>[\t ]*)(?:require\.(?:NoError|Nil)\([A-Za-z_][A-Za-z0-9_]*, os\.Chdir\((?<d1>.*)\)\)|(?:_ = )?os\.Chdir\((?<d2>.*)\))$/;
const restoreOf = (name) =>
	new RegExp(`^[\\t ]*(?:defer (?:_ = )?os\\.Chdir\\(${name}\\)|(?<recv>[A-Za-z_][A-Za-z0-9_]*)\\.Cleanup\\(func\\(\\) \\{ (?:_ = )?os\\.Chdir\\(${name}\\) \\}\\))$`);

// convert rewrites one function body, or returns null when its shape is not the idiom.
function convert(lines) {
	const saves = [];
	for (const [i, line] of lines.entries()) if (SAVE.test(line)) saves.push(i);
	if (saves.length !== 1) return null;
	const save = saves[0];
	const name = lines[save].match(SAVE).groups.name;

	const enters = [];
	const restores = [];
	const RESTORE = restoreOf(name);
	for (const [i, line] of lines.entries()) {
		if (RESTORE.test(line)) {
			restores.push(i);
			continue;
		}
		if (i !== save && ENTER.test(line)) enters.push(i);
	}
	if (enters.length !== 1 || restores.length !== 1) return null;

	const enter = lines[enters[0]].match(ENTER).groups;
	const recv = lines[restores[0]].match(RESTORE).groups.recv || 't';
	const drop = new Set([save, restores[0]]);
	// The check belongs to the getwd it follows, and nothing reads err after.
	if (SAVE_CHECK.test(lines[save + 1] ?? '')) drop.add(save + 1);

	const out = [];
	for (const [i, line] of lines.entries()) {
		if (drop.has(i)) continue;
		out.push(i === enters[0] ? `${enter.indent}${recv}.Chdir(${enter.d1 ?? enter.d2})` : line);
	}
	return out;
}

let touched = 0;
for (const file of files) {
	const before = readFileSync(file, 'utf8');
	// A top-level func opens at column zero and the next one closes the last.
	// A fixture's own func does too, inside a raw string, so the literal is
	// skipped rather than cut in half.
	const lines = before.split('\n');
	const starts = [];
	let inRaw = false;
	for (const [i, line] of lines.entries()) {
		if (!inRaw && /^func /.test(line)) starts.push(i);
		for (const _ of line.matchAll(/`/g)) inRaw = !inRaw;
	}
	const blocks = [];
	let at = 0;
	for (const start of [...starts, lines.length]) {
		if (start > at) blocks.push(lines.slice(at, start));
		at = start;
	}
	let converted = 0;
	const rebuilt = blocks.map((block) => {
		if (!block.some((l) => l.includes('os.Chdir('))) return block.join('\n');
		const out = convert(block);
		if (out === null) return block.join('\n');
		converted++;
		return out.join('\n');
	});
	if (converted === 0) {
		console.log(`${file}: no idiom found`);
		continue;
	}
	let after = rebuilt.join('\n');
	// An import the file no longer spells does not compile.
	if (!/\bos\./.test(after)) after = after.replace(/\n\t"os"\n/, '\n');
	writeFileSync(file, after);
	touched++;
	console.log(`${file}: ${converted} converted`);
}
console.log(`${touched} file(s) rewritten`);
