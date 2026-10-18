// Tests the git credential helper step of action.yml and ci.yml. No network: a loopback server stands in for github.com.

import { deepEqual, equal, match, notEqual, ok } from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { listen } from "./test-server.ts";

const root = join(import.meta.dirname, "..", "..");
const stepName = "Configure git for private repos";
const token = "tok-" + "0123456789abcdef";

function workflowFiles(): string[] {
	const dir = join(root, ".github", "workflows");
	const files = readdirSync(dir).filter((f) => /\.ya?ml$/.test(f)).map((f) => join(dir, f));
	return [join(root, "action.yml"), ...files];
}

// A line that rewrites a URL with insteadOf and carries a token or a userinfo part.
function tokenRewrites(text: string): string[] {
	return text.split("\n").filter((l) => /insteadOf/i.test(l) && /PRIVATE_ORG_REPO_READ|TOKEN|secrets\.|https?:\/\/[^\s"'\/]*@/.test(l));
}

// The run script of every step with the given name.
function stepScripts(text: string): string[] {
	const lines = text.split("\n");
	const found: string[] = [];
	for (let i = 0; i < lines.length; i++) {
		const m = lines[i].match(/^(\s*)- name:\s*(.*)$/);
		if (!m || m[2].trim() !== stepName) continue;
		const stepIndent = m[1].length;
		for (let j = i + 1; j < lines.length; j++) {
			const l = lines[j];
			if (l.trim() !== "" && l.length - l.trimStart().length <= stepIndent) break;
			const r = l.match(/^(\s*)run:\s*(.*)$/);
			if (!r) continue;
			if (r[2] !== "|") {
				found.push(r[2]);
				break;
			}
			const body: string[] = [];
			for (let k = j + 1; k < lines.length; k++) {
				const b = lines[k];
				if (b.trim() !== "" && b.length - b.trimStart().length <= r[1].length) break;
				body.push(b.trim());
			}
			found.push(body.join("\n"));
			break;
		}
	}
	return found;
}

async function test(name: string, fn: () => void | Promise<void>) {
	await fn();
	console.log(`ok - ${name}`);
}

await test("no insteadOf rewrite carries a token in action.yml or the workflows", () => {
	for (const f of workflowFiles()) {
		deepEqual(tokenRewrites(readFileSync(f, "utf8")), [], `${f} rewrites a URL with a token`);
	}
});

await test("the token-rewrite check detects a token in the rewrite", () => {
	const bad = `run: git config --global url."https://\${PRIVATE_ORG_REPO_READ}@github.com/wow-look-at-my/".insteadOf "https://github.com/wow-look-at-my/"`;
	equal(tokenRewrites(bad).length, 1);
	equal(tokenRewrites(`run: git config --global url."https://example.org/".insteadOf "https://github.com/"`).length, 0);
});

await test("action.yml has the credential helper step", () => {
	const scripts = stepScripts(readFileSync(join(root, "action.yml"), "utf8"));
	equal(scripts.length, 1, `want one "${stepName}" step in action.yml`);
	const s = scripts[0];
	match(s, /git config --global credential\.https:\/\/github\.com\.helper '!/);
	match(s, /x-access-token/);
	match(s, /\$PRIVATE_ORG_REPO_READ/);
	ok(!s.includes("${"), "the token must stay a shell variable the helper reads at call time");
});

await test("every ci.yml copy of the step matches action.yml", () => {
	const want = stepScripts(readFileSync(join(root, "action.yml"), "utf8"))[0];
	const ci = stepScripts(readFileSync(join(root, ".github", "workflows", "ci.yml"), "utf8"));
	ok(ci.length >= 1, "ci.yml has no credential helper step");
	for (const s of ci) equal(s, want);
});

// Smart HTTP git server over git-http-backend.
function gitServer(repoRoot: string) {
	const backend = join(execFileSync("git", ["--exec-path"], { encoding: "utf8" }).trim(), "git-http-backend");
	const want = "Basic " + Buffer.from(`x-access-token:${token}`).toString("base64");
	const served: number[] = [];
	return listen((req, res) => {
		if (req.headers.authorization !== want) {
			res.statusCode = 401;
			res.setHeader("WWW-Authenticate", 'Basic realm="git"');
			res.end();
			return;
		}
		const u = new URL(req.url!, "http://x");
		const cgi = spawn(backend, [], {
			env: {
				GIT_PROJECT_ROOT: repoRoot,
				GIT_HTTP_EXPORT_ALL: "1",
				REQUEST_METHOD: req.method!,
				PATH_INFO: u.pathname,
				QUERY_STRING: u.search.slice(1),
				CONTENT_TYPE: req.headers["content-type"] ?? "",
				CONTENT_LENGTH: req.headers["content-length"] ?? "",
				HTTP_GIT_PROTOCOL: String(req.headers["git-protocol"] ?? ""),
				HTTP_CONTENT_ENCODING: String(req.headers["content-encoding"] ?? ""),
				REMOTE_USER: "x-access-token",
				REMOTE_ADDR: "127.0.0.1",
			},
		});
		req.pipe(cgi.stdin);
		const chunks: Buffer[] = [];
		cgi.stdout.on("data", (c) => chunks.push(c));
		cgi.on("close", () => {
			const out = Buffer.concat(chunks);
			const split = out.indexOf("\r\n\r\n");
			for (const line of out.subarray(0, split).toString().split("\r\n")) {
				const i = line.indexOf(":");
				if (i < 0) continue;
				const k = line.slice(0, i);
				const v = line.slice(i + 1).trim();
				if (k.toLowerCase() === "status") res.statusCode = parseInt(v, 10);
				else res.setHeader(k, v);
			}
			served.push(res.statusCode);
			res.end(out.subarray(split + 4));
		});
	}).then((s) => ({ ...s, served }));
}

function sh(cmd: string, cwd: string, env: Record<string, string>): Promise<{ code: number; out: string }> {
	return new Promise((resolve) => {
		const p = spawn("bash", ["-c", cmd], { cwd, env: { PATH: process.env.PATH!, ...env } });
		let out = "";
		p.stdout.on("data", (c) => (out += c));
		p.stderr.on("data", (c) => (out += c));
		p.on("close", (code) => resolve({ code: code ?? -1, out }));
	});
}

await test("the helper authenticates ls-remote against a Basic-auth git server, and nothing else does", async () => {
	const tmp = mkdtempSync(join(tmpdir(), "git-credential-"));
	const bare = join(tmp, "repos", "org", "private.git");
	mkdirSync(bare, { recursive: true });
	const clean = { HOME: tmp, GIT_CONFIG_NOSYSTEM: "1", GIT_TERMINAL_PROMPT: "0" };
	const work = join(tmp, "seed");
	const seed = await sh(
		`git init -q -b main --bare "${bare}" && git init -q -b main "${work}" && cd "${work}" && git -c user.name=t -c user.email=t@t commit -q --allow-empty -m seed && git push -q "${bare}" main`,
		tmp, clean);
	equal(seed.code, 0, seed.out);
	const srv = await gitServer(join(tmp, "repos"));
	try {
		const fresh = mkdtempSync(join(tmp, "fresh-"));
		const globalConfig = join(tmp, "gitconfig");
		const env = { ...clean, GIT_CONFIG_GLOBAL: globalConfig, PRIVATE_ORG_REPO_READ: token };
		const ls = `git ls-remote ${srv.url}/org/private.git HEAD`;

		const before = await sh(ls, fresh, env);
		notEqual(before.code, 0, "ls-remote must fail with no helper configured");

		// The step as shipped, with the host swapped for the loopback server.
		const script = stepScripts(readFileSync(join(root, "action.yml"), "utf8"))[0].replaceAll("https://github.com", srv.url);
		const applied = await sh(script, fresh, env);
		equal(applied.code, 0, applied.out);
		ok(!readFileSync(globalConfig, "utf8").includes(token), "the token must not land in git config");

		const withHelper = await sh(ls, fresh, env);
		equal(withHelper.code, 0, withHelper.out);
		match(withHelper.out, /^[0-9a-f]{40}\tHEAD$/m);
		ok(srv.served.includes(200), "the server must have served a request");

		const wrong = await sh(ls, fresh, { ...env, PRIVATE_ORG_REPO_READ: "wrong" });
		notEqual(wrong.code, 0, "a wrong token must fail");
	} finally {
		srv.close();
	}
});

console.log("git-credential: all checks passed");
