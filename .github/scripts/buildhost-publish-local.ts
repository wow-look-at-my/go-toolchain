// Local-files port of wow-look-at-my/buildhost's buildhost-publish action
// (.github/actions/buildhost-publish/action.yml @ master).
//
// That action is artifacts-API-only: it downloads the current run's `go-build`
// artifact zip via the Actions API (needing `actions: read`) and publishes the
// unzipped files. This port keeps the scan / project-grouping / create-release
// / upload / publish / retry / OIDC logic identical, but sources the files
// from a LOCAL directory instead -- no GitHub Actions artifact, no Actions
// API, and no `actions: read` permission involved.
//
// Runs via wow-look-at-my/actions@typescript#latest (`file:` input; `core`,
// `fs`, `path`, `github`, `env` are injected -- no imports). Call sites:
//   - .github/workflows/ci.yml `publish` job (after restoring the run-keyed
//     build-output cache into build/)
//   - action.yml "Auto-release to buildhost" step (publishing the build/ the
//     action just produced in the consumer's workspace)
//
// Configuration (env vars):
//   BUILDHOST_PUBLISH_DIR  (required) directory containing the matrix
//                          artifacts, named <binary>_<os>_<arch>[.exe];
//                          resolved against GITHUB_WORKSPACE
//   BUILDHOST_SERVER       buildhost server URL (default https://pazer.build)
//   BUILDHOST_PROJECT      project name (default: the repository name)
//   BUILDHOST_VERSION      explicit release version (default: auto-versioned)
//   BUILDHOST_GIT_BRANCH   git branch (default: GITHUB_REF_NAME)
//   BUILDHOST_GIT_COMMIT   git commit SHA (default: GITHUB_SHA)

