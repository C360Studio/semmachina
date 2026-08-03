import { describe, expect, it, vi } from 'vitest';
import { inspect } from 'node:util';

import { loadDeploymentConfig, type DeploymentEnvironment } from './deployment-config';
import { isAuthorizedProjectionPrincipal } from './projection-principal';
import { loadSurfaceConfig } from './surface-config';
import { createSurfaceSessionAuthority } from './surface-session';

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
	SEMMACHINA_PLAYER_ID: 'c360.semmachina.bellweather.bellweather-maze.player.detective',
	SEMMACHINA_SESSION_TTL_SECONDS: '60'
};

const secureHeaders = {
	host: 'play.example.test',
	origin: 'https://play.example.test',
	'x-forwarded-proto': 'https',
	'x-semmachina-internal-transport': 'test-attestation'
};

function request(path: string, init: RequestInit = {}) {
	return new Request(`https://play.example.test${path}`, {
		...init,
		headers: { ...secureHeaders, ...init.headers }
	});
}

function cookie(response: Response, name: string): string {
	const header = response.headers.get('set-cookie') ?? '';
	expect(header).toContain(`${name}=`);
	expect(header).toContain('HttpOnly');
	expect(header).toContain('Secure');
	expect(header).toContain('SameSite=Strict');
	expect(header).toContain('Path=/');
	expect(header).not.toContain('Domain=');
	return header.split(';', 1)[0];
}

async function json(response: Response) {
	return (await response.json()) as Record<string, unknown>;
}

function authority(
	now = vi.fn(() => 1_000_000),
	options: { maxPreauthProofs?: number; maxSessions?: number } = {}
) {
	const deployment = loadDeploymentConfig(environment);
	return {
		deployment,
		now,
		authority: createSurfaceSessionAuthority(
			loadSurfaceConfig(environment, deployment),
			deployment,
			{
				now,
				isTransportAttested: (request) =>
					request.headers.get('x-semmachina-internal-transport') === 'test-attestation',
				...options
			}
		)
	};
}

async function preauthorize(setup: ReturnType<typeof authority>) {
	const response = await setup.authority.handlePreauth(
		request('/api/auth/preauth', { method: 'POST' })
	);
	return {
		response,
		cookie: cookie(response, '__Host-semmachina_preauth'),
		csrf: (await json(response)).csrf
	};
}

async function login() {
	const setup = authority();
	const preauth = await preauthorize(setup);
	const response = await setup.authority.handleLogin(
		request('/api/auth/login', {
			method: 'POST',
			headers: { cookie: preauth.cookie, 'content-type': 'application/json' },
			body: JSON.stringify({
				credential: environment.SEMMACHINA_CREATOR_CREDENTIAL,
				csrf: preauth.csrf
			})
		})
	);
	return { ...setup, response, sessionCookie: cookie(response, '__Host-semmachina_session') };
}

function upgradeRequest(
	sessionCookie: string,
	csrf: string,
	path = '/api/player',
	headers: Record<string, string> = {}
) {
	return request(path, {
		headers: {
			cookie: sessionCookie,
			connection: 'Upgrade',
			upgrade: 'websocket',
			'sec-websocket-protocol': `semmachina.player.v1, csrf.${csrf}`,
			...headers
		}
	});
}

