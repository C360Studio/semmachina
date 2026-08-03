import {
	createServer as createNodeServer,
	type IncomingMessage,
	type RequestListener,
	type Server,
	type ServerResponse
} from 'node:http';
import type { Duplex } from 'node:stream';

import type { DeploymentEnvironment } from './deployment-config';
import { createPlayerRelay, type PlayerRelay } from './player-relay';
import { assembleSurfaceRuntime } from './surface-runtime';
import type { UpgradeAuthorization } from './surface-session';
import { INTERNAL_TRANSPORT_HEADER } from './transport-boundary';
import { installWorldRuntime, type InstalledWorldRuntime } from './world-runtime-registry';

interface HandlerModule {
	readonly handler: RequestListener;
}

export type AuthorizedUpgradeContinuation = (
	incoming: IncomingMessage,
	request: Request,
	socket: Duplex,
	head: Buffer,
	authorization: UpgradeAuthorization
) => void;

export interface BootstrapDependencies {
	readonly environment?: DeploymentEnvironment;
	readonly registry?: object;
	readonly fetcher?: typeof fetch;
	readonly loadHandler?: () => Promise<RequestListener>;
	readonly createServer?: (handler: RequestListener) => Server;
	readonly listen?: (server: Server, port: number, host: string) => Promise<void>;
	readonly assembleRuntime?: () => InstalledWorldRuntime;
	readonly continueAuthorizedUpgrade?: AuthorizedUpgradeContinuation;
	readonly playerRelay?: PlayerRelay;
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

function refuseRawRequest(response: ServerResponse): void {
	response.writeHead(401, {
		'cache-control': 'no-store',
		connection: 'close',
		'content-length': '0'
	});
	response.end();
}

function fetchUpgradeRequest(request: IncomingMessage): Request | null {
	const headers = new Headers();
	for (let index = 0; index < request.rawHeaders.length; index += 2) {
		const name = request.rawHeaders[index];
		if (name === undefined || name.toLowerCase() === INTERNAL_TRANSPORT_HEADER) continue;
		headers.append(name, request.rawHeaders[index + 1] ?? '');
	}
	const attestation = request.headers[INTERNAL_TRANSPORT_HEADER];
	if (typeof attestation !== 'string') return null;
	headers.set(INTERNAL_TRANSPORT_HEADER, attestation);
	const host = request.headers.host;
	if (typeof host !== 'string' || request.url === undefined || request.method === undefined)
		return null;
	try {
		return new Request(`https://${host}${request.url}`, { method: request.method, headers });
	} catch {
		return null;
	}
}

export async function startCustomServer(dependencies: BootstrapDependencies = {}) {
	const environment = dependencies.environment ?? process.env;
	const address = listenAddress(environment);
	const fetcher = dependencies.fetcher ?? fetch;
	const runtime =
		dependencies.assembleRuntime?.() ?? assembleSurfaceRuntime({ environment, fetcher });
	if (runtime.attestRawTransport === undefined || runtime.authorizeUpgrade === undefined) {
		throw new Error('surface transport runtime is unavailable');
	}
	installWorldRuntime(runtime, dependencies.registry ?? globalThis);

	const handler = await (dependencies.loadHandler ?? loadGeneratedHandler)();
	const guardedHandler: RequestListener = (request, response) => {
		if (!runtime.attestRawTransport?.(request)) {
			refuseRawRequest(response);
			return;
		}
		handler(request, response);
	};
	const server = (dependencies.createServer ?? createNodeServer)(guardedHandler);
	const playerRelay = dependencies.playerRelay ?? createPlayerRelay();
	server.once('close', () => playerRelay.shutdown());
	server.on('upgrade', (incoming, socket, head) => {
		if (!runtime.attestRawTransport?.(incoming)) {
			refuseUpgrade(socket);
			return;
		}
		const request = fetchUpgradeRequest(incoming);
		const authorization = request === null ? null : runtime.authorizeUpgrade?.(request);
		if (request === null || authorization === null || authorization === undefined) {
			refuseUpgrade(socket);
			return;
		}
		(
			dependencies.continueAuthorizedUpgrade ??
			((raw, _request, target, upgradeHead, allowed) =>
				playerRelay.handleUpgrade(raw, target, upgradeHead, allowed))
		)(incoming, request, socket, head, authorization);
	});
	await (dependencies.listen ?? listen)(server, address.port, address.host);
	return Object.freeze({ server, runtime });
}