await (async () => {
  const inputProject: string = process.env.BUILDHOST_PROJECT ?? "";
  const inputVersion: string = process.env.BUILDHOST_VERSION ?? "";
  const inputBranch: string = process.env.BUILDHOST_GIT_BRANCH ?? "";
  const inputCommit: string = process.env.BUILDHOST_GIT_COMMIT ?? "";
  const inputDir: string = process.env.BUILDHOST_PUBLISH_DIR ?? "";

  if (inputDir.length === 0) {
    core.setFailed("BUILDHOST_PUBLISH_DIR is not set -- pass the directory holding the matrix artifacts to publish");
    return;
  }

  const server = process.env.BUILDHOST_SERVER ?? "https://pazer.build";
  const project = inputProject.length > 0 ? inputProject : github.repository.split("/").pop()!;
  const version = inputVersion;
  const gitBranch = inputBranch.length > 0 ? inputBranch : github.ref_name;
  const gitCommit = inputCommit.length > 0 ? inputCommit : github.sha;

  const publishDir = path.resolve(process.env.GITHUB_WORKSPACE ?? process.cwd(), inputDir);
  if (!fs.existsSync(publishDir) || !fs.statSync(publishDir).isDirectory()) {
    core.setFailed(`Publish directory not found: ${publishDir}`);
    return;
  }

  // Mint OIDC token for buildhost
  const idTokenUrl = env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const idTokenBearer = env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  if (!idTokenUrl || !idTokenBearer) {
    core.setFailed("OIDC not available. Add 'permissions: { id-token: write }'.");
    return;
  }
  const idResp = await fetch(`${idTokenUrl}&audience=${server}`, {
    headers: { Authorization: `Bearer ${idTokenBearer}` },
  });
  const token: string = ((await idResp.json()) as { value: string }).value;
  if (!token) { core.setFailed("Failed to obtain OIDC token"); return; }

  // Discover matrix artifacts: files named <binary>_<os>_<arch>[.exe].
  // <binary> is the cmd/ leaf name from go-toolchain and may contain
  // hyphens (e.g. log-streamer-client); os and arch are the trailing
  // two underscore-separated tokens. Unlike the flat artifact unzip dir the
  // upstream action scans, a build/ directory also holds non-binary files
  // (checksums.txt, profile.json, coverage output) and possibly
  // subdirectories -- the symlink/isFile guards and the name filters below
  // skip all of those.
  const artifactRe = /^(.+)_([a-z]+)_([a-z0-9]+)$/;
  const files = fs.readdirSync(publishDir).filter((f) => {
    const full = path.join(publishDir, f);
    if (fs.lstatSync(full).isSymbolicLink()) return false;
    if (!fs.statSync(full).isFile()) return false;
    if (f === "checksums.txt" || f.endsWith(".zip")) return false;
    return artifactRe.test(f.replace(/\.exe$/, ""));
  });

  if (files.length === 0) {
    core.setFailed(`No matrix artifacts matching '<binary>_{os}_{arch}' found in ${publishDir}`);
    core.info(`Files: ${fs.readdirSync(publishDir).join(", ")}`);
    return;
  }

  // Map a built binary to its buildhost project. A binary named exactly
  // after the repo publishes flat as "<repo>"; every other binary nests
  // under "<repo>/<binary>", stripping a redundant leading "<repo>-".
  const projectForBinary = (binary: string): string => {
    if (binary === project) return project;
    if (binary.startsWith(`${project}-`)) return `${project}/${binary.slice(project.length + 1)}`;
    return `${project}/${binary}`;
  };

  // Group artifacts by destination project.
  type Item = { file: string; os: string; arch: string };
  const groups = new Map<string, Item[]>();
  for (const file of files) {
    const m = file.replace(/\.exe$/, "").match(artifactRe)!;
    const [, binary, fileOs, arch] = m;
    const proj = projectForBinary(binary);
    const list = groups.get(proj) ?? [];
    list.push({ file, os: fileOs, arch });
    groups.set(proj, list);
  }
  core.info(`Found ${files.length} artifact(s) across ${groups.size} project(s): ${[...groups.keys()].join(", ")}`);

  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

  // Buildhost runs behind a CDN/reverse proxy and is redeployed with
  // rolling updates. During a deploy window the edge can briefly emit a
  // transient 5xx, a connection error, or an empty-body 404 (an
  // origin/proxy 404 -- distinct from the app's own 404, which always
  // carries a JSON body). Retry those with backoff so a buildhost deploy
  // never hard-fails a publish. Any 4xx that carries a body (including a
  // genuine "project not found") returns at once for the caller to act on.
  const isTransient = (status: number, text: string): boolean =>
    status >= 500 || (status === 404 && text.trim() === "");

  const api = async (method: string, urlPath: string, body?: string | Buffer) => {
    const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
    if (typeof body === "string") headers["Content-Type"] = "application/json";
    else if (body) headers["Content-Type"] = "application/octet-stream";
    const maxAttempts = 5;
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        const resp = await fetch(`${server}${urlPath}`, { method, headers, body });
        const text = await resp.text();
        if (attempt === maxAttempts || !isTransient(resp.status, text)) {
          return { status: resp.status, text };
        }
        core.warning(`${method} ${urlPath} -> HTTP ${resp.status} (transient; attempt ${attempt}/${maxAttempts}); retrying`);
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        if (attempt === maxAttempts) {
          return { status: 0, text: `request failed after ${maxAttempts} attempts: ${msg}` };
        }
        core.warning(`${method} ${urlPath} -> ${msg} (network error; attempt ${attempt}/${maxAttempts}); retrying`);
      }
      await sleep(1000 * 2 ** (attempt - 1));
    }
    return { status: 0, text: "unreachable" };
  };

  const releaseBody = () => JSON.stringify({
    git_branch: gitBranch, git_commit: gitCommit,
    ...(version.length > 0 ? { version } : {}),
  });

  // Resolve a release version from a create-release response (201 new,
  // 409 existing). Returns null and fails the action on any other status.
  const releaseVersionFrom = (proj: string, resp: { status: number; text: string }): string | null => {
    if (resp.status === 201) return JSON.parse(resp.text).version;
    if (resp.status === 409) {
      if (version.length === 0) { core.setFailed(`Release exists for ${proj}, no explicit version`); return null; }
      return version;
    }
    core.setFailed(`Create release for ${proj} failed (${resp.status}): ${resp.text}`);
    return null;
  };

  // Upload every os/arch artifact for a release, then publish it.
  const uploadAndPublish = async (proj: string, releaseVersion: string, items: { file: string; os: string; arch: string }[]) => {
    for (const { file, os: fileOs, arch } of items) {
      const body = fs.readFileSync(path.join(publishDir, file));
      const r = await api("PUT", `/api/v1/projects/${proj}/releases/${releaseVersion}/artifacts/${fileOs}/${arch}?kind=binary`, body);
      if (r.status !== 201) { core.setFailed(`Upload ${proj} ${fileOs}/${arch} failed (${r.status}): ${r.text}`); return false; }
      core.info(`Uploaded ${proj} ${fileOs}/${arch}`);
    }
    const pub = await api("POST", `/api/v1/projects/${proj}/releases/${releaseVersion}/publish`);
    if (pub.status !== 200) { core.setFailed(`Publish ${proj} failed (${pub.status}): ${pub.text}`); return false; }
    core.info(`Published ${proj}/${releaseVersion} (${items.length} artifacts) to ${server}`);
    return true;
  };

  const groupList = [...groups.entries()];

  // BACKWARD-COMPAT SHIM (transitional — remove in a few weeks, once every
  // target buildhost server authorizes slash-namespaced projects): a server
  // that predates namespace support rejects a namespaced project with
  // 404/403. Probe the first namespaced group up front; if it is rejected,
  // publish the old single-project layout instead (all `<repo>_<os>_<arch>`
  // artifacts flat to `<repo>`), so this action never regresses a repo while
  // the rollout is in flight. The probe's create-release is reused below, so
  // an up-to-date server gets no stray release. Single-binary repos publish
  // flat to `<repo>` either way; up-to-date servers never fall back.
  const created = new Map<string, { status: number; text: string }>();
  const firstNs = groupList.find(([p]) => p.includes("/"));
  if (firstNs) {
    const [proj] = firstNs;
    const probe = await api("POST", `/api/v1/projects/${proj}/releases`, releaseBody());
    if (probe.status === 404 || probe.status === 403) {
      core.warning(`Server rejected namespaced project '${proj}' (HTTP ${probe.status}); falling back to the legacy flat '${project}' layout. Upgrade buildhost to enable per-binary projects.`);
      const re = new RegExp(`^${project}_([a-z]+)_([a-z0-9]+)$`);
      const legacy: { file: string; os: string; arch: string }[] = [];
      for (const file of files) {
        const m = file.replace(/\.exe$/, "").match(re);
        if (m) legacy.push({ file, os: m[1], arch: m[2] });
      }
      if (legacy.length === 0) {
        core.setFailed(`Server does not support namespaced projects, and no artifacts match the legacy '${project}_{os}_{arch}' layout`);
        return;
      }
      const rv = releaseVersionFrom(project, await api("POST", `/api/v1/projects/${project}/releases`, releaseBody()));
      if (rv === null) return;
      core.info(`Release: ${project}/${rv} (legacy flat layout)`);
      await uploadAndPublish(project, rv, legacy);
      return;
    }
    created.set(proj, probe);
  }

  // Namespaced path: publish each <repo>/<binary> project independently.
  for (const [proj, items] of groupList) {
    const createResp = created.get(proj) ?? await api("POST", `/api/v1/projects/${proj}/releases`, releaseBody());
    const rv = releaseVersionFrom(proj, createResp);
    if (rv === null) return;
    core.info(`Release: ${proj}/${rv}`);
    if (!(await uploadAndPublish(proj, rv, items))) return;
  }
})();
