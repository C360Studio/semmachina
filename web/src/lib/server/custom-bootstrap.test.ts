import type { IncomingMessage } from 'node:http';
import type { Socket } from 'node:net';
import { describe, expect, it, vi } from 'vitest';

import type { DeploymentEnvironment } from './deployment-config';
import { startCustomServer } from './custom-bootstrap';
import { getInstalledWorldRuntime, installWorldRuntime } from './world-runtime-registry';

const safeEnvironment: DeploymentEnvironment = {
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

const loadHandler =
	async () => (_request: IncomingMessage, response: import('node:http').ServerResponse) => {
		response.statusCode = 200;
		response.end();
	};

function cookie(response: Response, name: string): string {
	const match = response.headers.get('set-cookie')?.match(new RegExp(`${name}=[A-Za-z0-9_-]+`));
	if (match === undefined || match === null) throw new Error(`missing ${name}`);
	return match[0];
}

async function authenticatedSession(runtime: ReturnType<typeof getInstalledWorldRuntime>) {
	const raw = {
		rawHeaders: ['Host', 'play.example.test', 'X-Forwarded-Proto', 'https'],
		socket: { remoteAddress: '127.0.0.1' },
		headers: {
			host: 'play.example.test',
			origin: 'https://play.example.test',
			'x-forwarded-proto': 'https'
		} as Record<string, string | string[] | undefined>
	};
	expect(runtime.attestRawTransport?.(raw)).toBe(true);
	const trustedHeaders = raw.headers as HeadersInit;
	const preauth = await runtime.handlePreauth?.(
		new Request('https://play.example.test/api/auth/preauth', {
			method: 'POST',
			headers: trustedHeaders
		})
	);
	const preauthBody = (await preauth?.json()) as { csrf: string };
	const login = await runtime.handleLogin?.(
		new Request('https://play.example.test/api/auth/login', {
			method: 'POST',
			headers: {
				...raw.headers,
				cookie: cookie(preauth as Response, '__Host-semmachina_preauth'),
				'content-type': 'application/json'
			} as HeadersInit,
			body: JSON.stringify({
				credential: safeEnvironment.SEMMACHINA_CREATOR_CREDENTIAL,
				csrf: preauthBody.csrf
			})
		})
	);
	const loginBody = (await login?.json()) as { csrf: string };
	return {
		cookie: cookie(login as Response, '__Host-semmachina_session'),
		csrf: loginBody.csrf
	};
}

function upgradeIncoming(session: { cookie: string; csrf: string }, socket: Socket) {
	const pairs = [
		['Host', 'play.example.test'],
		['X-Forwarded-Proto', 'https'],
		['Origin', 'https://play.example.test'],
		['Cookie', session.cookie],
		['Connection', 'Upgrade'],
		['Upgrade', 'websocket'],
		['Sec-WebSocket-Protocol', `semmachina.player.v1, csrf.${session.csrf}`]
	] as const;
	return {
		rawHeaders: pairs.flat(),
		headers: Object.fromEntries(pairs.map(([name, value]) => [name.toLowerCase(), value])),
		socket,
		method: 'GET',
		url: '/api/player'
	} as unknown as IncomingMessage;
}

function socket(): Socket {
	return {
		remoteAddress: '127.0.0.1',
		write: vi.fn(),
		destroy: vi.fn()
	} as unknown as Socket;
}

describe('custom server bootstrap', () => {
	it('fails unsafe configuration before installing or binding a listener', async () => {
		const registry = {};
		const listen = vi.fn();
		await expect(
			startCustomServer({
				environment: { ...safeEnvironment, SEMMACHINA_GRAPHQL_URL: '' },
				registry,
				fetcher: vi.fn(),
				loadHandler,
				listen
			})
		).rejects.toThrow();
		expect(() => getInstalledWorldRuntime(registry)).toThrow();
		expect(listen).not.toHaveBeenCalled();
	});

	it('installs one immutable runtime before listening', async () => {
		const registry = {};
		const listen = vi.fn(async () => {
			expect(getInstalledWorldRuntime(registry)).toBeDefined();
		});
		const result = await startCustomServer({
			environment: safeEnvironment,
			registry,
			fetcher: vi.fn(),
			loadHandler,
			listen
		});
		expect(getInstalledWorldRuntime(registry)).toBe(result.runtime);
		expect(Object.isFrozen(result.runtime)).toBe(true);
		expect(listen).toHaveBeenCalledOnce();
	});

	it('refuses a second process installation before listening', async () => {
		const registry = {};
		const first = Object.freeze({
			deploymentInstance: Object.freeze({}) as never,
			handle: vi.fn()
		});
		installWorldRuntime(first, registry);
		const listen = vi.fn();
		await expect(
			startCustomServer({
				environment: safeEnvironment,
				registry,
				fetcher: vi.fn(),
				loadHandler,
				listen
			})
		).rejects.toThrow();
		expect(getInstalledWorldRuntime(registry)).toBe(first);
		expect(listen).not.toHaveBeenCalled();
	});

	it('shares one runtime identity and adapter across requests while default-denying without a graph dial', async () => {
		const registry = {};
		const fetcher = vi.fn<typeof fetch>();
		const { runtime } = await startCustomServer({
			environment: safeEnvironment,
			registry,
			fetcher,
			loadHandler,
			listen: vi.fn()
		});
		const identity = runtime.deploymentInstance;
		expect((await runtime.handle(new Request('https://surface.test/api/world'))).status).toBe(401);
		expect((await runtime.handle(new Request('https://surface.test/api/world'))).status).toBe(401);
		expect(runtime.deploymentInstance).toBe(identity);
		expect(getInstalledWorldRuntime(registry)).toBe(runtime);
		expect(fetcher).not.toHaveBeenCalled();
	});

	it('explicitly refuses WebSocket upgrades without dialing graph or player upstreams', async () => {
		const fetcher = vi.fn<typeof fetch>();
		const { server } = await startCustomServer({
			environment: safeEnvironment,
			registry: {},
			fetcher,
			loadHandler,
			listen: vi.fn()
		});
		const socket = {
			write: vi.fn(),
			destroy: vi.fn()
		} as unknown as Socket;
		server.emit('upgrade', {} as IncomingMessage, socket, Buffer.alloc(0));
		expect(socket.write).toHaveBeenCalledWith(expect.stringContaining('426 Upgrade Required'));
		expect(socket.destroy).toHaveBeenCalledOnce();
		expect(fetcher).not.toHaveBeenCalled();
	});

	it('pins HTTP trust at raw headers, overwrites spoofed attestation, and rejects duplicates', async () => {
		const adapter = vi.fn(
			(_request: IncomingMessage, response: import('node:http').ServerResponse) => response.end()
		);
		const { server } = await startCustomServer({
			environment: safeEnvironment,
			registry: {},
			fetcher: vi.fn(),
			loadHandler: async () => adapter,
			listen: vi.fn()
		});
		const accepted = {
			rawHeaders: [
				'Host',
				'play.example.test',
				'X-Forwarded-Proto',
				'https',
				'X-SemMachina-Internal-Transport',
				'attacker'
			],
			headers: { 'x-semmachina-internal-transport': 'attacker' },
			socket: { remoteAddress: '127.0.0.1' }
		} as unknown as IncomingMessage;
		server.emit('request', accepted, { end: vi.fn() } as never);
		expect(adapter).toHaveBeenCalledOnce();
		expect(accepted.headers['x-semmachina-internal-transport']).not.toBe('attacker');

		const response = { writeHead: vi.fn(), end: vi.fn() };
		const duplicate = {
			rawHeaders: [
				'Host',
				'play.example.test',
				'Host',
				'evil.example.test',
				'X-Forwarded-Proto',
				'https'
			],
			headers: {},
			socket: { remoteAddress: '127.0.0.1' }
		} as unknown as IncomingMessage;
		server.emit('request', duplicate, response as never);
		expect(adapter).toHaveBeenCalledOnce();
		expect(response.writeHead).toHaveBeenCalledWith(401, expect.any(Object));
	});

	it('invokes the authorized upgrade continuation exactly once with the immutable mapping', async () => {
		const continuation = vi.fn();
		const { server, runtime } = await startCustomServer({
			environment: safeEnvironment,
			registry: {},
			fetcher: vi.fn(),
			loadHandler,
			listen: vi.fn(),
			continueAuthorizedUpgrade: continuation
		});
		const session = await authenticatedSession(runtime);
		const target = socket();
		server.emit('upgrade', upgradeIncoming(session, target), target, Buffer.alloc(0));
		expect(continuation).toHaveBeenCalledOnce();
		const authorization = continuation.mock.calls[0][4];
		expect(Object.isFrozen(authorization)).toBe(true);
		expect(authorization.playerBearer).toBe(safeEnvironment.SEMMACHINA_PLAYER_BEARER);
		expect(target.destroy).not.toHaveBeenCalled();
	});

	it('never invokes the authorized continuation for raw-boundary or session mismatches', async () => {
		const continuation = vi.fn();
		const { server, runtime } = await startCustomServer({
			environment: safeEnvironment,
			registry: {},
			fetcher: vi.fn(),
			loadHandler,
			listen: vi.fn(),
			continueAuthorizedUpgrade: continuation
		});
		const session = await authenticatedSession(runtime);
		const rawSocket = socket();
		const duplicateHost = upgradeIncoming(session, rawSocket);
		duplicateHost.rawHeaders.push('Host', 'evil.example.test');
		server.emit('upgrade', duplicateHost, rawSocket, Buffer.alloc(0));

		const sessionSocket = socket();
		const wrongSession = upgradeIncoming({ ...session, cookie: 'invalid=value' }, sessionSocket);
		server.emit('upgrade', wrongSession, sessionSocket, Buffer.alloc(0));
		expect(continuation).not.toHaveBeenCalled();
		expect(rawSocket.destroy).toHaveBeenCalledOnce();
		expect(sessionSocket.destroy).toHaveBeenCalledOnce();
	});
});
