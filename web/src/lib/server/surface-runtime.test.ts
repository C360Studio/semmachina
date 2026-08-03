import { describe, expect, it, vi } from 'vitest';

import type { DeploymentEnvironment } from './deployment-config';
import { assembleSurfaceRuntime } from './surface-runtime';

const environment: DeploymentEnvironment = {
	SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:8080/graphql',
	SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
	SEMMACHINA_WORLD_ORG: 'c360',
	SEMMACHINA_WORLD_NAMESPACE: 'bellweather',
	SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze',
	SEMMACHINA_PUBLIC_ORIGIN: 'https://play.example.test',
	SEMMACHINA_TLS_POSTURE: 'trusted_loopback_proxy',
	SEMMACHINA_CREATOR_CREDENTIAL: 'creator-secret-that-is-long',
	SEMMACHINA_PLAYER_BEARER: 'player-bearer-that-is-distinct',
	SEMMACHINA_PLAYER_WS_URL: 'ws://127.0.0.1:8081/play',
	SEMMACHINA_PLAYER_ID: 'c360.semmachina.bellweather.bellweather-maze.player.detective'
};

const secureHeaders = {
	host: 'play.example.test',
	origin: 'https://play.example.test',
	'x-forwarded-proto': 'https'
};

function cookie(response: Response, name: string): string {
	const header = response.headers.get('set-cookie') ?? '';
	const match = header.match(new RegExp(`${name}=[A-Za-z0-9_-]+`));
	if (match === null) throw new Error(`missing ${name}`);
	return match[0];
}

function attestedHeaders(runtime: ReturnType<typeof assembleSurfaceRuntime>) {
	const raw = {
		rawHeaders: ['Host', 'play.example.test', 'X-Forwarded-Proto', 'https'],
		socket: { remoteAddress: '127.0.0.1' },
		headers: { ...secureHeaders } as Record<string, string | string[] | undefined>
	};
	expect(runtime.attestRawTransport?.(raw)).toBe(true);
	return raw.headers;
}

describe('installed surface runtime', () => {
	it('keeps graph closed for refused browser authority and authorizes world reads only after login', async () => {
		const fetcher = vi.fn<typeof fetch>();
		const runtime = assembleSurfaceRuntime({ environment, fetcher });
		const trustedHeaders = attestedHeaders(runtime);
		const refused = await runtime.handle(
			new Request('https://play.example.test/api/world', {
				headers: {
					...secureHeaders,
					authorization: `Bearer ${environment.SEMMACHINA_PLAYER_BEARER}`
				}
			})
		);
		expect(refused.status).toBe(400);
		expect(fetcher).not.toHaveBeenCalled();

		const preauth = await runtime.handlePreauth?.(
			new Request('https://play.example.test/api/auth/preauth', {
				method: 'POST',
				headers: trustedHeaders as HeadersInit
			})
		);
		expect(preauth?.status).toBe(200);
		const preauthBody = (await preauth?.json()) as { csrf: string };
		const login = await runtime.handleLogin?.(
			new Request('https://play.example.test/api/auth/login', {
				method: 'POST',
				headers: {
					...trustedHeaders,
					cookie: cookie(preauth as Response, '__Host-semmachina_preauth'),
					'content-type': 'application/json'
				},
				body: JSON.stringify({
					credential: environment.SEMMACHINA_CREATOR_CREDENTIAL,
					csrf: preauthBody.csrf
				})
			})
		);
		expect(login?.status).toBe(200);
		const world = runtime.handle(
			new Request('https://play.example.test/api/world', {
				headers: {
					...trustedHeaders,
					cookie: cookie(login as Response, '__Host-semmachina_session')
				}
			})
		);
		await vi.waitFor(() => expect(fetcher).toHaveBeenCalled());
		// The mocked graph never resolves; reaching the one fixed graph dial proves session-to-principal wiring.
		void world;
	});

	it('does not expose player mapping through the runtime object', () => {
		const runtime = assembleSurfaceRuntime({ environment, fetcher: vi.fn() });
		expect(JSON.stringify(runtime)).not.toContain(String(environment.SEMMACHINA_PLAYER_BEARER));
		expect(JSON.stringify(runtime)).not.toContain(
			String(environment.SEMMACHINA_CREATOR_CREDENTIAL)
		);
	});
});
