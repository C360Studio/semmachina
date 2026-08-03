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
	SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze'
};

const loadHandler =
	async () => (_request: IncomingMessage, response: import('node:http').ServerResponse) => {
		response.statusCode = 200;
		response.end();
	};

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
});
