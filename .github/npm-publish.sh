#!/usr/bin/env bash
set -euo pipefail

# Publish cross-compiled Go binaries as per-platform npm packages.
#
# Usage: npm-publish.sh <version> [dist-tag]
# Env (required): NPM_REGISTRY, NPM_SCOPE
# Env (optional): NPM_NAME (default: go module basename), NPM_BUILD_DIR (default: build/)

VERSION="${1:?usage: npm-publish.sh <version> [dist-tag]}"
DIST_TAG="${2:-}"
BUILD_DIR="${NPM_BUILD_DIR:-build}"
NAME="${NPM_NAME:-$(sed -n 's/^module //p' go.mod | xargs basename)}"

declare -A OS_MAP=([linux]=linux [darwin]=darwin [windows]=win32 [freebsd]=freebsd [openbsd]=openbsd)
declare -A ARCH_MAP=([amd64]=x64 [arm64]=arm64 [386]=ia32 [arm]=arm)

npm_publish_args=(publish --access public)
if [[ -n "${NPM_REGISTRY:-}" ]]; then
  npm_publish_args+=(--registry "$NPM_REGISTRY")
fi
if [[ -n "$DIST_TAG" ]]; then
  npm_publish_args+=(--tag "$DIST_TAG")
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

platform_deps=""

for bin in "$BUILD_DIR"/${NAME}_*; do
  [[ -f "$bin" ]] || continue
  [[ -L "$bin" ]] && continue

  base=$(basename "$bin")
  exe=""
  if [[ "$base" == *.exe ]]; then
    exe=".exe"
    base="${base%.exe}"
  fi

  # Parse: <name>_<goos>_<goarch>
  goarch="${base##*_}"
  rest="${base%_*}"
  goos="${rest##*_}"

  npm_os="${OS_MAP[$goos]:-}"
  npm_arch="${ARCH_MAP[$goarch]:-}"
  [[ -n "$npm_os" && -n "$npm_arch" ]] || continue

  pkg_name="${NPM_SCOPE}/${NAME}-${npm_os}-${npm_arch}"
  pkg_dir="$WORK/$npm_os-$npm_arch"
  mkdir -p "$pkg_dir/bin"

  cp "$bin" "$pkg_dir/bin/${NAME}${exe}"
  chmod 755 "$pkg_dir/bin/${NAME}${exe}"

  jq -n \
    --arg name "$pkg_name" \
    --arg version "$VERSION" \
    --arg desc "$NAME binary for $npm_os/$npm_arch" \
    --arg os "$npm_os" \
    --arg cpu "$npm_arch" \
    --arg bin_entry "bin/${NAME}${exe}" \
    --arg bin_name "$NAME" \
    '{name: $name, version: $version, description: $desc,
      os: [$os], cpu: [$cpu], bin: {($bin_name): $bin_entry}, files: ["bin/"]}' \
    > "$pkg_dir/package.json"

  platform_deps="$platform_deps $(printf '"%s": "%s"' "$pkg_name" "$VERSION")"

  echo "=> Publishing $pkg_name@$VERSION"
  (cd "$pkg_dir" && npm "${npm_publish_args[@]}")
done

if [[ -z "$platform_deps" ]]; then
  echo "ERROR: no binaries found in $BUILD_DIR matching ${NAME}_*" >&2
  exit 1
fi

# Build the optionalDependencies JSON object from collected pairs.
opt_deps=$(echo "$platform_deps" | xargs -n2 | jq -Rn '[inputs | split(" ") | {(.[0]): .[1]}] | add')

# Wrapper package
wrapper_dir="$WORK/wrapper"
mkdir -p "$wrapper_dir/bin"

cat > "$wrapper_dir/bin/${NAME}.js" << 'SHIM'
#!/usr/bin/env node
"use strict";
const path = require("node:path");
const fs = require("node:fs");
const { spawnSync } = require("node:child_process");
const { createRequire } = require("node:module");
SHIM

cat >> "$wrapper_dir/bin/${NAME}.js" << SHIM
const SCOPE = $(jq -n --arg s "$NPM_SCOPE" '$s');
const NAME = $(jq -n --arg n "$NAME" '$n');
SHIM

cat >> "$wrapper_dir/bin/${NAME}.js" << 'SHIM'
function resolveBinary() {
  const pkgName = SCOPE + "/" + NAME + "-" + process.platform + "-" + process.arch;
  const req = createRequire(__filename);
  let pkgJsonPath;
  try { pkgJsonPath = req.resolve(pkgName + "/package.json"); }
  catch (err) {
    throw new Error("Cannot find " + pkgName + ". Platform " +
      process.platform + "/" + process.arch + " may not be supported.");
  }
  const pkg = JSON.parse(fs.readFileSync(pkgJsonPath, "utf8"));
  const rel = pkg.bin && pkg.bin[NAME];
  if (!rel) throw new Error(pkgName + " missing bin." + NAME);
  return path.join(path.dirname(pkgJsonPath), rel);
}
const r = spawnSync(resolveBinary(), process.argv.slice(2), {stdio:"inherit", windowsHide:true});
if (r.error) { console.error(r.error.message); process.exit(1); }
process.exit(r.status === null ? 1 : r.status);
SHIM

chmod 755 "$wrapper_dir/bin/${NAME}.js"

jq -n \
  --arg name "${NPM_SCOPE}/${NAME}" \
  --arg version "$VERSION" \
  --arg desc "$NAME wrapper - selects the right binary for the host platform" \
  --arg bin_entry "bin/${NAME}.js" \
  --arg bin_name "$NAME" \
  --argjson opt_deps "$opt_deps" \
  '{name: $name, version: $version, description: $desc,
    bin: {($bin_name): $bin_entry}, optionalDependencies: $opt_deps, files: ["bin/"]}' \
  > "$wrapper_dir/package.json"

echo "=> Publishing ${NPM_SCOPE}/${NAME}@$VERSION (wrapper)"
(cd "$wrapper_dir" && npm "${npm_publish_args[@]}")

echo "=> Done: published $(echo "$platform_deps" | wc -w | xargs expr 1 + ) packages at $VERSION"
