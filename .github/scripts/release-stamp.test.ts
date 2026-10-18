// Behavior tests for release-stamp.ts, run against a fake buildhost and a fake OIDC endpoint.

import { strict as assert } from "node:assert";
import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { decode, encode, projectFor, repairFile, stampFile } from "./release-stamp.ts";
import { listen } from "./test-server.ts";

const script = path.join(import.meta.dirname, "release-stamp-cli.ts");

type Seen = { url: string; body: any }[];

function fakeServer(answer: (url: string, body: any) => { status: number; body: unknown }): Promise<{ url: string; seen: Seen; close: () => void }> {
	const seen: Seen = [];
	return listen((req, res) => {
		let raw = "";
		req.on("data", (c) => (raw += c));
		req.on("end", () => {
			if (req.url!.startsWith("/oidc")) {
				res.end(JSON.stringify({ value: "oidc-token" }));
				return;
			}
			const body = raw ? JSON.parse(raw) : null;
			seen.push({ url: req.url!, body });
			const a = answer(req.url!, body);
			res.statusCode = a.status;
			res.end(JSON.stringify(a.body));
		});
	}).then((s) => ({ ...s, seen }));
}

function run(dir: string, server: string, extra: Record<string, string> = {}): Promise<{ code: number; stdout: string; stderr: string; output: string }> {
	const out = path.join(dir, "..", "github-output");
	fs.writeFileSync(out, "");
	const env = {
		PATH: process.env.PATH ?? "", GITHUB_REPOSITORY: "wow-look-at-my/webhooks", GITHUB_SHA: "abc123", GITHUB_REF_NAME: "master",
		BUILDHOST_SERVER: server, ACTIONS_ID_TOKEN_REQUEST_URL: `${server}/oidc?x=1`, ACTIONS_ID_TOKEN_REQUEST_TOKEN: "req", GITHUB_OUTPUT: out, ...extra,
	};
	return new Promise((resolve) => {
		const p = spawn(process.execPath, [script, dir], { env });
		let stdout = "", stderr = "";
		p.stdout.on("data", (c) => (stdout += c));
		p.stderr.on("data", (c) => (stderr += c));
		p.on("close", (code) => resolve({ code: code ?? -1, stdout, stderr, output: fs.readFileSync(out, "utf8") }));
	});
}

function buildDir(files: Record<string, string | Buffer>): string {
	const dir = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "release-stamp-")), "build");
	fs.mkdirSync(dir);
	for (const [name, body] of Object.entries(files)) fs.writeFileSync(path.join(dir, name), body, { mode: 0o755 });
	return dir;
}

assert.equal(projectFor("webhooks", "webhooks"), "webhooks");
assert.equal(projectFor("webhooks", "webhook-runner"), "webhooks/webhook-runner");
assert.equal(projectFor("webhooks", "webhooks-cli"), "webhooks/cli");
assert.equal(projectFor("VictoriaLogs-compat-layer", "VictoriaLogs-compat-layer"), "victorialogs-compat-layer");
assert.equal(projectFor("VictoriaLogs-compat-layer", "victorialogs-compat-layer-cli"), "victorialogs-compat-layer/cli");

// encode then decode round-trips, and bytes with no stamp decode to null
assert.deepEqual(decode(Buffer.concat([Buffer.from("x"), encode({ project: "p", release: "1", revision: "r" })]))!.stamp, { project: "p", release: "1", revision: "r" });
assert.equal(decode(Buffer.from("plain")), null);

// a binary not named after the repo gets its release created in ITS project, is stamped in place, and the checksums follow
{
	const srv = await fakeServer(() => ({ status: 201, body: { version: "347" } }));
	const dir = buildDir({
		"webhook-runner_linux_amd64": "ELF-A",
		"webhook-runner_darwin_arm64": "ELF-B",
		"checksums.txt": "00  webhook-runner_darwin_arm64\n00  webhook-runner_linux_amd64\n",
	});
	const r = await run(dir, srv.url, { CREATE_SERVICE: "true", APT_DEPENDS: "libc6" });
	srv.close();
	assert.equal(r.code, 0, r.stderr);
	assert.equal(srv.seen.length, 1);
	assert.equal(srv.seen[0].url, "/api/v1/projects/webhooks/webhook-runner/releases");
	assert.deepEqual(srv.seen[0].body, { git_branch: "master", git_commit: "abc123", create_service: true, apt_depends: "libc6" });
	for (const [name, orig] of [["webhook-runner_linux_amd64", "ELF-A"], ["webhook-runner_darwin_arm64", "ELF-B"]]) {
		const body = fs.readFileSync(path.join(dir, name));
		assert.ok(body.subarray(0, orig.length).equals(Buffer.from(orig)), `${name}: the original bytes come first`);
		assert.deepEqual(decode(body)!.stamp, { project: "webhooks/webhook-runner", release: "347", revision: "abc123" });
	}
	assert.ok(!fs.readFileSync(path.join(dir, "checksums.txt"), "utf8").includes("00  "), "checksums.txt must carry the stamped hashes");
	assert.equal(r.output, "version=347\n");
}

