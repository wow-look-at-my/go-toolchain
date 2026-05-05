#!/usr/bin/env node
"use strict";
const path = require("node:path");
const fs = require("node:fs");
const { spawnSync } = require("node:child_process");
const { createRequire } = require("node:module");

const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, "..", "package.json"), "utf8"));
const scope = pkg.name.split("/")[0];
const name = pkg.name.split("/")[1];

function resolveBinary() {
  const pkgName = scope + "/" + name + "-" + process.platform + "-" + process.arch;
  const req = createRequire(__filename);
  let pkgJsonPath;
  try { pkgJsonPath = req.resolve(pkgName + "/package.json"); }
  catch (err) {
    throw new Error("Cannot find " + pkgName + ". Platform " +
      process.platform + "/" + process.arch + " may not be supported.");
  }
  const plat = JSON.parse(fs.readFileSync(pkgJsonPath, "utf8"));
  const rel = plat.bin && plat.bin[name];
  if (!rel) throw new Error(pkgName + " missing bin." + name);
  return path.join(path.dirname(pkgJsonPath), rel);
}

const r = spawnSync(resolveBinary(), process.argv.slice(2), { stdio: "inherit", windowsHide: true });
if (r.error) { console.error(r.error.message); process.exit(1); }
process.exit(r.status === null ? 1 : r.status);
