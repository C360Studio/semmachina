import type { IncomingMessage } from 'node:http';
import type { Duplex } from 'node:stream';
import WebSocket, { WebSocketServer } from 'ws';

import { createBoundedRelayQueue } from './player-relay-queue';
import {
	createBridgeCapacity,
	DEFAULT_PLAYER_RELAY_POLICY,
	type BridgeCapacity,
	type PlayerRelayPolicy
} from './player-relay-policy';
import type { UpgradeAuthorization } from './surface-session';

interface TimerScheduler {
	readonly now: () => number;
	readonly setTimeout: (
		callback: () => void,
		milliseconds: number
	) => ReturnType<typeof setTimeout>;
	readonly clearTimeout: (timer: ReturnType<typeof setTimeout>) => void;
	readonly setInterval: (
		callback: () => void,
		milliseconds: number
	) => ReturnType<typeof setInterval>;
	readonly clearInterval: (timer: ReturnType<typeof setInterval>) => void;
}

interface SocketLike {
	readonly readyState: number;
	onceOpen(listener: () => void): void;
	offOpen(listener: () => void): void;
	onceError(listener: (error: Error) => void): void;
	onceClose(listener: (code: number, reason: Buffer) => void): void;
	onMessage(listener: (data: unknown, binary: boolean) => void): void;
	offMessage(listener: (data: unknown, binary: boolean) => void): void;
	onClose(listener: (code: number, reason: Buffer) => void): void;
	offClose(listener: (code: number, reason: Buffer) => void): void;
	onError(listener: (error: Error) => void): void;
	offError(listener: (error: Error) => void): void;
	onPong(listener: () => void): void;
	offPong(listener: () => void): void;
	send(
		data: Buffer,
		options: { binary: false; compress: false },
		callback: (error?: Error) => void
	): void;
	close(code?: number): void;
	terminate(): void;
	ping(): void;
}

class WsSocketAdapter implements SocketLike {
	constructor(private readonly websocket: WebSocket) {}
	get readyState() {
		return this.websocket.readyState;
	}
	onceOpen(listener: () => void) {
		this.websocket.once('open', listener);
	}
	offOpen(listener: () => void) {
		this.websocket.off('open', listener);
	}
	onceError(listener: (error: Error) => void) {
		this.websocket.once('error', listener);
	}
	onceClose(listener: (code: number, reason: Buffer) => void) {
		this.websocket.once('close', listener);
	}
	onMessage(listener: (data: unknown, binary: boolean) => void) {
		this.websocket.on('message', listener);
	}
	offMessage(listener: (data: unknown, binary: boolean) => void) {
		this.websocket.off('message', listener);
	}
	onClose(listener: (code: number, reason: Buffer) => void) {
		this.websocket.on('close', listener);
	}
	offClose(listener: (code: number, reason: Buffer) => void) {
		this.websocket.off('close', listener);
	}
	onError(listener: (error: Error) => void) {
		this.websocket.on('error', listener);
	}
	offError(listener: (error: Error) => void) {
		this.websocket.off('error', listener);
	}
	onPong(listener: () => void) {
		this.websocket.on('pong', listener);
	}
	offPong(listener: () => void) {
		this.websocket.off('pong', listener);
	}
	send(
		data: Buffer,
		options: { binary: false; compress: false },
		callback: (error?: Error) => void
	) {
		this.websocket.send(data, options, callback);
	}
	close(code?: number) {
		this.websocket.close(code);
	}
	terminate() {
		this.websocket.terminate();
	}
	ping() {
		this.websocket.ping();
	}
}

interface Upgrader {
	readonly accept: (
		request: IncomingMessage,
		socket: Duplex,
		head: Buffer,
		complete: (browser: SocketLike) => void
	) => void;
}

export interface PlayerRelayDependencies {
	readonly policy?: PlayerRelayPolicy;
	readonly capacity?: BridgeCapacity;
	readonly scheduler?: TimerScheduler;
	readonly dialUpstream?: (authorization: UpgradeAuthorization) => SocketLike;
	readonly upgrader?: Upgrader;
	readonly logger?: { readonly error: (code: string) => void };
}

export interface PlayerRelay {
	readonly handleUpgrade: (
		request: IncomingMessage,
		socket: Duplex,
		head: Buffer,
		authorization: UpgradeAuthorization
	) => void;
	readonly shutdown: () => void;
	readonly snapshot: () => { pending: number; active: number; total: number };
}

const CLOSED = WebSocket.CLOSED;
const SAFE_CLOSE_CODES = new Set([1000, 1001, 1002, 1003, 1007, 1008, 1009, 1011, 1012, 1013]);

