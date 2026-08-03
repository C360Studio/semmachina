export interface PlayerRelayPolicy {
	readonly browserMessageBytes: number;
	readonly upstreamMessageBytes: number;
	readonly processBridges: number;
	readonly perSessionBridges: number;
	readonly outstandingMessages: number;
	readonly browserToUpstreamBytes: number;
	readonly upstreamToBrowserBytes: number;
	readonly handshakeMs: number;
	readonly writeMs: number;
	readonly pingMs: number;
	readonly pongMs: number;
	readonly closeGraceMs: number;
}

export const DEFAULT_PLAYER_RELAY_POLICY: PlayerRelayPolicy = Object.freeze({
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

export interface BridgeReservation {
	readonly activate: () => void;
	readonly release: () => void;
}

export interface BridgeCapacity {
	readonly reserve: (sessionIdentity: object) => BridgeReservation | null;
	readonly snapshot: () => { pending: number; active: number; total: number };
}

export function createBridgeCapacity(policy: PlayerRelayPolicy): BridgeCapacity {
	let pending = 0;
	let active = 0;
	const perSession = new Map<object, number>();

	function reserve(sessionIdentity: object): BridgeReservation | null {
		const sessionCount = perSession.get(sessionIdentity) ?? 0;
		if (pending + active >= policy.processBridges || sessionCount >= policy.perSessionBridges) {
			return null;
		}
		pending += 1;
		perSession.set(sessionIdentity, sessionCount + 1);
		let state: 'pending' | 'active' | 'released' = 'pending';
		return Object.freeze({
			activate() {
				if (state !== 'pending') return;
				pending -= 1;
				active += 1;
				state = 'active';
			},
			release() {
				if (state === 'released') return;
				if (state === 'pending') pending -= 1;
				if (state === 'active') active -= 1;
				const remaining = (perSession.get(sessionIdentity) ?? 1) - 1;
				if (remaining === 0) perSession.delete(sessionIdentity);
				else perSession.set(sessionIdentity, remaining);
				state = 'released';
			}
		});
	}

	return Object.freeze({
		reserve,
		snapshot: () => Object.freeze({ pending, active, total: pending + active })
	});
}
