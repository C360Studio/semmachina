import type { RequestHandler } from './$types';

import { getInstalledWorldRuntime } from '$lib/server/world-runtime-registry';

export const POST: RequestHandler = ({ request }) => {
	const handler = getInstalledWorldRuntime().handleLogin;
	if (handler === undefined) return new Response(null, { status: 503 });
	return handler(request);
};
