// Behavior tests for release-stamp.ts, run against a fake buildhost and a fake OIDC endpoint.

import { strict as assert } from "node:assert";
import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as http from "node:http";
import * as os from "node:os";
import * as path from "node:path";
import { decode, EmbedMagic, EmbedTrailerSize, encode, projectFor, stampFile } from "./release-stamp.ts";

const script = path.join(import.meta.dirname, "release-stamp.ts");

type Seen = { url: string; body: any }[];

function fakeServer(answer: (url: string) => { status: number; body: unknown }): Promise<{ url: string; seen: Seen; close: () => void }> {
	const seen: Seen = [];
	const srv = http.createServer((req, res) => {
		let raw = "";
		req.on("data", (c) => (raw += c));
		req.on("end", () => {
			if (req.url!.startsWith("/oidc")) {
				res.end(JSON.stringify({ value: "oidc-token" }));
				return;
			}
			seen.push({ url: req.url!, body: raw ? JSON.parse(raw) : null });
			const a = answer(req.url!);
			res.statusCode = a.status;
			res.end(JSON.stringify(a.body));
		});
	});
	return new Promise((resolve) => srv.listen(0, "127.0.0.1", () => {
		const { port } = srv.address() as { port: number };
		resolve({ url: `http://127.0.0.1:${port}`, seen, close: () => srv.close() });
	}));
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

// a gosmopolitan binary keeps its GOSTDEMB record as the last 64 bytes, and a second stamp replaces the first
{
	const record = Buffer.concat([EmbedMagic, Buffer.alloc(EmbedTrailerSize - EmbedMagic.length, 7)]);
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "stamp-embed-"));
	const file = path.join(dir, "go-toolchain");
	fs.writeFileSync(file, Buffer.concat([Buffer.from("APE"), record]));
	stampFile(file, { project: "p", release: "1", revision: "r" });
	stampFile(file, { project: "p", release: "2", revision: "r" });
	const body = fs.readFileSync(file);
	assert.ok(body.subarray(body.length - EmbedTrailerSize).equals(record), "the embed record must stay the last bytes");
	assert.ok(body.subarray(0, 3).equals(Buffer.from("APE")), "the original bytes come first");
	const got = decode(body)!;
	assert.equal(got.stamp.release, "2");
	assert.equal(decode(body.subarray(0, got.start)), null, "the old stamp is gone, not stacked");
}

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

// binaries for several projects fail loud, before any release is created
{
	const srv = await fakeServer(() => ({ status: 201, body: { version: "1" } }));
	const dir = buildDir({ "alpha_linux_amd64": "A", "beta_linux_amd64": "B" });
	const r = await run(dir, srv.url);
	srv.close();
	assert.notEqual(r.code, 0);
	assert.match(r.stderr, /publish to 2 projects/);
	assert.equal(srv.seen.length, 0);
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
