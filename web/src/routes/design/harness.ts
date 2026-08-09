import { mutableFixture, rolledDelivery } from '$lib/player-v1/fixtures';
import type { RolledOutcomeBand, TurnDelivery } from '$lib/player-v1/parser';
import type { SessionController } from '$lib/player-session/session-controller';
import type { SessionEvent, SessionState } from '$lib/player-session/session-machine';
import type { WorldProjection } from '$lib/server/world-projection';

const WORLD_PREFIX = 'c360.semmachina.demo.bellweather-maze.location';

const place = (slug: string, label: string, connections: readonly string[]) => ({
	id: `${WORLD_PREFIX}.${slug}`,
	label,
	connections: connections.map((target) => `${WORLD_PREFIX}.${target}`)
});

export const bellweatherWorld: WorldProjection = {
	places: [
		place('fete-green', 'Fete Green', ['prize-maze', 'parish-hall', 'river-cottage']),
		place('prize-maze', 'Prize Maze', []),
		place('parish-hall', 'Parish Hall', ['bell-tower']),
		place('bell-tower', 'Bell Tower', []),
		place('river-cottage', 'River Cottage', ['fete-green'])
	],
	clock: { state: 'configured', label: 'Fete day', value: 1, unit: 'days' }
};

const counters = {
	authenticationGeneration: 1,
	connectionGeneration: 1,
	operationGeneration: 0
} as const;

export const signedOut: SessionState = {
	tag: 'signed_out',
	authenticationGeneration: 0,
	connectionGeneration: 0,
	operationGeneration: 0
};

const idle = (operationGeneration: number): SessionState => ({
	...counters,
	operationGeneration,
	tag: 'idle',
	sessionCsrf: 'harness-csrf'
});

interface NarrationVariant {
	readonly playerText: string;
	readonly band: RolledOutcomeBand;
	readonly prose: string;
}

// Canned Bellweather-flavored beats, rotated by generation so the demo's
// UI-side turn history (CreatorSurface) shows real variety instead of one
// resolution repeated verbatim.
const NARRATION_VARIANTS: readonly NarrationVariant[] = [
	{
		playerText: 'Examine the service gate latch',
		band: 'partial',
		prose:
			'The hinges scream, but the gate gives — beyond it the prize maze breathes, hedges cut close and the grass still trampled from someone else’s hurry.'
	},
	{
		playerText: 'Follow Kit Finch into the maze',
		band: 'full',
		prose:
			'Kit Finch ducks between the hedges without a sound, and you are right behind — the maze’s turns open for you both like it remembers the way.'
	},
	{
		playerText: 'Force the service gate latch',
		band: 'miss',
		prose:
			'The latch snaps against your grip. Somewhere past the hedge, footsteps hurry away — whoever was there has heard you now.'
	},
	{
		playerText: 'Ask Kit Finch about the maze entrance',
		band: 'partial',
		prose:
			'Kit Finch keeps watch at the maze entrance while you work the latch. It gives, grudgingly — but not before the bell tower strikes the hour and every eye at the fete turns your way.'
	}
];

// rolledDelivery's own ids are fixed; stamping unique per-generation ids is
// what lets consecutive turns accumulate in CreatorSurface's history instead
// of colliding on one repeated [turn_id, action_id] identity.
function idsForGeneration(operationGeneration: number): { actionId: string; turnId: string } {
	const actionId = `act-${operationGeneration}`;
	return { actionId, turnId: `turn-${actionId}` };
}

function narrationVariantFor(operationGeneration: number): NarrationVariant {
	return NARRATION_VARIANTS[operationGeneration % NARRATION_VARIANTS.length];
}

// Clones rolledDelivery's fixture shape, restamps its identity to match the
// generation's ids, and swaps in a rotating narration/band variant so the
// demo log shows variety instead of one resolution repeated verbatim.
function deliveryFor(actionId: string, turnId: string, variant: NarrationVariant): TurnDelivery {
	const delivery = mutableFixture(rolledDelivery);
	delivery.result.action_id = actionId;
	delivery.result.turn_id = turnId;
	delivery.result.narration_ref = `obj://ARTIFACTS/narration/${turnId}`;
	delivery.result.resolution.band = variant.band;
	delivery.narration.turn_id = turnId;
	delivery.narration.band = variant.band;
	delivery.narration.prose = variant.prose;
	return delivery as unknown as TurnDelivery;
}