// an explicit version that exists already is used as it is, and a second stamp replaces the first
{
	const srv = await fakeServer(() => ({ status: 409, body: { error: "exists" } }));
	const dir = buildDir({ "webhooks_linux_amd64": Buffer.concat([Buffer.from("ELF"), encode({ project: "webhooks", release: "1", revision: "old" })]) });
	const r = await run(dir, srv.url, { RELEASE_VERSION: "9" });
	srv.close();
	assert.equal(r.code, 0, r.stderr);
	assert.equal(srv.seen[0].body.version, "9");
	const body = fs.readFileSync(path.join(dir, "webhooks_linux_amd64"));
	assert.equal(decode(body)!.stamp.release, "9");
	assert.equal(decode(body.subarray(0, decode(body)!.start)), null, "the old stamp is gone, not stacked");
}

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

// a go command that carries its standard library keeps the trailer that finds it as the last bytes. A second stamp still replaces the first
{
	// gosmopolitan's EncodeTrailer: magic, blob offset and size as little-endian uint64, the blob's sha256, padding to many bytes
	const blob = Buffer.from("GOSTDBLB-index-and-archives");
	const program = Buffer.from("APE-PROGRAM");
	const trailer = Buffer.alloc(64);
	trailer.write("GOSTDEMB", 0, "ascii");
	trailer.writeBigUInt64LE(BigInt(program.length), 8);
	trailer.writeBigUInt64LE(BigInt(blob.length), 16);
	trailer.fill(7, 24, 56);
	const orig = Buffer.concat([program, blob, trailer]);
	for (const release of ["6", "7"]) {
		const srv = await fakeServer(() => ({ status: 201, body: { version: release } }));
		const dir = buildDir({ "webhooks_linux_amd64": orig });
		if (release === "7") stampFile(path.join(dir, "webhooks_linux_amd64"), { project: "webhooks", release: "6", revision: "old" });
		const r = await run(dir, srv.url);
		srv.close();
		assert.equal(r.code, 0, r.stderr);
		const body = fs.readFileSync(path.join(dir, "webhooks_linux_amd64"));
		assert.ok(body.subarray(body.length - 64).equals(trailer), "the embedded standard library's trailer must stay the last bytes");
		assert.ok(body.subarray(0, program.length + blob.length).equals(Buffer.concat([program, blob])), "the program and the blob keep their offsets");
		const got = decode(body)!;
		assert.deepEqual(got.stamp, { project: "webhooks", release, revision: "abc123" });
		assert.equal(got.start, program.length + blob.length, "one stamp, right after the blob");
	}
}

// binaries for several projects share one release. The repo's project numbers it, every other project is created at that number, and each binary names its own project
{
	const srv = await fakeServer((_url, body) => ({ status: 201, body: { version: body?.version ?? "12" } }));
	const dir = buildDir({
		"slopfix_linux_amd64": "S",
		"slopfix_darwin_arm64": "S2",
		"probe_linux_amd64": "P",
		"ratchet_linux_amd64": "R",
		"checksums.txt": "00  slopfix_linux_amd64\n00  slopfix_darwin_arm64\n00  probe_linux_amd64\n00  ratchet_linux_amd64\n",
	});
	const r = await run(dir, srv.url, { GITHUB_REPOSITORY: "wow-look-at-my/slopfix", CREATE_SERVICE: "false" });
	srv.close();
	assert.equal(r.code, 0, r.stderr);
	assert.deepEqual(srv.seen.map((s) => s.url), [
		"/api/v1/projects/slopfix/releases",
		"/api/v1/projects/slopfix/probe/releases",
		"/api/v1/projects/slopfix/ratchet/releases",
	]);
	assert.deepEqual(srv.seen[0].body, { git_branch: "master", git_commit: "abc123", create_service: false });
	for (const s of srv.seen.slice(1)) assert.deepEqual(s.body, { git_branch: "master", git_commit: "abc123", version: "12", create_service: false });
	for (const [name, project] of [
		["slopfix_linux_amd64", "slopfix"], ["slopfix_darwin_arm64", "slopfix"],
		["probe_linux_amd64", "slopfix/probe"], ["ratchet_linux_amd64", "slopfix/ratchet"],
	]) {
		assert.deepEqual(decode(fs.readFileSync(path.join(dir, name)))!.stamp, { project, release: "12", revision: "abc123" }, name);
	}
	assert.ok(!fs.readFileSync(path.join(dir, "checksums.txt"), "utf8").includes("00  "), "checksums.txt must carry the stamped hashes");
	assert.equal(r.output, "version=12\n");
}

