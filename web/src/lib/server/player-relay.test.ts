import { EventEmitter } from 'node:events';
import type { IncomingMessage } from 'node:http';
import type { Duplex } from 'node:stream';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createPlayerRelay } from './player-relay';
import { DEFAULT_PLAYER_RELAY_POLICY } from './player-relay-policy';
import type { SessionLease, UpgradeAuthorization } from './surface-session';

class FakeWebSocket extends EventEmitter {
	readyState = 0;
	readonly sends: Array<{
		data: Buffer;
		options: { binary: false; compress: false };
	}> = [];
	readonly closeCodes: number[] = [];
	readonly pings: number[] = [];
	readonly terminations: number[] = [];
	holdWrites = false;
	throwSend = false;
	throwClose = false;
	throwTerminate = false;
	emitAsyncTerminateError = false;
	sendError: Error | undefined;
	readonly completions: Array<(error?: Error) => void> = [];
	onceOpen(listener: () => void) {
		this.once('open', listener);
	}
	offOpen(listener: () => void) {
		this.off('open', listener);
	}
	onceError(listener: (error: Error) => void) {
		this.once('error', listener);
	}
	onceClose(listener: (code: number, reason: Buffer) => void) {
		this.once('close', listener);
	}
	onMessage(listener: (data: unknown, binary: boolean) => void) {
		this.on('message', listener);
	}
	offMessage(listener: (data: unknown, binary: boolean) => void) {
		this.off('message', listener);
	}
	onClose(listener: (code: number, reason: Buffer) => void) {
		this.on('close', listener);
	}
	offClose(listener: (code: number, reason: Buffer) => void) {
		this.off('close', listener);
	}
	onError(listener: (error: Error) => void) {
		this.on('error', listener);
	}
	offError(listener: (error: Error) => void) {
		this.off('error', listener);
	}
	onPong(listener: () => void) {
		this.on('pong', listener);
	}
	offPong(listener: () => void) {
		this.off('pong', listener);
	}

	open() {
		this.readyState = 1;
		this.emit('open');
	}

	send(
		data: Buffer,
		options: { binary: false; compress: false },
		complete: (error?: Error) => void
	) {
		if (this.throwSend) throw new Error('sync send failure');
		this.sends.push({ data, options });
		if (this.holdWrites) this.completions.push(complete);
		else complete(this.sendError);
	}

	close(code = 1000) {
		this.closeCodes.push(code);
		if (this.throwClose) throw new Error('sync close failure');
		this.readyState = 2;
	}

	remoteClose(code: number) {
		this.readyState = 3;
		this.emit('close', code, Buffer.from('secret reason'));
	}

	terminate() {
		this.terminations.push(1);
		if (this.emitAsyncTerminateError) {
			setTimeout(() => this.emit('error', new Error('async terminate error')), 0);
		}
		if (this.throwTerminate) throw new Error('sync terminate failure');
		this.readyState = 3;
		this.emit('close', 1006, Buffer.alloc(0));
	}

	ping() {
		this.pings.push(1);
	}
}

class FakeRawSocket extends EventEmitter {
	destroyed = false;
	readonly writes: string[] = [];
	write(value: string) {
		this.writes.push(value);
		return true;
	}
	destroy() {
		if (this.destroyed) return this;
		this.destroyed = true;
		this.emit('close');
		return this;
	}
}

const policy = Object.freeze({
	...DEFAULT_PLAYER_RELAY_POLICY,
	processBridges: 1,
	perSessionBridges: 1,
	handshakeMs: 100,
	writeMs: 100,
	pingMs: 100,
	pongMs: 50,
	closeGraceMs: 25
});

function authorization(controller = new AbortController(), expiresAt = Date.now() + 1000) {
	const lease: SessionLease = Object.freeze({
		identity: Object.freeze({}),
		expiresAt,
		signal: controller.signal
	});
	return {
		controller,
		authorization: Object.freeze({
			playerId: 'c360.semmachina.bellweather.bellweather-maze.player.detective',
			playerBearer: 'player-bearer-that-is-distinct',
			playerWsUrl: 'ws://127.0.0.1:8081/play',
			protocol: 'semmachina.player.v1' as const,
			lease
		}) satisfies UpgradeAuthorization
	};
}