function defaultScheduler(): TimerScheduler {
	return { now: Date.now, setTimeout, clearTimeout, setInterval, clearInterval };
}

function defaultUpgrader(policy: PlayerRelayPolicy): Upgrader {
	const server = new WebSocketServer({
		noServer: true,
		maxPayload: policy.browserMessageBytes,
		perMessageDeflate: false,
		handleProtocols: (protocols) =>
			protocols.size === 2 && protocols.has('semmachina.player.v1') ? 'semmachina.player.v1' : false
	});
	return Object.freeze({
		accept(
			request: IncomingMessage,
			socket: Duplex,
			head: Buffer,
			complete: (browser: SocketLike) => void
		) {
			server.handleUpgrade(request, socket, head, (browser) =>
				complete(new WsSocketAdapter(browser))
			);
		}
	});
}

function defaultDialer(policy: PlayerRelayPolicy) {
	return (authorization: UpgradeAuthorization): SocketLike =>
		new WsSocketAdapter(
			new WebSocket(authorization.playerWsUrl, {
				headers: { Authorization: `Bearer ${authorization.playerBearer}` },
				followRedirects: false,
				perMessageDeflate: false,
				handshakeTimeout: policy.handshakeMs,
				maxPayload: policy.upstreamMessageBytes
			})
		);
}

function refuseBeforeUpgrade(socket: Duplex): void {
	try {
		if (socket.destroyed) return;
	} catch {
		// Continue with best-effort refusal when a transport getter itself fails.
	}
	try {
		socket.write(
			'HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n'
		);
	} catch {
		// Pre-upgrade failures intentionally expose no transport details.
	}
	try {
		socket.destroy();
	} catch {
		// Destruction is best-effort; reservation cleanup is owned by the caller.
	}
}

function propagatedCode(code: number): number {
	return SAFE_CLOSE_CODES.has(code) ? code : 1011;
}

