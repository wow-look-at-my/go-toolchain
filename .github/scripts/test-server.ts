// Starts a loopback HTTP server for a test and resolves with its URL and a close function.

import * as http from "node:http";

export function listen(handler: http.RequestListener): Promise<{ url: string; close: () => void }> {
	const srv = http.createServer(handler);
	return new Promise((resolve) => srv.listen(0, "127.0.0.1", () => {
		const { port } = srv.address() as { port: number };
		resolve({ url: `http://127.0.0.1:${port}`, close: () => srv.close() });
	}));
}
