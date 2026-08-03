export const ACTION_ID = 'act-1';
export const TURN_ID = `turn-${ACTION_ID}`;
export const PLAYER_ID = 'c360.semmachina.world1.starter.player.p1';
export const COMPANION_ID = 'c360.semmachina.world1.starter.character.wren';
export const RESOLVED_AT = '2026-07-28T09:15:30.123456Z';

export const acceptedFrame = {
	protocol: 'player/v1',
	type: 'submit_response',
	response: {
		protocol: 'player/v1',
		status: 'accepted',
		idempotency_key: 'key-1',
		action_id: ACTION_ID,
		turn_id: TURN_ID,
		arrived_at: RESOLVED_AT
	}
} as const;

export const refusedFrame = {
	protocol: 'player/v1',
	type: 'submit_response',
	response: {
		protocol: 'player/v1',
		status: 'refused',
		idempotency_key: 'key-1',
		refusal: {
			code: 'invalid_field',
			message: 'text is required',
			field: 'text'
		}
	}
} as const;

export const rolledDelivery = {
	protocol: 'player/v1',
	result: {
		protocol: 'player/v1',
		turn_id: TURN_ID,
		action_id: ACTION_ID,
		player_id: PLAYER_ID,
		phase: 'complete',
		resolution: {
			verdict: {
				plausibility: 'plausible',
				risk: 'high',
				consequence: 'harm',
				requires_roll: true
			},
			band: 'partial',
			roll: {
				mechanic: '2d6-pbta/v1',
				dice: [4, 4],
				modifiers: [{ source: 'equipment', value: 1, note: 'crowbar' }],
				modifier_total: 1,
				total: 9
			}
		},
		companion_resolution: {
			companion_id: COMPANION_ID,
			kind: 'hint',
			hint_level: 'connect'
		},
		narration_ref: `obj://ARTIFACTS/narration/${TURN_ID}`,
		resolved_at: RESOLVED_AT
	},
	narration: {
		turn_id: TURN_ID,
		band: 'partial',
		prose: 'The hinges scream, but the gate gives.'
	}
} as const;

export const noRollDelivery = {
	...rolledDelivery,
	result: {
		...rolledDelivery.result,
		resolution: {
			verdict: {
				plausibility: 'certain',
				risk: 'none',
				consequence: 'none',
				requires_roll: false
			},
			band: 'auto'
		}
	},
	narration: { ...rolledDelivery.narration, band: 'auto' }
} as const;

export const failedDelivery = {
	protocol: 'player/v1',
	result: {
		protocol: 'player/v1',
		turn_id: TURN_ID,
		action_id: ACTION_ID,
		player_id: PLAYER_ID,
		phase: 'failed',
		failure_reason: 'effect-invalid',
		resolved_at: RESOLVED_AT
	}
} as const;

export const deliveryFrame = {
	protocol: 'player/v1',
	type: 'turn_delivery',
	delivery: rolledDelivery
} as const;

export const failedDeliveryFrame = {
	protocol: 'player/v1',
	type: 'turn_delivery',
	delivery: failedDelivery
} as const;

export const retrievalFoundFrame = {
	protocol: 'player/v1',
	type: 'retrieve_response',
	retrieval: {
		protocol: 'player/v1',
		status: 'found',
		by: 'turn',
		id: TURN_ID,
		delivery: rolledDelivery
	}
} as const;

export const retrievalRefusedFrame = {
	protocol: 'player/v1',
	type: 'retrieve_response',
	retrieval: {
		protocol: 'player/v1',
		status: 'refused',
		by: 'latest',
		refusal: { code: 'not_ready', message: 'the turn is still running' }
	}
} as const;

export const operationRefusedFrame = {
	protocol: 'player/v1',
	type: 'operation_response',
	operation: {
		protocol: 'player/v1',
		status: 'refused',
		refusal: {
			code: 'unsupported_operation',
			operation: 'cast_spell',
			message: 'operation is not supported'
		}
	}
} as const;

type DeepMutable<T> = T extends string
	? string
	: T extends number
		? number
		: T extends boolean
			? boolean
			: T extends readonly (infer Item)[]
				? DeepMutable<Item>[]
				: T extends object
					? { -readonly [Key in keyof T]: DeepMutable<T[Key]> }
					: T;

export function mutableFixture<T>(fixture: T): DeepMutable<T> {
	return structuredClone(fixture) as DeepMutable<T>;
}
