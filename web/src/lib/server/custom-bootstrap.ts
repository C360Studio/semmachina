import {
	createServer as createNodeServer,
	type IncomingMessage,
	type RequestListener,
	type Server
} from 'node:http';
import type { Duplex } from 'node:stream';

import type { DeploymentEnvironment } from './deployment-config';
import { assembleWorldProjectionRuntime } from './world-projection-route';
import { installWorldRuntime, type InstalledWorldRuntime } from './world-runtime-registry';

interface HandlerModule {
	readonly handler: RequestListener;
}

export type UpgradeHandler = (request: IncomingMessage, socket: Duplex, head: Buffer) => void;

export interface BootstrapDependencies {
	readonly environment?: DeploymentEnvironment;
	readonly registry?: object;
	readonly fetcher?: typeof fetch;
	readonly loadHandler?: () => Promise<RequestListener>;
	readonly createServer?: (handler: RequestListener) => Server;
	readonly listen?: (server: Server, port: number, host: string) => Promise<void>;
	readonly assembleRuntime?: () => InstalledWorldRuntime;
	readonly handleUpgrade?: UpgradeHandler;
}

function listenAddress(environment: DeploymentEnvironment): { host: string; port: number } {
	const host = environment.HOST ?? '127.0.0.1';
	if (host === '' || /[\s/]/.test(host)) throw new Error('invalid HOST');
	const rawPort = environment.PORT ?? '3000';
	if (!/^\d+$/.test(rawPort)) throw new Error('invalid PORT');
	const port = Number(rawPort);
	if (port < 1 || port > 65_535) throw new Error('PORT outside supported range');
	return { host, port };
}

async function loadGeneratedHandler(): Promise<RequestListener> {
	const moduleUrl = new URL('../build/handler.js', import.meta.url).href;
	const loaded = (await import(moduleUrl)) as Partial<HandlerModule>;
	if (typeof loaded.handler !== 'function') throw new Error('adapter-node handler is unavailable');
	return loaded.handler;
}

function listen(server: Server, port: number, host: string): Promise<void> {
	return new Promise((resolveListen, rejectListen) => {
		const error = (cause: Error) => {
			server.off('listening', listening);
			rejectListen(cause);
		};
		const listening = () => {
			server.off('error', error);
			resolveListen();
		};
		server.once('error', error);
		server.once('listening', listening);
		server.listen(port, host);
	});
}

function refuseUpgrade(socket: Duplex): void {
	try {
		socket.write('HTTP/1.1 426 Upgrade Required\r\nConnection: close\r\nContent-Length: 0\r\n\r\n');
	} finally {
		socket.destroy();
	}
}

export async function startCustomServer(dependencies: BootstrapDependencies = {}) {
	const environment = dependencies.environment ?? process.env;
	const address = listenAddress(environment);
	const fetcher = dependencies.fetcher ?? fetch;
	const runtime =
		dependencies.assembleRuntime?.() ?? assembleWorldProjectionRuntime({ environment, fetcher });
	installWorldRuntime(runtime, dependencies.registry ?? globalThis);

	const handler = await (dependencies.loadHandler ?? loadGeneratedHandler)();
	const server = (dependencies.createServer ?? createNodeServer)(handler);
	server.on('upgrade', dependencies.handleUpgrade ?? ((_request, socket) => refuseUpgrade(socket)));
	await (dependencies.listen ?? listen)(server, address.port, address.host);
	return Object.freeze({ server, runtime });
}
