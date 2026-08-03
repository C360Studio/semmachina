import { describe, expect, it } from 'vitest';

import { DEFAULT_PLAYER_RELAY_POLICY, createBridgeCapacity } from './player-relay-policy';

describe('player relay policy', () => {
	it('pins every reviewed resource and liveness bound', () => {
		expect(DEFAULT_PLAYER_RELAY_POLICY).toEqual({
			browserMessageBytes: 25_856,
			upstreamMessageBytes: 262_144,
			processBridges: 4,
			perSessionBridges: 2,
			outstandingMessages: 8,
			browserToUpstreamBytes: 131_072,
			upstreamToBrowserBytes: 524_288,
			handshakeMs: 10_000,
			writeMs: 10_000,
			pingMs: 30_000,
			pongMs: 15_000,
			closeGraceMs: 5_000
		});
		expect(Object.isFrozen(DEFAULT_PLAYER_RELAY_POLICY)).toBe(true);
	});

	it('reserves pending plus active capacity and refuses the newest before work starts', () => {
		const capacity = createBridgeCapacity({
			...DEFAULT_PLAYER_RELAY_POLICY,
			processBridges: 2,
			perSessionBridges: 1
		});
		const sessionA = Object.freeze({});
		const sessionB = Object.freeze({});
		const first = capacity.reserve(sessionA);
		expect(first).not.toBeNull();
		expect(capacity.reserve(sessionA)).toBeNull();
		const second = capacity.reserve(sessionB);
		expect(second).not.toBeNull();
		expect(capacity.reserve(Object.freeze({}))).toBeNull();
		expect(capacity.snapshot()).toEqual({ pending: 2, active: 0, total: 2 });
		first?.activate();
		expect(capacity.snapshot()).toEqual({ pending: 1, active: 1, total: 2 });
		first?.release();
		first?.release();
		expect(capacity.snapshot()).toEqual({ pending: 1, active: 0, total: 1 });
		expect(capacity.reserve(sessionA)).not.toBeNull();
		second?.release();
	});
});