// with no binary named after the repo, the first project by name numbers the release, and a project that holds that number already keeps it
{
	const srv = await fakeServer((url) => url.includes("/beta/") ? { status: 409, body: { error: "exists" } } : { status: 201, body: { version: "4" } });
	const dir = buildDir({ "beta_linux_amd64": "B", "alpha_linux_amd64": "A" });
	const r = await run(dir, srv.url);
	srv.close();
	assert.equal(r.code, 0, r.stderr);
	assert.deepEqual(srv.seen.map((s) => [s.url, s.body.version]), [
		["/api/v1/projects/webhooks/alpha/releases", undefined],
		["/api/v1/projects/webhooks/beta/releases", "4"],
	]);
	assert.deepEqual(decode(fs.readFileSync(path.join(dir, "beta_linux_amd64")))!.stamp, { project: "webhooks/beta", release: "4", revision: "abc123" });
	assert.equal(r.output, "version=4\n");
}

// a repository named in mixed case still numbers the release in its own lowercased project
{
	const srv = await fakeServer((_url, body) => ({ status: 201, body: { version: body?.version ?? "3" } }));
	const dir = buildDir({ "Zeta_linux_amd64": "Z", "abc_linux_amd64": "A" });
	const r = await run(dir, srv.url, { GITHUB_REPOSITORY: "wow-look-at-my/Zeta" });
	srv.close();
	assert.equal(r.code, 0, r.stderr);
	assert.deepEqual(srv.seen.map((s) => [s.url, s.body.version]), [
		["/api/v1/projects/zeta/releases", undefined],
		["/api/v1/projects/zeta/abc/releases", "3"],
	]);
}

// a refused release in a second project fails the step and stamps nothing
{
	const srv = await fakeServer((url) => url.includes("/beta/") ? { status: 403, body: { error: "no" } } : { status: 201, body: { version: "4" } });
	const dir = buildDir({ "alpha_linux_amd64": "A", "beta_linux_amd64": "B" });
	const r = await run(dir, srv.url);
	srv.close();
	assert.notEqual(r.code, 0);
	assert.match(r.stderr, /webhooks\/beta: HTTP 403/);
	assert.equal(fs.readFileSync(path.join(dir, "alpha_linux_amd64"), "utf8"), "A");
	assert.equal(r.output, "");
}

// a refused release fails the step and stamps nothing
{
	const srv = await fakeServer(() => ({ status: 403, body: { error: "no" } }));
	const dir = buildDir({ "webhooks_linux_amd64": "ELF" });
	const r = await run(dir, srv.url);
	srv.close();
	assert.notEqual(r.code, 0);
	assert.match(r.stderr, /HTTP 403/);
	assert.equal(fs.readFileSync(path.join(dir, "webhooks_linux_amd64"), "utf8"), "ELF");
}

// a portable manifest entry is stamped, and its declared filename names the project
{
	const srv = await fakeServer(() => ({ status: 201, body: { version: "5" } }));
	const dir = buildDir({
		"tool.com": "APE",
		"buildhost-artifacts.json": JSON.stringify({ schema: 1, artifacts: [{ file: "tool.com", filename: "webhook-runner", platforms: ["linux/amd64"] }] }),
	});
	const r = await run(dir, srv.url);
	srv.close();
	assert.equal(r.code, 0, r.stderr);
	assert.equal(srv.seen[0].url, "/api/v1/projects/webhooks/webhook-runner/releases");
	assert.equal(decode(fs.readFileSync(path.join(dir, "tool.com")))!.stamp.release, "5");
	assert.equal(JSON.parse(fs.readFileSync(path.join(dir, "buildhost-artifacts.json"), "utf8")).schema, 1, "the manifest is not stamped");
}

// a stamped ELF still starts and exits 0
{
	const srv = await fakeServer(() => ({ status: 201, body: { version: "2" } }));
	const dir = buildDir({});
	// the host's own true: an ELF on linux, a signed Mach-O on darwin
	const trueBin = ["/usr/bin/true", "/bin/true"].find((p) => fs.existsSync(p));
	assert.ok(trueBin, "no true binary at /usr/bin/true or /bin/true");
	fs.copyFileSync(trueBin, path.join(dir, "webhooks_linux_amd64"));
	fs.chmodSync(path.join(dir, "webhooks_linux_amd64"), 0o755);
	const r = await run(dir, srv.url);
	srv.close();
	assert.equal(r.code, 0, r.stderr);
	const p = spawn(path.join(dir, "webhooks_linux_amd64"));
	const code = await new Promise((res) => p.on("close", res));
	assert.equal(code, 0, "the stamped binary must still run");
}

console.log("release-stamp: all checks passed");
