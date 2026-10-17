// Stamps the build directory named on the command line: `node release-stamp-cli.ts <dir>`.

import { stampBuild } from "./release-stamp.ts";

stampBuild(process.argv[2] ?? "").catch((e) => {
	console.error(`release-stamp: ${e instanceof Error ? e.message : String(e)}`);
	process.exit(1);
});
