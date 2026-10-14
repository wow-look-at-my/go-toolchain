// Stamps each binary in the build directory with the buildhost release it will publish as, before the publish uploads it. see docs/RELEASE-STAMP.md

import * as crypto from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";

export const Magic = Buffer.from("BUILDHOST-STAMP1", "ascii");
const manifestName = "buildhost-artifacts.json";
const artifactRe = /^(.+)_([a-z]+)_([a-z0-9]+)$/;

export interface Stamp { project: string; release: string; revision: string }

// The trailer is the JSON payload, its length as a big-endian uint32, then Magic.
export function encode(s: Stamp): Buffer {
	const payload = Buffer.from(JSON.stringify({ project: s.project, release: s.release, revision: s.revision }), "utf8");
	const len = Buffer.alloc(4);
	len.writeUInt32BE(payload.length);
	return Buffer.concat([payload, len, Magic]);
}

// A gosmopolitan binary ends with this 64-byte record. Its runtime finds the embedded GOROOT by it, so it must stay the last bytes.
export const EmbedMagic = Buffer.from("GOSTDEMB", "ascii");
export const EmbedTrailerSize = 64;

export function embedTrailer(buf: Buffer): number {
	if (buf.length < EmbedTrailerSize) return 0;
	return buf.subarray(buf.length - EmbedTrailerSize, buf.length - EmbedTrailerSize + EmbedMagic.length).equals(EmbedMagic) ? EmbedTrailerSize : 0;
}

// The stamp and where it starts, or null when there is none. The stamp ends
// the file, or sits before an embed record.
export function decode(file: Buffer): { stamp: Stamp; start: number } | null {
	const buf = file.subarray(0, file.length - embedTrailer(file));
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

// Stamps the file in place. An old stamp is replaced, not stacked.
export function stampFile(file: string, s: Stamp): void {
	for (const key of ["project", "release", "revision"] as const) {
		if (s[key] === "") throw new Error(`release stamp for ${file}: ${key} is empty`);
	}
	const body = fs.readFileSync(file);
	const embed = body.subarray(body.length - embedTrailer(body));
	const head = body.subarray(0, body.length - embed.length);
	const old = decode(body);
	fs.writeFileSync(file, Buffer.concat([old ? head.subarray(0, old.start) : head, encode(s), embed]));
}

// The binaries buildhost-publish uploads from dir, each with the binary name its project comes from. This mirrors the publish's own selection.
export function binaries(dir: string): { file: string; binary: string }[] {
	const out: { file: string; binary: string }[] = [];
	const portable = new Set<string>();
	const manifest = path.join(dir, manifestName);
	if (fs.existsSync(manifest)) {
		const doc = JSON.parse(fs.readFileSync(manifest, "utf8"));
		for (const raw of doc?.artifacts ?? []) {
			const file = String(raw?.file ?? "");
			portable.add(file);
			if (String(raw?.kind ?? "binary") !== "binary") continue;
			const filename = String(raw?.filename ?? path.basename(file));
			const base = path.basename(file).replace(/\.exe$/, "");
			const m = base.match(artifactRe);
			out.push({ file, binary: filename !== path.basename(file) ? filename : m ? m[1] : base });
		}
	}
	for (const f of fs.readdirSync(dir)) {
		const full = path.join(dir, f);
		if (fs.lstatSync(full).isSymbolicLink() || !fs.statSync(full).isFile()) continue;
		if (f === "checksums.txt" || f === manifestName || f.endsWith(".zip") || portable.has(f)) continue;
		const m = f.replace(/\.exe$/, "").match(artifactRe);
		if (m) out.push({ file: f, binary: m[1] });
	}
	return out;
}

// The project buildhost-publish uploads a binary to.
export function projectFor(repoName: string, binaryName: string): string {
	// buildhost accepts only a lowercase project name, and the action publishes to the lowercased repository.
	const repo = repoName.toLowerCase();
	const binary = binaryName.toLowerCase();
	if (binary === repo) return repo;
	if (binary.startsWith(`${repo}-`)) return `${repo}/${binary.slice(repo.length + 1)}`;
	return `${repo}/${binary}`;
}

// Rewrites every line of checksums.txt whose file the stamp changed.
export function refreshChecksums(dir: string): void {
	const sums = path.join(dir, "checksums.txt");
	if (!fs.existsSync(sums)) return;
	const lines = fs.readFileSync(sums, "utf8").split("\n").filter((l) => l !== "");
	const fresh = lines.map((l) => {
		const name = l.slice(l.indexOf("  ") + 2);
		const hash = crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, name))).digest("hex");
		return `${hash}  ${name}`;
	});
	fs.writeFileSync(sums, fresh.join("\n") + "\n");
}