const terminal = (operationGeneration: number): SessionState => {
	const variant = narrationVariantFor(operationGeneration);
	const { actionId, turnId } = idsForGeneration(operationGeneration);
	return {
		...counters,
		operationGeneration,
		tag: 'terminal',
		sessionCsrf: 'harness-csrf',
		intent: {
			text: variant.playerText,
			idempotencyKey: `harness-${operationGeneration}`,
			watermark: 'empty',
			ids: { actionId, turnId }
		},
		delivery: deliveryFor(actionId, turnId, variant)
	};
};

export const refused = (operationGeneration: number): SessionState => ({
	...counters,
	operationGeneration,
	tag: 'refused',
	sessionCsrf: 'harness-csrf',
	intent: { text: 'Accuse everyone at once', idempotencyKey: `harness-${operationGeneration}` },
	refusal: {
		code: 'turn_in_progress',
		message: 'A turn is already resolving. Wait for its resolution before acting again.'
	}
});

export const reconnecting = (operationGeneration: number): SessionState => ({
	...counters,
	operationGeneration,
	tag: 'reconnecting',
	sessionCsrf: 'harness-csrf',
	resume: { kind: 'idle' },
	transportFailure: 'transport closed'
});

/**
 * Scripted stand-in for the production controller: plays the happy-path
 * session loop with short delays so the surface can be exercised without a
 * running world. State jumps for edge states are exposed via `emit`.
 */
export class HarnessController implements SessionController {
	#state: SessionState = signedOut;
	#subscribers = new Set<(state: SessionState) => void>();
	#timers = new Set<ReturnType<typeof setTimeout>>();
	#generation = 0;

	getState(): SessionState {
		return this.#state;
	}

	subscribe(subscriber: (state: SessionState) => void): () => void {
		this.#subscribers.add(subscriber);
		subscriber(this.#state);
		return () => this.#subscribers.delete(subscriber);
	}

	emit(state: SessionState): void {
		this.#state = state;
		for (const subscriber of this.#subscribers) subscriber(state);
	}

	destroy(): void {
		for (const timer of this.#timers) clearTimeout(timer);
		this.#timers.clear();
		this.#subscribers.clear();
	}

	dispatch(event: SessionEvent): void {
		switch (event.type) {
			case 'AuthenticateRequested':
				this.emit({ ...counters, operationGeneration: this.#generation, tag: 'authenticating' });
				this.#after(500, () =>
					this.emit({
						...counters,
						operationGeneration: this.#generation,
						tag: 'connecting',
						sessionCsrf: 'harness-csrf',
						resume: { kind: 'idle' }
					})
				);
				this.#after(900, () => this.emit(idle(this.#generation)));
				break;
			case 'IntentCreated': {
				const generation = ++this.#generation;
				this.emit({
					...counters,
					operationGeneration: generation,
					tag: 'waiting',
					sessionCsrf: 'harness-csrf',
					intent: {
						text: event.text,
						idempotencyKey: event.idempotencyKey,
						watermark: 'empty',
						ids: idsForGeneration(generation)
					}
				});
				this.#after(1200, () => this.emit(terminal(generation)));
				break;
			}
			case 'TerminalAcknowledged':
			case 'RefusalAcknowledged':
				this.emit(idle(++this.#generation));
				break;
			case 'ReconnectRequested':
				this.emit({
					...counters,
					operationGeneration: ++this.#generation,
					tag: 'connecting',
					sessionCsrf: 'harness-csrf',
					resume: { kind: 'idle' }
				});
				this.#after(700, () => this.emit(idle(this.#generation)));
				break;
			default:
				break;
		}
	}

	#after(delay: number, run: () => void): void {
		const timer = setTimeout(() => {
			this.#timers.delete(timer);
			run();
		}, delay);
		this.#timers.add(timer);
	}
}