export function createPlayerRelay(dependencies: PlayerRelayDependencies = {}): PlayerRelay {
	const policy = dependencies.policy ?? DEFAULT_PLAYER_RELAY_POLICY;
	const capacity = dependencies.capacity ?? createBridgeCapacity(policy);
	const scheduler = dependencies.scheduler ?? defaultScheduler();
	const dialUpstream = dependencies.dialUpstream ?? defaultDialer(policy);
	const upgrader = dependencies.upgrader ?? defaultUpgrader(policy);
	const logger = dependencies.logger ?? { error: (code: string) => console.error(code) };
	const report = (code: string) => {
		try {
			logger.error(code);
		} catch {
			// Logging is never allowed to become a relay failure path.
		}
	};
	const attempt = (code: string, operation: () => void) => {
		try {
			operation();
		} catch {
			report(code);
		}
	};
	const operations = new Set<(code: number) => void>();
	let shuttingDown = false;

	function handleUpgrade(
		request: IncomingMessage,
		socket: Duplex,
		head: Buffer,
		authorization: UpgradeAuthorization
	): void {
		if (
			shuttingDown ||
			authorization.lease.signal.aborted ||
			authorization.lease.expiresAt <= scheduler.now()
		) {
			refuseBeforeUpgrade(socket);
			return;
		}
		const reservation = capacity.reserve(authorization.lease.identity);
		if (reservation === null) {
			refuseBeforeUpgrade(socket);
			return;
		}
		const acceptedReservation = reservation;
		let upstream: SocketLike | undefined;
		let pending = true;
		let finished = false;
		let reservationReleased = false;
		const pendingTimers: {
			handshake?: ReturnType<typeof setTimeout>;
			lease?: ReturnType<typeof setTimeout>;
		} = {};
		const pendingLeaseAbort = () => finishPending();
		const upstreamIsClosed = () => {
			if (upstream === undefined) return true;
			try {
				return upstream.readyState === CLOSED;
			} catch {
				report('player_relay_state_read_failed');
				return false;
			}
		};
		const releasePendingReservation = () => {
			if (reservationReleased) return;
			reservationReleased = true;
			if (upstream !== undefined) {
				attempt('player_relay_pending_cleanup_failed', () =>
					upstream?.offError(upstreamTerminalError)
				);
				attempt('player_relay_pending_cleanup_failed', () =>
					upstream?.offClose(upstreamTerminalClose)
				);
			}
			attempt('player_relay_capacity_release_failed', acceptedReservation.release);
		};
		function upstreamTerminalError() {
			report('player_relay_pending_error');
		}
		function upstreamTerminalClose() {
			releasePendingReservation();
		}

		const finishPending = () => {
			if (finished) return;
			finished = true;
			operations.delete(finishPending);
			const handshakeTimer = pendingTimers.handshake;
			const pendingLeaseTimer = pendingTimers.lease;
			if (handshakeTimer !== undefined)
				attempt('player_relay_timer_cleanup_failed', () => scheduler.clearTimeout(handshakeTimer));
			if (pendingLeaseTimer !== undefined)
				attempt('player_relay_timer_cleanup_failed', () =>
					scheduler.clearTimeout(pendingLeaseTimer)
				);
			authorization.lease.signal.removeEventListener('abort', pendingLeaseAbort);
			attempt('player_relay_pending_cleanup_failed', () => socket.off('close', browserGone));
			attempt('player_relay_pending_cleanup_failed', () => socket.off('error', browserGone));
			if (upstream !== undefined) {
				attempt('player_relay_pending_cleanup_failed', () => upstream?.offOpen(upstreamOpen));
				attempt('player_relay_pending_cleanup_failed', () =>
					upstream?.offError(upstreamPendingError)
				);
				attempt('player_relay_pending_cleanup_failed', () =>
					upstream?.offClose(upstreamPendingClose)
				);
			}
			if (pending) refuseBeforeUpgrade(socket);
			if (upstream === undefined) {
				releasePendingReservation();
				return;
			}
			attempt('player_relay_pending_cleanup_failed', () =>
				upstream?.onError(upstreamTerminalError)
			);
			attempt('player_relay_pending_cleanup_failed', () =>
				upstream?.onceClose(upstreamTerminalClose)
			);
			if (upstreamIsClosed()) {
				releasePendingReservation();
				return;
			}
			if (upstream !== undefined) {
				attempt('player_relay_pending_terminate_failed', () => {
					upstream?.terminate();
				});
			}
			if (upstreamIsClosed()) releasePendingReservation();
		};
		operations.add(finishPending);
		const browserGone = () => finishPending();
		socket.once('close', browserGone);
		socket.once('error', browserGone);

		try {
			upstream = dialUpstream(authorization);
		} catch {
			finishPending();
			return;
		}
		pendingTimers.handshake = scheduler.setTimeout(() => {
			report('player_relay_handshake_timeout');
			finishPending();
		}, policy.handshakeMs);
		pendingTimers.lease = scheduler.setTimeout(
			pendingLeaseAbort,
			Math.max(0, authorization.lease.expiresAt - scheduler.now())
		);
		authorization.lease.signal.addEventListener('abort', pendingLeaseAbort, { once: true });
		const dialing = upstream;
		function upstreamPendingError() {
			if (pending) finishPending();
		}
		function upstreamPendingClose() {
			if (pending) finishPending();
		}
		function upstreamOpen() {
			if (finished || socket.destroyed) {
				finishPending();
				return;
			}
			try {
				upgrader.accept(request, socket, head, (browser) => {
					if (finished) {
						attempt('player_relay_finished_browser_close_failed', () => browser.close(1013));
						return;
					}
					pending = false;
					finished = true;
					operations.delete(finishPending);
					const activeHandshakeTimer = pendingTimers.handshake;
					const activeLeaseTimer = pendingTimers.lease;
					if (activeHandshakeTimer !== undefined)
						attempt('player_relay_timer_cleanup_failed', () =>
							scheduler.clearTimeout(activeHandshakeTimer)
						);
					if (activeLeaseTimer !== undefined)
						attempt('player_relay_timer_cleanup_failed', () =>
							scheduler.clearTimeout(activeLeaseTimer)
						);
					authorization.lease.signal.removeEventListener('abort', pendingLeaseAbort);
					attempt('player_relay_pending_cleanup_failed', () => socket.off('close', browserGone));
					attempt('player_relay_pending_cleanup_failed', () => socket.off('error', browserGone));
					attempt('player_relay_pending_cleanup_failed', () =>
						dialing.offError(upstreamPendingError)
					);
					attempt('player_relay_pending_cleanup_failed', () =>
						dialing.offClose(upstreamPendingClose)
					);
					acceptedReservation.activate();
					startActiveRelay(browser, dialing, authorization, acceptedReservation.release);
				});
			} catch {
				finishPending();
			}
		}
		dialing.onceError(upstreamPendingError);
		dialing.onceClose(upstreamPendingClose);
		dialing.onceOpen(upstreamOpen);
	}

	function startActiveRelay(
		browser: SocketLike,
		upstream: SocketLike,
		authorization: UpgradeAuthorization,
		release: () => void
	): void {
		let stopped = false;
		let finalized = false;
		let browserLive = true;
		let upstreamLive = true;
		let pongTimer: ReturnType<typeof setTimeout> | undefined;
		let graceTimer: ReturnType<typeof setTimeout> | undefined;
		const writeTimers = new Set<ReturnType<typeof setTimeout>>();
		const isClosed = (peer: SocketLike): boolean => {
			try {
				return peer.readyState === CLOSED;
			} catch {
				report('player_relay_state_read_failed');
				return false;
			}
		};

		const finalize = () => {
			if (finalized) return;
			finalized = true;
			const activeGraceTimer = graceTimer;
			if (activeGraceTimer !== undefined)
				attempt('player_relay_timer_cleanup_failed', () =>
					scheduler.clearTimeout(activeGraceTimer)
				);
			attempt('player_relay_final_cleanup_failed', () => browser.offClose(browserFinalClose));
			attempt('player_relay_final_cleanup_failed', () => upstream.offClose(upstreamFinalClose));
			attempt('player_relay_final_cleanup_failed', () => browser.offError(browserError));
			attempt('player_relay_final_cleanup_failed', () => upstream.offError(upstreamError));
			attempt('player_relay_capacity_release_failed', release);
		};
		const finalizeIfClosed = () => {
			if (isClosed(browser) && isClosed(upstream)) finalize();
		};
		const browserFinalClose = () => finalizeIfClosed();
		const upstreamFinalClose = () => finalizeIfClosed();

		const shutdown = (code: number) => {
			if (stopped) return;
			stopped = true;
			operations.delete(shutdown);
			attempt('player_relay_queue_cleanup_failed', browserToUpstream.stop);
			attempt('player_relay_queue_cleanup_failed', upstreamToBrowser.stop);
			attempt('player_relay_timer_cleanup_failed', () => scheduler.clearInterval(livenessTimer));
			const activePongTimer = pongTimer;
			if (activePongTimer !== undefined)
				attempt('player_relay_timer_cleanup_failed', () => scheduler.clearTimeout(activePongTimer));
			attempt('player_relay_timer_cleanup_failed', () => scheduler.clearTimeout(leaseTimer));
			for (const timer of writeTimers)
				attempt('player_relay_timer_cleanup_failed', () => scheduler.clearTimeout(timer));
			writeTimers.clear();
			authorization.lease.signal.removeEventListener('abort', leaseAbort);
			attempt('player_relay_listener_cleanup_failed', () => browser.offMessage(browserMessage));
			attempt('player_relay_listener_cleanup_failed', () => upstream.offMessage(upstreamMessage));
			attempt('player_relay_listener_cleanup_failed', () => browser.offClose(browserClose));
			attempt('player_relay_listener_cleanup_failed', () => upstream.offClose(upstreamClose));
			// Error sinks remain attached throughout CLOSING and are removed only by finalize.
			attempt('player_relay_listener_cleanup_failed', () => browser.offPong(browserActivity));
			attempt('player_relay_listener_cleanup_failed', () => upstream.offPong(upstreamActivity));
			attempt('player_relay_listener_cleanup_failed', () => browser.onceClose(browserFinalClose));
			attempt('player_relay_listener_cleanup_failed', () => upstream.onceClose(upstreamFinalClose));
			if (!isClosed(browser))
				attempt('player_relay_browser_close_failed', () => browser.close(code));
			if (!isClosed(upstream))
				attempt('player_relay_upstream_close_failed', () => upstream.close(code));
			finalizeIfClosed();
			if (finalized) return;
			try {
				graceTimer = scheduler.setTimeout(() => {
					attempt('player_relay_browser_terminate_failed', () => {
						if (!isClosed(browser)) browser.terminate();
					});
					attempt('player_relay_upstream_terminate_failed', () => {
						if (!isClosed(upstream)) upstream.terminate();
					});
					finalizeIfClosed();
				}, policy.closeGraceMs);
			} catch {
				report('player_relay_grace_timer_failed');
				attempt('player_relay_browser_terminate_failed', () => {
					if (!isClosed(browser)) browser.terminate();
				});
				attempt('player_relay_upstream_terminate_failed', () => {
					if (!isClosed(upstream)) upstream.terminate();
				});
				finalizeIfClosed();
			}
		};

		function send(target: SocketLike, payload: Buffer, complete: (error?: Error) => void): void {
			let done = false;
			const timer = scheduler.setTimeout(() => {
				if (done) return;
				done = true;
				writeTimers.delete(timer);
				report('player_relay_write_timeout');
				complete(new Error('write timeout'));
			}, policy.writeMs);
			writeTimers.add(timer);
			try {
				target.send(payload, { binary: false, compress: false }, (error) => {
					if (done) return;
					done = true;
					attempt('player_relay_timer_cleanup_failed', () => scheduler.clearTimeout(timer));
					writeTimers.delete(timer);
					// ws reports successful writes as null at runtime despite its optional-Error type.
					complete(error ?? undefined);
				});
			} catch {
				if (done) return;
				done = true;
				attempt('player_relay_timer_cleanup_failed', () => scheduler.clearTimeout(timer));
				writeTimers.delete(timer);
				complete(new Error('relay send failed'));
			}
		}

		const browserToUpstream = createBoundedRelayQueue({
			maxMessages: policy.outstandingMessages,
			maxBytes: policy.browserToUpstreamBytes,
			send: (payload, complete) => send(upstream, payload, complete),
			overflow: () => {
				report('player_relay_queue_overflow');
				shutdown(1013);
			},
			failed: () => {
				report('player_relay_browser_to_upstream_write_failed');
				shutdown(1013);
			}
		});
		const upstreamToBrowser = createBoundedRelayQueue({
			maxMessages: policy.outstandingMessages,
			maxBytes: policy.upstreamToBrowserBytes,
			send: (payload, complete) => send(browser, payload, complete),
			overflow: () => {
				report('player_relay_queue_overflow');
				shutdown(1013);
			},
			failed: () => {
				report('player_relay_upstream_to_browser_write_failed');
				shutdown(1013);
			}
		});

		function payload(data: unknown): Buffer | null {
			if (Buffer.isBuffer(data)) return data;
			if (data instanceof ArrayBuffer) return Buffer.from(data);
			if (Array.isArray(data)) return Buffer.concat(data as Buffer[]);
			if (ArrayBuffer.isView(data)) {
				return Buffer.from(data.buffer, data.byteOffset, data.byteLength);
			}
			return null;
		}
		function browserMessage(data: unknown, binary: boolean) {
			browserLive = true;
			const bytes = payload(data);
			if (bytes === null) shutdown(1011);
			else if (binary) shutdown(1003);
			else if (bytes.byteLength > policy.browserMessageBytes) shutdown(1009);
			else browserToUpstream.enqueue(bytes);
		}
		function upstreamMessage(data: unknown, binary: boolean) {
			upstreamLive = true;
			const bytes = payload(data);
			if (bytes === null) shutdown(1011);
			else if (binary) shutdown(1003);
			else if (bytes.byteLength > policy.upstreamMessageBytes) shutdown(1009);
			else upstreamToBrowser.enqueue(bytes);
		}
		const browserActivity = () => {
			browserLive = true;
		};
		const upstreamActivity = () => {
			upstreamLive = true;
		};
		const browserClose = (code: number) => shutdown(propagatedCode(code));
		const upstreamClose = (code: number) => shutdown(propagatedCode(code));
		const browserError = () => shutdown(1011);
		const upstreamError = () => shutdown(1011);
		const leaseAbort = () => shutdown(1008);

		browser.onMessage(browserMessage);
		upstream.onMessage(upstreamMessage);
		browser.onClose(browserClose);
		upstream.onClose(upstreamClose);
		browser.onError(browserError);
		upstream.onError(upstreamError);
		browser.onPong(browserActivity);
		upstream.onPong(upstreamActivity);
		authorization.lease.signal.addEventListener('abort', leaseAbort, { once: true });
		const leaseTimer = scheduler.setTimeout(
			leaseAbort,
			Math.max(0, authorization.lease.expiresAt - scheduler.now())
		);
		const livenessTimer = scheduler.setInterval(() => {
			if (stopped) return;
			browserLive = false;
			upstreamLive = false;
			try {
				browser.ping();
				upstream.ping();
			} catch {
				shutdown(1001);
				return;
			}
			if (pongTimer !== undefined) scheduler.clearTimeout(pongTimer);
			pongTimer = scheduler.setTimeout(() => {
				if (!browserLive || !upstreamLive) shutdown(1001);
			}, policy.pongMs);
		}, policy.pingMs);
		operations.add(shutdown);
	}

	function shutdown(): void {
		if (shuttingDown) return;
		shuttingDown = true;
		for (const stop of [...operations]) {
			attempt('player_relay_global_shutdown_failed', () => stop(1001));
		}
		operations.clear();
	}

	return Object.freeze({ handleUpgrade, shutdown, snapshot: capacity.snapshot });
}