export interface ReleaseRequest {
	server: string; token: string; project: string;
	branch: string; commit: string; version: string; createService: string; aptDepends: string;
}

// Creates the project's release and answers its number. An explicit version that exists already is used as it is.
export async function createRelease(r: ReleaseRequest): Promise<string> {
	const body: Record<string, unknown> = { git_branch: r.branch, git_commit: r.commit };
	if (r.version !== "") body.version = r.version;
	if (r.createService !== "") body.create_service = r.createService === "true";
	if (r.aptDepends !== "") body.apt_depends = r.aptDepends;
	const url = `${r.server}/api/v1/projects/${r.project}/releases`;
	const attempts = 5;
	for (let attempt = 1; ; attempt++) {
		const resp = await fetch(url, {
			method: "POST",
			headers: { Authorization: `Bearer ${r.token}`, "Content-Type": "application/json" },
			body: JSON.stringify(body),
		});
		const text = await resp.text();
		if (resp.status === 201) {
			const v = JSON.parse(text).version;
			if (typeof v !== "string" || v === "") throw new Error(`creating the release of ${r.project}: the answer names no version: ${text}`);
			return v;
		}
		if (resp.status === 409 && r.version !== "") return r.version;
		if (resp.status < 500 || attempt === attempts) throw new Error(`creating the release of ${r.project}: HTTP ${resp.status}: ${text}`);
		console.error(`creating the release of ${r.project}: HTTP ${resp.status} (attempt ${attempt}/${attempts}); retrying`);
		await new Promise((res) => setTimeout(res, 2000));
	}
}

async function oidcToken(audience: string): Promise<string> {
	const url = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
	const bearer = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
	if (!url || !bearer) throw new Error("no OIDC token: the job needs permissions: { id-token: write }");
	const resp = await fetch(`${url}&audience=${encodeURIComponent(audience)}`, { headers: { Authorization: `Bearer ${bearer}` } });
	const value = ((await resp.json()) as { value?: string }).value;
	if (!value) throw new Error(`no OIDC token: HTTP ${resp.status}`);
	return value;
}

function need(name: string): string {
	const v = process.env[name] ?? "";
	if (v === "") throw new Error(`${name} is unset`);
	return v;
}

// Creates the release, stamps every binary in dir and records the number for the publish. Reads its inputs from the environment.
export async function stampBuild(dir: string): Promise<void> {
	const repo = need("GITHUB_REPOSITORY").split("/").pop()!;
	const bins = binaries(dir);
	if (bins.length === 0) throw new Error(`no binary in ${dir} to stamp`);
	const projects = [...new Set(bins.map((b) => projectFor(repo, b.binary)))];
	if (projects.length !== 1) {
		throw new Error(`the binaries in ${dir} publish to ${projects.length} projects (${projects.join(", ")}), and the publish takes one release number`);
	}
	const [project] = projects;
	const server = need("BUILDHOST_SERVER");
	const commit = need("GITHUB_SHA");
	const release = await createRelease({
		server, token: await oidcToken(server), project,
		branch: need("GITHUB_REF_NAME"), commit,
		version: process.env.RELEASE_VERSION ?? "", createService: process.env.CREATE_SERVICE ?? "", aptDepends: process.env.APT_DEPENDS ?? "",
	});
	for (const b of bins) {
		stampFile(path.join(dir, b.file), { project, release, revision: commit });
		console.log(`stamped ${b.file}: ${project} release ${release} (${commit})`);
	}
	refreshChecksums(dir);
	const out = process.env.GITHUB_OUTPUT;
	if (out) fs.appendFileSync(out, `version=${release}\n`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(import.meta.filename)) {
	stampBuild(process.argv[2] ?? "").catch((e) => {
		console.error(`release-stamp: ${e instanceof Error ? e.message : String(e)}`);
		process.exit(1);
	});
}
