import type { RequestHandler } from './$types';

import { getInstalledWorldRuntime } from '$lib/server/world-runtime-registry';

export const GET: RequestHandler = ({ request }) => getInstalledWorldRuntime().handle(request);
