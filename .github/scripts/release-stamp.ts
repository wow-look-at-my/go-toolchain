// Reads and repairs the BUILDHOST-STAMP1 trailer that binaries published before the build stamped them natively carry. see docs/RELEASE-STAMP.md.

import * as fs from "node:fs";

export const Magic = Buffer.from("BUILDHOST-STAMP1", "ascii");

export interface Stamp { project: string; release: string; revision: string }

// A go command that carries its standard library finds it through this 64-byte record, which has to stay the last bytes of the file (gosmopolitan's internal/cosmo/embedded).
const embeddedMagic = Buffer.from("GOSTDEMB", "ascii");
const embeddedTrailerSize = 64;

// The embedded standard library's trailer at the end of buf, or an empty buffer when buf carries none.
export function embeddedTrailer(buf: Buffer): Buffer {
	if (buf.length < embeddedTrailerSize) return Buffer.alloc(0);
	const t = buf.subarray(buf.length - embeddedTrailerSize);
	return t.subarray(0, embeddedMagic.length).equals(embeddedMagic) ? t : Buffer.alloc(0);
}

// The stamp at the end of the bytes, before an embedded standard library's trailer, and where it starts, or null when there is none.
// The trailer is the JSON payload, its length as a big-endian uint32, then Magic.
export function decode(whole: Buffer): { stamp: Stamp; start: number } | null {
	const buf = whole.subarray(0, whole.length - embeddedTrailer(whole).length);
	const tail = Magic.length + 4;
	if (buf.length < tail || !buf.subarray(buf.length - Magic.length).equals(Magic)) return null;
	const len = buf.readUInt32BE(buf.length - tail);
	const start = buf.length - tail - len;
	if (start < 0) throw new Error(`release stamp: the trailer claims ${len} payload bytes, more than the file holds`);
	const raw = JSON.parse(buf.subarray(start, start + len).toString("utf8"));
	for (const key of ["project", "release", "revision"] as const) {
		if (typeof raw[key] !== "string" || raw[key] === "") throw new Error(`release stamp: the payload has no ${key}`);
	}
	return { stamp: { project: raw.project, release: raw.release, revision: raw.revision }, start };
}

// Moves a stamp that sits after an embedded standard library's trailer to before it.
// A binary published with the trailer last carries the other order, and its go command finds no standard library.
// Answers whether the file changed.
export function repairFile(file: string): boolean {
	const body = fs.readFileSync(file);
	if (embeddedTrailer(body).length > 0) return false;
	const old = decode(body);
	if (!old) return false;
	const before = body.subarray(0, old.start);
	const embedded = embeddedTrailer(before);
	if (embedded.length === 0) return false;
	fs.writeFileSync(file, Buffer.concat([before.subarray(0, before.length - embedded.length), body.subarray(old.start), embedded]));
	return true;
}