function harness(auth = authorization()) {
	const upstream = new FakeWebSocket();
	const browser = new FakeWebSocket();
	browser.readyState = 1;
	const dialUpstream = vi.fn(() => upstream);
	const upgrader = {
		accept: vi.fn(
			(
				_request: IncomingMessage,
				_socket: Duplex,
				_head: Buffer,
				complete: (browser: FakeWebSocket) => void
			) => complete(browser)
		)
	};
	const relay = createPlayerRelay({ policy, dialUpstream, upgrader });
	const raw = new FakeRawSocket();
	relay.handleUpgrade(
		{} as IncomingMessage,
		raw as unknown as Duplex,
		Buffer.alloc(0),
		auth.authorization
	);
	return { ...auth, relay, upstream, browser, dialUpstream, upgrader, raw };
}

describe('player byte relay', () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(1_000_000);
	});
	afterEach(() => vi.useRealTimers());

	it('reserves pending capacity before dial and refuses the newest request', () => {
		const setup = harness();
		const newest = new FakeRawSocket();
		setup.relay.handleUpgrade(
			{} as IncomingMessage,
			newest as unknown as Duplex,
			Buffer.alloc(0),
			setup.authorization
		);
		expect(setup.dialUpstream).toHaveBeenCalledOnce();
		expect(newest.destroyed).toBe(true);
		expect(setup.relay.snapshot()).toEqual({ pending: 1, active: 0, total: 1 });
	});

	it('refuses an aborted or expired lease before reservation and upstream dial', () => {
		const aborted = authorization();
		aborted.controller.abort();
		const dial = vi.fn(() => new FakeWebSocket());
		const relay = createPlayerRelay({ policy, dialUpstream: dial });
		const first = new FakeRawSocket();
		relay.handleUpgrade(
			{} as IncomingMessage,
			first as unknown as Duplex,
			Buffer.alloc(0),
			aborted.authorization
		);
		const expired = authorization(new AbortController(), Date.now());
		const second = new FakeRawSocket();
		relay.handleUpgrade(
			{} as IncomingMessage,
			second as unknown as Duplex,
			Buffer.alloc(0),
			expired.authorization
		);
		expect(dial).not.toHaveBeenCalled();
		expect(first.destroyed).toBe(true);
		expect(second.destroyed).toBe(true);
	});

	it('cancels a pending dial when its lease aborts or expires', () => {
		const aborted = harness();
		aborted.controller.abort();
		expect(aborted.upstream.terminations).toHaveLength(1);
		expect(aborted.raw.destroyed).toBe(true);
		expect(aborted.upgrader.accept).not.toHaveBeenCalled();
		expect(aborted.relay.snapshot().total).toBe(0);
		expect(aborted.upstream.listenerCount('open')).toBe(0);
		expect(aborted.upstream.listenerCount('error')).toBe(0);
		expect(aborted.upstream.listenerCount('close')).toBe(0);

		const expiry = authorization(new AbortController(), Date.now() + 40);
		const expired = harness(expiry);
		vi.advanceTimersByTime(40);
		expect(expired.upstream.terminations).toHaveLength(1);
		expect(expired.raw.destroyed).toBe(true);
		expect(expired.upgrader.accept).not.toHaveBeenCalled();
	});

	it('keeps the end-to-end handshake deadline armed until browser upgrade completes', () => {
		const upstream = new FakeWebSocket();
		const upgrader = { accept: vi.fn() };
		const relay = createPlayerRelay({ policy, dialUpstream: () => upstream, upgrader });
		const raw = new FakeRawSocket();
		relay.handleUpgrade(
			{} as IncomingMessage,
			raw as unknown as Duplex,
			Buffer.alloc(0),
			authorization().authorization
		);
		upstream.open();
		expect(upgrader.accept).toHaveBeenCalledOnce();
		vi.advanceTimersByTime(policy.handshakeMs);
		expect(raw.destroyed).toBe(true);
		expect(upstream.terminations).toHaveLength(1);
		expect(relay.snapshot().total).toBe(0);
	});

	it('waits for upstream open, then relays complete text bytes without transformation', () => {
		const setup = harness();
		expect(setup.upgrader.accept).not.toHaveBeenCalled();
		setup.upstream.open();
		expect(setup.upgrader.accept).toHaveBeenCalledOnce();
		expect(setup.raw.listenerCount('close')).toBe(0);
		expect(setup.raw.listenerCount('error')).toBe(0);
		const outbound = Buffer.from('Midsomer \u{1f50d}', 'utf8');
		const inbound = Buffer.from('exact narration \u{1f3ad}', 'utf8');
		setup.browser.emit('message', outbound, false);
		setup.upstream.emit('message', inbound, false);
		expect(setup.upstream.sends).toEqual([
			{ data: outbound, options: { binary: false, compress: false } }
		]);
		expect(setup.browser.sends).toEqual([
			{ data: inbound, options: { binary: false, compress: false } }
		]);
		expect(setup.relay.snapshot()).toEqual({ pending: 0, active: 1, total: 1 });
	});

	it('accepts exact complete-message/queue limits and closes both peers on the first +1', () => {
		const setup = harness();
		setup.upstream.holdWrites = true;
		setup.upstream.open();
		setup.browser.emit('message', Buffer.alloc(policy.browserMessageBytes), false);
		expect(setup.upstream.sends).toHaveLength(1);
		for (let index = 1; index < policy.outstandingMessages; index += 1) {
			setup.browser.emit('message', Buffer.from([index]), false);
		}
		expect(setup.browser.closeCodes).toEqual([]);
		setup.browser.emit('message', Buffer.from([9]), false);
		expect(setup.browser.closeCodes).toContain(1013);
		expect(setup.upstream.closeCodes).toContain(1013);

		const oversize = harness();
		oversize.upstream.open();
		oversize.browser.emit('message', Buffer.alloc(policy.browserMessageBytes + 1), false);
		expect(oversize.browser.closeCodes).toContain(1009);
		expect(oversize.upstream.closeCodes).toContain(1009);
	});

	it('enforces exact and +1 directional byte queues independently', () => {
		const outbound = harness();
		outbound.upstream.holdWrites = true;
		outbound.upstream.open();
		for (let index = 0; index < 5; index += 1) {
			outbound.browser.emit('message', Buffer.alloc(policy.browserMessageBytes), false);
		}
		outbound.browser.emit(
			'message',
			Buffer.alloc(policy.browserToUpstreamBytes - policy.browserMessageBytes * 5),
			false
		);
		expect(outbound.browser.closeCodes).toEqual([]);
		outbound.browser.emit('message', Buffer.from([1]), false);
		expect(outbound.browser.closeCodes).toContain(1013);

		const inbound = harness();
		inbound.browser.holdWrites = true;
		inbound.upstream.open();
		inbound.upstream.emit('message', Buffer.alloc(policy.upstreamMessageBytes), false);
		inbound.upstream.emit('message', Buffer.alloc(policy.upstreamMessageBytes), false);
		expect(inbound.browser.closeCodes).toEqual([]);
		inbound.upstream.emit('message', Buffer.from([1]), false);
		expect(inbound.browser.closeCodes).toContain(1013);
		expect(inbound.upstream.closeCodes).toContain(1013);
	});

	it('accepts the exact upstream complete message and closes both peers on +1', () => {
		const exact = harness();
		exact.upstream.open();
		exact.upstream.emit('message', Buffer.alloc(policy.upstreamMessageBytes), false);
		expect(exact.browser.sends[0]?.data.byteLength).toBe(policy.upstreamMessageBytes);
		expect(exact.browser.closeCodes).toEqual([]);

		const exceeded = harness();
		exceeded.upstream.open();
		exceeded.upstream.emit('message', Buffer.alloc(policy.upstreamMessageBytes + 1), false);
		expect(exceeded.browser.closeCodes).toContain(1009);
		expect(exceeded.upstream.closeCodes).toContain(1009);
	});

	it('closes both peers for binary input and send errors', () => {
		const binary = harness();
		binary.upstream.open();
		binary.browser.emit('message', Buffer.from('binary'), true);
		expect(binary.browser.closeCodes).toContain(1003);
		expect(binary.upstream.closeCodes).toContain(1003);

		const failed = harness();
		failed.upstream.sendError = new Error('write failed');
		failed.upstream.open();
		failed.browser.emit('message', Buffer.from('text'), false);
		expect(failed.browser.closeCodes).toContain(1013);
		expect(failed.upstream.closeCodes).toContain(1013);

		const upstreamBinary = harness();
		upstreamBinary.upstream.open();
		upstreamBinary.upstream.emit('message', Buffer.from('binary'), true);
		expect(upstreamBinary.browser.closeCodes).toContain(1003);
		expect(upstreamBinary.upstream.closeCodes).toContain(1003);
	});

	it('contains a synchronous send throw and shuts down with stable 1013', () => {
		const setup = harness();
		setup.upstream.throwSend = true;
		setup.upstream.open();
		expect(() => setup.browser.emit('message', Buffer.from('text'), false)).not.toThrow();
		expect(setup.browser.closeCodes).toContain(1013);
		expect(setup.upstream.closeCodes).toContain(1013);
	});

	it('closes both peers when one active write exceeds its deadline', () => {
		const setup = harness();
		setup.upstream.holdWrites = true;
		setup.upstream.open();
		setup.browser.emit('message', Buffer.from('blocked text'), false);
		vi.advanceTimersByTime(policy.writeMs);
		expect(setup.browser.closeCodes).toContain(1013);
		expect(setup.upstream.closeCodes).toContain(1013);
	});

	it('cancels pending work on browser or upstream loss without upgrading', () => {
		const browserLoss = harness();
		browserLoss.raw.destroy();
		expect(browserLoss.upstream.terminations).toHaveLength(1);
		expect(browserLoss.upgrader.accept).not.toHaveBeenCalled();
		expect(browserLoss.relay.snapshot().total).toBe(0);

		const upstreamLoss = harness();
		upstreamLoss.upstream.remoteClose(1006);
		expect(upstreamLoss.raw.destroyed).toBe(true);
		expect(upstreamLoss.upgrader.accept).not.toHaveBeenCalled();
		expect(upstreamLoss.relay.snapshot().total).toBe(0);
	});

	it('expires and aborts a live lease without another authentication request', () => {
		const expiring = authorization(new AbortController(), Date.now() + 40);
		const setup = harness(expiring);
		setup.upstream.open();
		vi.advanceTimersByTime(40);
		expect(setup.browser.closeCodes).toContain(1008);
		expect(setup.upstream.closeCodes).toContain(1008);
		expect(setup.relay.snapshot().active).toBe(1);
		vi.advanceTimersByTime(policy.closeGraceMs);
		expect(setup.relay.snapshot()).toEqual({ pending: 0, active: 0, total: 0 });

		const aborted = harness();
		aborted.upstream.open();
		aborted.controller.abort();
		expect(aborted.browser.closeCodes).toContain(1008);
	});

	it('requires liveness from both peers but permits an idle player that answers probes', () => {
		const setup = harness();
		setup.upstream.open();
		vi.advanceTimersByTime(policy.pingMs);
		expect(setup.browser.pings).toHaveLength(1);
		expect(setup.upstream.pings).toHaveLength(1);
		setup.browser.emit('pong');
		setup.upstream.emit('pong');
		vi.advanceTimersByTime(policy.pongMs);
		expect(setup.browser.closeCodes).toEqual([]);
		vi.advanceTimersByTime(policy.pingMs);
		setup.browser.emit('pong');
		vi.advanceTimersByTime(policy.pongMs);
		expect(setup.browser.closeCodes).toContain(1001);
	});

	it('maps application close codes to 1011 and retains capacity through close grace', () => {
		const setup = harness();
		setup.upstream.open();
		setup.browser.remoteClose(3001);
		expect(setup.upstream.closeCodes).toContain(1011);
		expect(setup.relay.snapshot().active).toBe(1);
		vi.advanceTimersByTime(policy.closeGraceMs);
		expect(setup.relay.snapshot().total).toBe(0);
		setup.relay.shutdown();
		setup.relay.shutdown();
		expect(setup.relay.snapshot().total).toBe(0);
	});

	it('handles active errors without reason disclosure and cleans resources after grace', () => {
		const logger = { error: vi.fn() };
		const upstream = new FakeWebSocket();
		const browser = new FakeWebSocket();
		browser.readyState = 1;
		const relay = createPlayerRelay({
			policy,
			logger,
			dialUpstream: () => upstream,
			upgrader: {
				accept: (_request, _socket, _head, complete) => complete(browser)
			}
		});
		const raw = new FakeRawSocket();
		relay.handleUpgrade(
			{} as IncomingMessage,
			raw as unknown as Duplex,
			Buffer.alloc(0),
			authorization().authorization
		);
		upstream.open();
		browser.emit('error', new Error('secret browser reason'));
		expect(browser.closeCodes).toContain(1011);
		expect(upstream.closeCodes).toContain(1011);
		expect(JSON.stringify(logger.error.mock.calls)).not.toContain('secret browser reason');
		vi.advanceTimersByTime(policy.closeGraceMs);
		expect(relay.snapshot().total).toBe(0);
	});

	it('shutdown discards queued work, never replays it, and releases exactly once after grace', () => {
		const setup = harness();
		setup.upstream.holdWrites = true;
		setup.upstream.open();
		setup.browser.emit('message', Buffer.from('active'), false);
		setup.browser.emit('message', Buffer.from('queued-never-replay'), false);
		expect(setup.upstream.sends).toHaveLength(1);
		setup.relay.shutdown();
		setup.relay.shutdown();
		expect(setup.browser.closeCodes).toContain(1001);
		expect(setup.upstream.closeCodes).toContain(1001);
		expect(setup.relay.snapshot().active).toBe(1);
		setup.upstream.completions.shift()?.();
		setup.browser.emit('message', Buffer.from('late'), false);
		expect(setup.upstream.sends).toHaveLength(1);
		vi.advanceTimersByTime(policy.closeGraceMs);
		expect(setup.relay.snapshot()).toEqual({ pending: 0, active: 0, total: 0 });
		expect(setup.browser.listenerCount('message')).toBe(0);
		expect(setup.browser.listenerCount('error')).toBe(0);
		expect(setup.browser.listenerCount('pong')).toBe(0);
		expect(setup.upstream.listenerCount('message')).toBe(0);
	});

	it('contains close and terminate throws without undercounting live peers', () => {
		const closeFailure = harness();
		closeFailure.browser.throwClose = true;
		closeFailure.upstream.open();
		expect(() => closeFailure.relay.shutdown()).not.toThrow();
		expect(closeFailure.upstream.closeCodes).toContain(1001);
		vi.advanceTimersByTime(policy.closeGraceMs);
		expect(closeFailure.relay.snapshot().total).toBe(0);

		const terminateFailure = harness();
		terminateFailure.browser.throwClose = true;
		terminateFailure.browser.throwTerminate = true;
		terminateFailure.upstream.open();
		expect(() => terminateFailure.relay.shutdown()).not.toThrow();
		expect(() => vi.advanceTimersByTime(policy.closeGraceMs)).not.toThrow();
		expect(terminateFailure.upstream.terminations).toHaveLength(1);
		expect(terminateFailure.relay.snapshot().total).toBe(1);
		expect(terminateFailure.browser.listenerCount('error')).toBeGreaterThan(0);
		terminateFailure.browser.remoteClose(1006);
		expect(terminateFailure.relay.snapshot().total).toBe(0);
		expect(terminateFailure.browser.listenerCount('error')).toBe(0);
	});

	it('retains non-disclosing error sinks through grace and removes them at final cleanup', () => {
		const setup = harness();
		setup.upstream.open();
		setup.relay.shutdown();
		expect(setup.browser.listenerCount('error')).toBeGreaterThan(0);
		expect(setup.upstream.listenerCount('error')).toBeGreaterThan(0);
		expect(() => setup.browser.emit('error', new Error('closing browser secret'))).not.toThrow();
		expect(() => setup.upstream.emit('error', new Error('closing upstream secret'))).not.toThrow();
		vi.advanceTimersByTime(policy.closeGraceMs);
		expect(setup.browser.listenerCount('error')).toBe(0);
		expect(setup.upstream.listenerCount('error')).toBe(0);
		expect(setup.relay.snapshot().total).toBe(0);
	});

	it('contains an asynchronous pending terminate error and releases only after confirmed close', () => {
		const setup = harness();
		setup.upstream.emitAsyncTerminateError = true;
		setup.upstream.throwTerminate = true;
		expect(() => setup.controller.abort()).not.toThrow();
		expect(setup.raw.destroyed).toBe(true);
		expect(setup.relay.snapshot()).toEqual({ pending: 1, active: 0, total: 1 });
		expect(setup.upstream.listenerCount('open')).toBe(0);
		expect(setup.upstream.listenerCount('error')).toBe(1);
		expect(setup.upstream.listenerCount('close')).toBe(1);
		expect(() => vi.advanceTimersByTime(0)).not.toThrow();
		expect(setup.relay.snapshot().total).toBe(1);
		setup.upstream.remoteClose(1006);
		expect(setup.relay.snapshot().total).toBe(0);
		expect(setup.upstream.listenerCount('error')).toBe(0);
		expect(setup.upstream.listenerCount('close')).toBe(0);
	});

	it('continues global shutdown across a throwing first bridge', () => {
		const multiPolicy = Object.freeze({ ...policy, processBridges: 2, perSessionBridges: 1 });
		const upstreams = [new FakeWebSocket(), new FakeWebSocket()];
		const browsers = [new FakeWebSocket(), new FakeWebSocket()];
		for (const browser of browsers) browser.readyState = 1;
		browsers[0].throwClose = true;
		browsers[0].throwTerminate = true;
		let dialIndex = 0;
		let upgradeIndex = 0;
		const relay = createPlayerRelay({
			policy: multiPolicy,
			dialUpstream: () => upstreams[dialIndex++],
			upgrader: {
				accept: (_request, _socket, _head, complete) => complete(browsers[upgradeIndex++])
			}
		});
		for (let index = 0; index < 2; index += 1) {
			relay.handleUpgrade(
				{} as IncomingMessage,
				new FakeRawSocket() as unknown as Duplex,
				Buffer.alloc(0),
				authorization().authorization
			);
			upstreams[index].open();
		}
		expect(relay.snapshot().active).toBe(2);
		expect(() => relay.shutdown()).not.toThrow();
		expect(browsers[1].closeCodes).toContain(1001);
		expect(upstreams[1].closeCodes).toContain(1001);
		expect(() => vi.advanceTimersByTime(multiPolicy.closeGraceMs)).not.toThrow();
		expect(relay.snapshot()).toEqual({ pending: 0, active: 1, total: 1 });
		expect(browsers[0].listenerCount('error')).toBeGreaterThan(0);
		expect(browsers[1].listenerCount('error')).toBe(0);
		expect(upstreams[1].listenerCount('error')).toBe(0);
		browsers[0].remoteClose(1006);
		expect(relay.snapshot()).toEqual({ pending: 0, active: 0, total: 0 });
	});
});
