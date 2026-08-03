import { describe, expect, it } from 'vitest';

import { rolledDelivery } from '../player-v1/fixtures';
import type { TurnDelivery } from '../player-v1/parser';
import type { SessionState } from './session-machine';
import { projectSessionView } from './session-view';

const authenticated = {
	authenticationGeneration: 1,
	connectionGeneration: 2,
	operationGeneration: 3,
	sessionCsrf: 'csrf'
} as const;

function state(value: Partial<SessionState> & Pick<SessionState, 'tag'>): SessionState {
	return { ...authenticated, ...value } as SessionState;
}

describe('session view projection', () => {
	it.each([
		[
			state({
				tag: 'watermarking',
				intent: { text: 'Open the gate', idempotencyKey: 'key-1' },
				operation: { kind: 'latest', generation: 3, purpose: 'watermark' }
			}),
			'Checking prior activity.'
		],
		[
			state({
				tag: 'submitting',
				intent: { text: 'Open the gate', idempotencyKey: 'key-1', watermark: 'empty' },
				operation: { kind: 'submit', generation: 3, replay: false }
			}),
			'Submitting action.'
		],
		[
			state({
				tag: 'waiting',
				intent: {
					text: 'Open the gate',
					idempotencyKey: 'key-1',
					watermark: 'empty',
					ids: { actionId: 'act-1', turnId: 'turn-1' }
				}
			}),
			'Waiting for resolution.'
		],
		[state({ tag: 'reconnecting', resume: { kind: 'idle' } }), 'Connection interrupted.'],
		[
			state({
				tag: 'recovering_evidence',
				intent: { text: 'Open', idempotencyKey: 'key-1', watermark: 'empty' },
				operation: { kind: 'latest', generation: 3, purpose: 'evidence' }
			}),
			'Checking player activity without assuming correlation.'
		],
		[
			state({
				tag: 'terminal',
				intent: {
					text: 'Open',
					idempotencyKey: 'key-1',
					watermark: 'empty',
					ids: { actionId: 'act-1', turnId: 'turn-act-1' }
				},
				delivery: rolledDelivery as unknown as TurnDelivery
			}),
			'Resolution received.'
		]
	] as const)('gives %s a phase-specific live status', (session, status) => {
		const projected = projectSessionView(session, 'draft');

		expect(projected.action.liveStatus?.text).toBe(status);
		expect(projected.action.liveStatus?.announcementId).toContain(session.tag);
		expect(projected.action.busy).toBe(
			session.tag !== 'reconnecting' && session.tag !== 'terminal'
		);
		expect(projected.action.disabled).toBe(true);
	});

	it('projects reconnect, refusal, recovery, acknowledgement, and fail-closed controls', () => {
		const reconnecting = projectSessionView(
			state({ tag: 'reconnecting', resume: { kind: 'idle' }, transportFailure: 'secret' }),
			'draft'
		);
		expect(reconnecting.action.reconnect).toEqual({
			text: 'Connection interrupted.',
			available: true
		});

		const refused = projectSessionView(
			state({
				tag: 'refused',
				intent: { text: 'Open', idempotencyKey: 'key-1' },
				refusal: { code: 'invalid_field', message: 'Describe a concrete action.', field: 'text' }
			}),
			'draft'
		);
		expect(refused.action.refusal?.message).toBe('Describe a concrete action.');
		expect(refused.action.inputDisabled).toBe(false);
		expect(refused.action.submitDisabled).toBe(true);
		expect(refused.canAcknowledgeRefusal).toBe(true);

		const recovery = projectSessionView(
			state({
				tag: 'recovery_required',
				intent: { text: 'Open', idempotencyKey: 'key-1', watermark: 'empty' },
				replayExplanation: 'Use the reducer explanation exactly.'
			}),
			'draft'
		);
		expect(recovery.replayExplanation).toBe('Use the reducer explanation exactly.');
		expect(recovery.canAuthorizeReplay).toBe(true);

		const protocol = projectSessionView(
			state({
				tag: 'protocol_error',
				protocolFailure: { kind: 'correlation', message: 'sensitive detail' }
			}),
			'draft'
		);
		expect(protocol.protocolError).toBe('Player protocol error. Session controls are disabled.');
		expect(protocol.action.disabled).toBe(true);
		expect(protocol.resolution).toBeUndefined();
		expect(JSON.stringify(protocol)).not.toContain('sensitive detail');
	});

	it('projects only the machine current terminal delivery into one presentation view', () => {
		const projected = projectSessionView(
			state({
				tag: 'terminal',
				intent: {
					text: 'Open',
					idempotencyKey: 'key-1',
					watermark: 'empty',
					ids: { actionId: 'act-1', turnId: 'turn-act-1' }
				},
				delivery: rolledDelivery as unknown as TurnDelivery
			}),
			'draft'
		);

		expect(projected.resolution).toMatchObject({
			turn_id: 'turn-act-1',
			action_id: 'act-1',
			companion_resolution: {
				companion_id: 'c360.semmachina.world1.starter.character.wren',
				kind: 'hint',
				hint_level: 'connect'
			}
		});
	});
});
