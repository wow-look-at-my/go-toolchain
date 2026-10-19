// Behavior tests for release-stamp.ts.

import { strict as assert } from "node:assert";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { decode, Magic, repairFile, type Stamp } from "./release-stamp.ts";

function encode(s: Stamp): Buffer {
	const payload = Buffer.from(JSON.stringify(s), "utf8");
	const len = Buffer.alloc(4);
	len.writeUInt32BE(payload.length);
	return Buffer.concat([payload, len, Magic]);
}

function buildDir(files: Record<string, string | Buffer>): string {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "release-stamp-"));
	for (const [name, body] of Object.entries(files)) fs.writeFileSync(path.join(dir, name), body);
	return dir;
}

// a stamp round-trips, and bytes with no stamp decode to null
assert.deepEqual(decode(Buffer.concat([Buffer.from("x"), encode({ project: "p", release: "1", revision: "r" })]))!.stamp, { project: "p", release: "1", revision: "r" });
assert.equal(decode(Buffer.from("plain")), null);

// a trailer that claims more payload than the file holds is an error, not a null
assert.throws(() => decode(Buffer.concat([Buffer.from("x"), Buffer.from([0, 0, 1, 0]), Magic])), /more than the file holds/);

// a binary published with its stamp after the standard library's trailer gets the stamp moved before it. And a binary already in that order is left alone
{
	const program = Buffer.from("APE-PROGRAM-AND-BLOB");
	const trailer = Buffer.alloc(64);
	trailer.write("GOSTDEMB", 0, "ascii");
	trailer.writeBigUInt64LE(3n, 8);
	trailer.writeBigUInt64LE(9n, 16);
	const stamp = encode({ project: "go-toolchain", release: "1176", revision: "abc123" });
	const dir = buildDir({ "broken": Buffer.concat([program, trailer, stamp]), "plain": "ELF" });
	assert.equal(repairFile(path.join(dir, "broken")), true);
	const fixed = fs.readFileSync(path.join(dir, "broken"));
	assert.ok(fixed.equals(Buffer.concat([program, stamp, trailer])), "the stamp moves before the trailer, every other byte stays");
	assert.deepEqual(decode(fixed)!.stamp, { project: "go-toolchain", release: "1176", revision: "abc123" });
	assert.equal(repairFile(path.join(dir, "broken")), false, "a repaired file is left alone");
	assert.ok(fs.readFileSync(path.join(dir, "broken")).equals(fixed));
	assert.equal(repairFile(path.join(dir, "plain")), false, "an unstamped file is left alone");
	assert.equal(fs.readFileSync(path.join(dir, "plain"), "utf8"), "ELF");
}

console.log("release-stamp: all checks passed");