describe('surface session authority', () => {
	it('mints a preauth proof and rotates it into a bounded authenticated session', async () => {
		const { response, sessionCookie } = await login();
		expect(response.status).toBe(200);
		const body = await json(response);
		expect(body).toEqual({ csrf: expect.stringMatching(/^[A-Za-z0-9_-]+$/) });
		expect(sessionCookie).not.toContain(String(environment.SEMMACHINA_CREATOR_CREDENTIAL));
		expect(JSON.stringify(body)).not.toContain(String(environment.SEMMACHINA_PLAYER_BEARER));
	});

	it.each([
		['wrong Host', { host: 'evil.example.test' }],
		['wrong Origin', { origin: 'https://evil.example.test' }],
		['spoofed transport attestation', { 'x-semmachina-internal-transport': 'attacker' }],
		['missing forwarded proto', { 'x-forwarded-proto': '' }],
		['multi-valued forwarded proto', { 'x-forwarded-proto': 'https, http' }]
	])('refuses preauth with %s', async (_name, headers) => {
		const setup = authority();
		const response = await setup.authority.handlePreauth(
			request('/api/auth/preauth', { method: 'POST', headers })
		);
		expect(response.status).toBe(401);
		expect(response.headers.get('set-cookie')).toBeNull();
	});

	it('gives missing, invalid, and upstream Bearer credentials the same refusal', async () => {
		const setup = authority();
		const attempts = [undefined, 'not-the-credential', environment.SEMMACHINA_PLAYER_BEARER];
		const responses = [];
		for (const credential of attempts) {
			const preauth = await preauthorize(setup);
			responses.push(
				await setup.authority.handleLogin(
					request('/api/auth/login', {
						method: 'POST',
						headers: { cookie: preauth.cookie, 'content-type': 'application/json' },
						body: JSON.stringify({ credential, csrf: preauth.csrf })
					})
				)
			);
		}
		expect(responses.map((response) => response.status)).toEqual([401, 401, 401]);
		expect(await Promise.all(responses.map(json))).toEqual([
			{ error: { code: 'unauthorized' } },
			{ error: { code: 'unauthorized' } },
			{ error: { code: 'unauthorized' } }
		]);
		expect(responses.every((response) => response.headers.get('set-cookie') === null)).toBe(true);
	});

	it('consumes a preauth proof after one credential attempt', async () => {
		const setup = authority();
		const proof = await preauthorize(setup);
		const attempt = (credential: string) =>
			setup.authority.handleLogin(
				request('/api/auth/login', {
					method: 'POST',
					headers: { cookie: proof.cookie, 'content-type': 'application/json' },
					body: JSON.stringify({ credential, csrf: proof.csrf })
				})
			);
		expect((await attempt('wrong-credential-value')).status).toBe(401);
		expect((await attempt(String(environment.SEMMACHINA_CREATOR_CREDENTIAL))).status).toBe(401);
	});

	it('authorizes projection and an exact WebSocket header pair from one immutable mapping', async () => {
		const { authority, deployment, response, sessionCookie } = await login();
		const csrf = (await json(response)).csrf as string;
		const authenticated = request('/api/world', { headers: { cookie: sessionCookie } });
		const principal = authority.authorizeProjection(authenticated);
		expect(isAuthorizedProjectionPrincipal(principal, deployment)).toBe(true);

		const upgraded = authority.authorizeUpgrade(
			request('/api/player', {
				headers: {
					cookie: sessionCookie,
					connection: 'keep-alive, Upgrade',
					upgrade: 'websocket',
					'sec-websocket-protocol': `semmachina.player.v1, csrf.${csrf}`
				}
			})
		);
		expect(upgraded?.playerId).toBe(environment.SEMMACHINA_PLAYER_ID);
		expect(upgraded?.playerBearer).toBe(environment.SEMMACHINA_PLAYER_BEARER);
		expect(upgraded?.playerWsUrl).toBe(environment.SEMMACHINA_PLAYER_WS_URL);
		expect(upgraded?.protocol).toBe('semmachina.player.v1');
		expect(Object.isFrozen(upgraded)).toBe(true);
		expect(Object.keys(upgraded ?? {})).not.toContain('playerBearer');
		expect(JSON.stringify(upgraded)).not.toContain(String(environment.SEMMACHINA_PLAYER_BEARER));
		expect(inspect(upgraded, { showHidden: true })).not.toContain(
			String(environment.SEMMACHINA_PLAYER_BEARER)
		);
	});

	it('allows a same-origin projection GET without an Origin header', async () => {
		const { authority, sessionCookie } = await login();
		const authenticated = new Request('https://play.example.test/api/world', {
			headers: {
				host: 'play.example.test',
				'x-forwarded-proto': 'https',
				'x-semmachina-internal-transport': 'test-attestation',
				cookie: sessionCookie
			}
		});
		expect(authority.authorizeProjection(authenticated)).not.toBeNull();
	});

	it('accepts normal optional whitespace around the two ordered protocol tokens', async () => {
		const { authority, response, sessionCookie } = await login();
		const csrf = (await json(response)).csrf as string;
		expect(
			authority.authorizeUpgrade(
				request('/api/player', {
					headers: {
						cookie: sessionCookie,
						connection: 'Upgrade',
						upgrade: 'websocket',
						'sec-websocket-protocol': `semmachina.player.v1,\tcsrf.${csrf}`
					}
				})
			)
		).not.toBeNull();
	});

	it('refuses non-exact WebSocket protocol pairs even when the CSRF proof is valid', async () => {
		const { authority, response, sessionCookie } = await login();
		const csrf = (await json(response)).csrf as string;
		const protocols = [
			'semmachina.player.v1',
			`csrf.${csrf}, semmachina.player.v1`,
			`semmachina.player.v1, csrf.${csrf}, extra`,
			`other.protocol, csrf.${csrf}`
		];
		for (const protocol of protocols) {
			expect(
				authority.authorizeUpgrade(
					request('/api/player', {
						headers: {
							cookie: sessionCookie,
							connection: 'Upgrade',
							upgrade: 'websocket',
							'sec-websocket-protocol': protocol
						}
					})
				)
			).toBeNull();
		}
	});

	it('invalidates rotation, logout, and expiry for projection and upgrade authorization', async () => {
		const { authority, response, sessionCookie } = await login();
		const csrf = (await json(response)).csrf as string;
		const exactUpgrade = upgradeRequest(sessionCookie, csrf);
		expect(authority.authorizeUpgrade(exactUpgrade)).not.toBeNull();
		const oldRequest = request('/api/world', { headers: { cookie: sessionCookie } });
		const logout = await authority.handleLogout(
			request('/api/auth/logout', {
				method: 'POST',
				headers: { cookie: sessionCookie, 'x-csrf-token': csrf }
			})
		);
		expect(logout.status).toBe(204);
		expect(logout.headers.get('set-cookie')).toContain('Max-Age=0');
		expect(authority.authorizeProjection(oldRequest)).toBeNull();
		expect(authority.authorizeUpgrade(exactUpgrade)).toBeNull();

		const fresh = await login();
		const freshCsrf = (await json(fresh.response)).csrf as string;
		const freshUpgrade = upgradeRequest(fresh.sessionCookie, freshCsrf);
		expect(fresh.authority.authorizeUpgrade(freshUpgrade)).not.toBeNull();
		fresh.now.mockReturnValue(1_061_000);
		expect(
			fresh.authority.authorizeProjection(
				request('/api/world', { headers: { cookie: fresh.sessionCookie } })
			)
		).toBeNull();
		expect(fresh.authority.authorizeUpgrade(freshUpgrade)).toBeNull();
	});

	it('rotates an existing live session in the same authority', async () => {
		const first = await login();
		const firstCsrf = (await json(first.response)).csrf as string;
		const firstUpgrade = upgradeRequest(first.sessionCookie, firstCsrf);
		expect(first.authority.authorizeUpgrade(firstUpgrade)).not.toBeNull();
		const nextPreauth = await preauthorize(first);
		const rotated = await first.authority.handleLogin(
			request('/api/auth/login', {
				method: 'POST',
				headers: {
					cookie: `${nextPreauth.cookie}; ${first.sessionCookie}`,
					'content-type': 'application/json'
				},
				body: JSON.stringify({
					credential: environment.SEMMACHINA_CREATOR_CREDENTIAL,
					csrf: nextPreauth.csrf
				})
			})
		);
		const rotatedCookie = cookie(rotated, '__Host-semmachina_session');
		expect(rotatedCookie).not.toBe(first.sessionCookie);
		expect(
			first.authority.authorizeProjection(
				request('/api/world', { headers: { cookie: first.sessionCookie } })
			)
		).toBeNull();
		expect(first.authority.authorizeUpgrade(firstUpgrade)).toBeNull();
		expect(
			first.authority.authorizeProjection(
				request('/api/world', { headers: { cookie: rotatedCookie } })
			)
		).not.toBeNull();
		const rotatedCsrf = (await json(rotated)).csrf as string;
		expect(
			first.authority.authorizeUpgrade(upgradeRequest(rotatedCookie, rotatedCsrf))
		).not.toBeNull();
	});

	it('refuses every mismatched upgrade authority dimension', async () => {
		const { authority, response, sessionCookie } = await login();
		const csrf = (await json(response)).csrf as string;
		const mismatches = [
			upgradeRequest(sessionCookie, csrf, '/api/player', { host: 'evil.example.test' }),
			upgradeRequest(sessionCookie, csrf, '/api/player', {
				origin: 'https://evil.example.test'
			}),
			upgradeRequest('__Host-semmachina_session=invalid', csrf),
			upgradeRequest(sessionCookie, csrf, '/play'),
			upgradeRequest(sessionCookie, csrf, '/api/player?player=other'),
			upgradeRequest(sessionCookie, csrf, '/api/player', { connection: 'keep-alive' }),
			upgradeRequest(sessionCookie, csrf, '/api/player', { upgrade: 'h2c' }),
			upgradeRequest(sessionCookie, csrf, '/api/player', {
				'sec-websocket-protocol': `other, csrf.${csrf}`
			}),
			upgradeRequest(sessionCookie, csrf, '/api/player', {
				'x-semmachina-internal-transport': 'attacker'
			})
		];
		for (const mismatch of mismatches) {
			expect(authority.authorizeUpgrade(mismatch)).toBeNull();
		}
	});

	it('refuses duplicate cookies and oversized login bodies', async () => {
		const setup = authority();
		const proof = await preauthorize(setup);
		const duplicate = await setup.authority.handleLogin(
			request('/api/auth/login', {
				method: 'POST',
				headers: {
					cookie: `${proof.cookie}; ${proof.cookie}`,
					'content-type': 'application/json'
				},
				body: JSON.stringify({
					credential: environment.SEMMACHINA_CREATOR_CREDENTIAL,
					csrf: proof.csrf
				})
			})
		);
		expect(duplicate.status).toBe(401);
		const oversized = await setup.authority.handleLogin(
			request('/api/auth/login', {
				method: 'POST',
				headers: { cookie: proof.cookie, 'content-type': 'application/json' },
				body: JSON.stringify({ credential: 'x'.repeat(9000), csrf: proof.csrf })
			})
		);
		expect(oversized.status).toBe(401);
	});

	it('cancels an unknown-length login stream as soon as it exceeds the body bound', async () => {
		const setup = authority();
		const proof = await preauthorize(setup);
		const cancel = vi.fn();
		let sent = false;
		const body = new ReadableStream<Uint8Array>({
			pull(controller) {
				if (!sent) {
					sent = true;
					controller.enqueue(new Uint8Array(8193));
				} else {
					controller.close();
				}
			},
			cancel
		});
		const streamedRequest = new Request('https://play.example.test/api/auth/login', {
			method: 'POST',
			headers: {
				...secureHeaders,
				cookie: proof.cookie,
				'content-type': 'application/json'
			},
			body,
			duplex: 'half'
		} as RequestInit & { duplex: 'half' });
		const response = await setup.authority.handleLogin(streamedRequest);
		expect(response.status).toBe(401);
		expect(cancel).toHaveBeenCalledOnce();
	});

	it('bounds the public preauth store and reclaims expired proofs', async () => {
		const now = vi.fn(() => 1_000_000);
		const setup = authority(now, { maxPreauthProofs: 1 });
		expect((await preauthorize(setup)).response.status).toBe(200);
		const full = await setup.authority.handlePreauth(
			request('/api/auth/preauth', { method: 'POST' })
		);
		expect(full.status).toBe(503);
		now.mockReturnValue(1_060_001);
		expect((await preauthorize(setup)).response.status).toBe(200);
	});

	it('expires preauth proofs and bounds live authenticated sessions', async () => {
		const now = vi.fn(() => 1_000_000);
		const setup = authority(now, { maxSessions: 1 });
		const expiredProof = await preauthorize(setup);
		now.mockReturnValue(1_060_001);
		const expiredLogin = await setup.authority.handleLogin(
			request('/api/auth/login', {
				method: 'POST',
				headers: { cookie: expiredProof.cookie, 'content-type': 'application/json' },
				body: JSON.stringify({
					credential: environment.SEMMACHINA_CREATOR_CREDENTIAL,
					csrf: expiredProof.csrf
				})
			})
		);
		expect(expiredLogin.status).toBe(401);

		const firstProof = await preauthorize(setup);
		const firstLogin = await setup.authority.handleLogin(
			request('/api/auth/login', {
				method: 'POST',
				headers: { cookie: firstProof.cookie, 'content-type': 'application/json' },
				body: JSON.stringify({
					credential: environment.SEMMACHINA_CREATOR_CREDENTIAL,
					csrf: firstProof.csrf
				})
			})
		);
		expect(firstLogin.status).toBe(200);
		const secondProof = await preauthorize(setup);
		const secondLogin = await setup.authority.handleLogin(
			request('/api/auth/login', {
				method: 'POST',
				headers: { cookie: secondProof.cookie, 'content-type': 'application/json' },
				body: JSON.stringify({
					credential: environment.SEMMACHINA_CREATOR_CREDENTIAL,
					csrf: secondProof.csrf
				})
			})
		);
		expect(secondLogin.status).toBe(503);
	});
});
