#!/usr/bin/env -S npx tsx
/**
 * Publish cross-compiled Go binaries as per-platform npm packages.
 *
 * Usage: npm-publish.ts <version> [dist-tag]
 * Env (required): NPM_REGISTRY, NPM_SCOPE, NPM_REPOSITORY_URL
 * Env (optional): NPM_NAME (default: go module basename), NPM_BUILD_DIR (default: build/)
 */

import { spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

type GoOS = "linux" | "darwin" | "windows" | "freebsd" | "openbsd";
type GoArch = "amd64" | "arm64" | "386" | "arm";
type NpmOS = "linux" | "darwin" | "win32" | "freebsd" | "openbsd";
type NpmArch = "x64" | "arm64" | "ia32" | "arm";

const OS_MAP: Record<GoOS, NpmOS> = {
  linux: "linux",
  darwin: "darwin",
  windows: "win32",
  freebsd: "freebsd",
  openbsd: "openbsd",
};
const ARCH_MAP: Record<GoArch, NpmArch> = {
  amd64: "x64",
  arm64: "arm64",
  "386": "ia32",
  arm: "arm",
};

interface Repository {
  type: "git";
  url: string;
}

interface PlatformPackage {
  name: string;
  version: string;
  description: string;
  os: NpmOS[];
  cpu: NpmArch[];
  bin: Record<string, string>;
  files: string[];
  repository: Repository;
}

interface WrapperPackage {
  name: string;
  version: string;
  description: string;
  bin: Record<string, string>;
  optionalDependencies: Record<string, string>;
  files: string[];
  repository: Repository;
}

function require_env(name: string): string {
  const v = process.env[name];
  if (!v) {
    throw new Error(`required env var ${name} is not set`);
  }
  return v;
}

function module_basename(): string {
  const gomod = fs.readFileSync("go.mod", "utf8");
  const match = gomod.match(/^module\s+(\S+)/m);
  if (!match) throw new Error("could not parse module path from go.mod");
  return path.basename(match[1]);
}

function npm_publish(pkg_dir: string, dist_tag: string, registry: string): void {
  const args = ["publish", "--access", "public", "--registry", registry];
  if (dist_tag) args.push("--tag", dist_tag);

  const r = spawnSync("npm", args, { cwd: pkg_dir, stdio: "inherit" });
  if (r.status !== 0) {
    throw new Error(`npm publish in ${pkg_dir} exited with ${r.status}`);
  }
}

function discover_binaries(build_dir: string, name: string) {
  const entries = fs.readdirSync(build_dir, { withFileTypes: true });
  const binaries: Array<{
    path: string;
    npm_os: NpmOS;
    npm_arch: NpmArch;
    exe: string;
  }> = [];

  for (const e of entries) {
    if (!e.isFile()) continue;          // skips directories AND symlinks
    if (e.name.startsWith("checksums")) continue;

    let base = e.name;
    let exe = "";
    if (base.endsWith(".exe")) {
      exe = ".exe";
      base = base.slice(0, -4);
    }

    const parts = base.split("_");
    if (parts.length < 3) continue;
    const goarch = parts[parts.length - 1];
    const goos = parts[parts.length - 2];
    const got_name = parts.slice(0, -2).join("_");
    if (got_name !== name) continue;

    const npm_os = OS_MAP[goos as GoOS];
    const npm_arch = ARCH_MAP[goarch as GoArch];
    if (!npm_os || !npm_arch) continue;

    binaries.push({
      path: path.join(build_dir, e.name),
      npm_os,
      npm_arch,
      exe,
    });
  }

  // Stable order: (npm_os, npm_arch).
  binaries.sort((a, b) => a.npm_os.localeCompare(b.npm_os) || a.npm_arch.localeCompare(b.npm_arch));
  return binaries;
}

function write_json(file: string, data: unknown): void {
  fs.writeFileSync(file, JSON.stringify(data, null, "\t") + "\n");
}

function main(): void {
  const [, , version, dist_tag = ""] = process.argv;
  if (!version) {
    console.error("usage: npm-publish.ts <version> [dist-tag]");
    process.exit(2);
  }

  const registry = require_env("NPM_REGISTRY");
  const scope = require_env("NPM_SCOPE");
  const repository: Repository = { type: "git", url: require_env("NPM_REPOSITORY_URL") };
  const name = process.env.NPM_NAME || module_basename();
  const build_dir = process.env.NPM_BUILD_DIR || "build";

  const binaries = discover_binaries(build_dir, name);
  if (binaries.length === 0) {
    console.error(`ERROR: no binaries found in ${build_dir} matching ${name}_*`);
    process.exit(1);
  }

  const work = fs.mkdtempSync(path.join(os.tmpdir(), "npm-publish-"));
  process.on("exit", () => fs.rmSync(work, { recursive: true, force: true }));

  const optional_deps: Record<string, string> = {};

  for (const b of binaries) {
    const pkg_name = `${scope}/${name}-${b.npm_os}-${b.npm_arch}`;
    const pkg_dir = path.join(work, `${b.npm_os}-${b.npm_arch}`);
    fs.mkdirSync(path.join(pkg_dir, "bin"), { recursive: true });

    const bin_target = path.join(pkg_dir, "bin", `${name}${b.exe}`);
    fs.copyFileSync(b.path, bin_target);
    fs.chmodSync(bin_target, 0o755);

    const pkg: PlatformPackage = {
      name: pkg_name,
      version,
      description: `${name} binary for ${b.npm_os}/${b.npm_arch}`,
      os: [b.npm_os],
      cpu: [b.npm_arch],
      bin: { [name]: `bin/${name}${b.exe}` },
      files: ["bin/"],
      repository,
    };
    write_json(path.join(pkg_dir, "package.json"), pkg);

    optional_deps[pkg_name] = version;
    console.log(`=> Publishing ${pkg_name}@${version}`);
    npm_publish(pkg_dir, dist_tag, registry);
  }

  // Wrapper package: ship the JS shim verbatim from the repo so it isn't
  // generated/templated at publish time.
  const script_dir = path.dirname(__filename);
  const wrapper_dir = path.join(work, "wrapper");
  fs.mkdirSync(path.join(wrapper_dir, "bin"), { recursive: true });

  const wrapper_bin = path.join(wrapper_dir, "bin", `${name}.js`);
  fs.copyFileSync(path.join(script_dir, "npm-wrapper.js"), wrapper_bin);
  fs.chmodSync(wrapper_bin, 0o755);

  const wrapper: WrapperPackage = {
    name: `${scope}/${name}`,
    version,
    description: `${name} wrapper - selects the right binary for the host platform`,
    bin: { [name]: `bin/${name}.js` },
    optionalDependencies: optional_deps,
    files: ["bin/"],
    repository,
  };
  write_json(path.join(wrapper_dir, "package.json"), wrapper);

  console.log(`=> Publishing ${scope}/${name}@${version} (wrapper)`);
  npm_publish(wrapper_dir, dist_tag, registry);

  console.log(`=> Done: published ${binaries.length + 1} packages at ${version}`);
}

main();
