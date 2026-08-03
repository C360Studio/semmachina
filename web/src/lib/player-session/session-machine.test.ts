import { describe, expect, expectTypeOf, it } from 'vitest';

import type { RetrieveResponse, SubmitResponse, TurnDelivery } from '../player-v1/parser';
import {
	createSessionState,
	reduceSession,
	type SessionEvent,
	type SessionState
} from './session-machine';

const accepted = (actionId = 'action:one', turnId = 'turn:one'): SubmitResponse => ({
	protocol: 'player/v1',
	status: 'accepted',
	idempotency_key: 'intent-key',
	action_id: actionId,
	turn_id: turnId,
	arrived_at: '2026-08-03T12:00:00Z'
});

const delivery = (actionId = 'action:one', turnId = 'turn:one', prose = 'Done.'): TurnDelivery => ({
	protocol: 'player/v1',
	result: {
		protocol: 'player/v1',
		phase: 'complete',
		action_id: actionId,
		turn_id: turnId,
		player_id: 'player:one',
		resolved_at: '2026-08-03T12:00:01Z',
		resolution: {
			verdict: {
				plausibility: 'certain',
				risk: 'none',
				consequence: 'none',
				requires_roll: false
			},
			band: 'auto'
		},
		narration_ref: 'narration:one'
	},
	narration: { turn_id: turnId, band: 'auto', prose }
});

const found = (
	by: 'latest' | 'turn' | 'action',
	value: TurnDelivery,
	id?: string
): RetrieveResponse => ({
	protocol: 'player/v1',
	status: 'found',
	by,
	...(id === undefined ? {} : { id }),
	delivery: value
});

const refusedLatest = (code: 'not_found' | 'not_ready' | 'unavailable'): RetrieveResponse => ({
	protocol: 'player/v1',
	status: 'refused',
	by: 'latest',
	refusal: { code, message: code }
});

const dispatch = (state: SessionState, event: SessionEvent): SessionState =>
	reduceSession(state, event).state;

function connected(): SessionState {
	let state = createSessionState();
	state = dispatch(state, {
		type: 'AuthenticateRequested',
		credential: 'creator-secret'
	});
	state = dispatch(state, {
		type: 'Authenticated',
		authenticationGeneration: 1,
		sessionCsrf: 'session-csrf'
	});
	return dispatch(state, { type: 'SocketOpened', connectionGeneration: 1 });
}

function watermarking(): SessionState {
	return dispatch(connected(), {
		type: 'IntentCreated',
		text: 'Open the iron door',
		idempotencyKey: 'intent-key'
	});
}

function submittingWithEmptyWatermark(): SessionState {
	return dispatch(watermarking(), {
		type: 'RetrieveAnswered',
		connectionGeneration: 1,
		operationGeneration: 1,
		response: refusedLatest('not_found')
	});
}

describe('player session machine', () => {
	it('exposes phase-specific state contracts', () => {
		type TerminalState = Extract<SessionState, { tag: 'terminal' }>;
		type WaitingState = Extract<SessionState, { tag: 'waiting' }>;
		type WatermarkingState = Extract<SessionState, { tag: 'watermarking' }>;
		expectTypeOf<TerminalState['delivery']>().toEqualTypeOf<TurnDelivery>();
		expectTypeOf<WaitingState['intent']['ids']>().toEqualTypeOf<{
			readonly actionId: string;
			readonly turnId: string;
		}>();
		expectTypeOf<WatermarkingState['operation']>().toMatchTypeOf<{
			kind: 'latest';
			purpose: 'watermark';
		}>();
		expectTypeOf<Omit<TerminalState, 'delivery'>>().not.toMatchTypeOf<SessionState>();
		expectTypeOf<
			Omit<WaitingState, 'intent'> & { intent: Omit<WaitingState['intent'], 'ids'> }
		>().not.toMatchTypeOf<SessionState>();
		expectTypeOf<Omit<WatermarkingState, 'operation'>>().not.toMatchTypeOf<SessionState>();
		expect(true).toBe(true);
	});

	it('authenticates without retaining the credential, connects, then submits after watermarking', () => {
		const initial = createSessionState();
		expect(reduceSession(initial, { type: 'ReconnectRequested' })).toEqual({
			state: initial,
			effects: []
		});
		const authenticating = reduceSession(initial, {
			type: 'AuthenticateRequested',
			credential: 'creator-secret'
		});
		expect(authenticating.state.tag).toBe('authenticating');
		expect(authenticating.state).not.toHaveProperty('credential');
		expect(authenticating.effects).toEqual([
			{ type: 'Authenticate', authenticationGeneration: 1, credential: 'creator-secret' }
		]);
		const connecting = reduceSession(authenticating.state, {
			type: 'Authenticated',
			authenticationGeneration: 1,
			sessionCsrf: 'session-csrf'
		});
		expect(connecting.state.tag).toBe('connecting');
		expect(connecting.effects).toEqual([
			{ type: 'OpenSocket', connectionGeneration: 1, sessionCsrf: 'session-csrf' }
		]);

		const idle = reduceSession(connecting.state, {
			type: 'SocketOpened',
			connectionGeneration: 1
		});
		expect(idle.state.tag).toBe('idle');

		const checking = reduceSession(idle.state, {
			type: 'IntentCreated',
			text: 'Open the iron door',
			idempotencyKey: 'intent-key'
		});
		expect(checking.state.tag).toBe('watermarking');
		expect(checking.effects).toEqual([
			{
				type: 'SendRetrieveLatest',
				connectionGeneration: 1,
				operationGeneration: 1
			}
		]);

		const sending = reduceSession(checking.state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 1,
			operationGeneration: 1,
			response: refusedLatest('not_found')
		});
		expect(sending.state).toMatchObject({
			tag: 'submitting',
			intent: { text: 'Open the iron door', idempotencyKey: 'intent-key', watermark: 'empty' }
		});
		expect(sending.effects).toEqual([
			{
				type: 'SendSubmit',
				connectionGeneration: 1,
				operationGeneration: 2,
				text: 'Open the iron door',
				idempotencyKey: 'intent-key'
			}
		]);
	});

	it('returns a typed authentication refusal to signed out', () => {
		const authenticating = dispatch(createSessionState(), {
			type: 'AuthenticateRequested',
			credential: 'wrong'
		});
		const result = reduceSession(authenticating, {
			type: 'AuthenticationRefused',
			authenticationGeneration: 1,
			refusal: { code: 'authentication_refused', message: 'Authentication refused' }
		});
		expect(result.state).toEqual({
			tag: 'signed_out',
			authenticationGeneration: 1,
			connectionGeneration: 0,
			operationGeneration: 0,
			authenticationRefusal: {
				code: 'authentication_refused',
				message: 'Authentication refused'
			}
		});
		expect(result.effects).toEqual([]);
	});

	it('records a found terminal as a watermark before the initial submit', () => {
		const previous = delivery('action:previous', 'turn:previous');
		const result = reduceSession(watermarking(), {
			type: 'RetrieveAnswered',
			connectionGeneration: 1,
			operationGeneration: 1,
			response: found('latest', previous)
		});
		expect(result.state).toMatchObject({
			tag: 'submitting',
			intent: {
				watermark: { actionId: 'action:previous', turnId: 'turn:previous' }
			}
		});
		expect(result.effects[0]).toMatchObject({ type: 'SendSubmit' });
	});

	it.each(['not_ready', 'unavailable'] as const)('blocks submit when latest is %s', (code) => {
		const result = reduceSession(watermarking(), {
			type: 'RetrieveAnswered',
			connectionGeneration: 1,
			operationGeneration: 1,
			response: refusedLatest(code)
		});
		expect(result.state).toMatchObject({ tag: 'refused', refusal: { code } });
		expect(result.effects).toEqual([]);
	});

	it('moves accepted submission to waiting and preserves structured refusal', () => {
		const pending = reduceSession(submittingWithEmptyWatermark(), {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		expect(pending.state).toMatchObject({
			tag: 'waiting',
			intent: { ids: { actionId: 'action:one', turnId: 'turn:one' } }
		});

		const refusal = reduceSession(submittingWithEmptyWatermark(), {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: {
				protocol: 'player/v1',
				status: 'refused',
				idempotency_key: 'intent-key',
				refusal: { code: 'invalid_field', field: 'text', message: 'invalid' }
			}
		});
		expect(refusal.state).toMatchObject({
			tag: 'refused',
			refusal: { code: 'invalid_field', field: 'text' }
		});
	});

	it('disables a second intent and resolves only exact matching delivery', () => {
		let state = submittingWithEmptyWatermark();
		const ignored = reduceSession(state, {
			type: 'IntentCreated',
			text: 'Different action',
			idempotencyKey: 'different-key'
		});
		expect(ignored.state).toBe(state);
		expect(ignored.effects).toEqual([]);

		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		const terminal = reduceSession(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 1,
			delivery: delivery()
		});
		expect(terminal.state).toMatchObject({ tag: 'terminal', delivery: delivery() });
	});

	it('reconnects an unknown-ID intent through latest evidence without automatic submit', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		expect(state.tag).toBe('reconnecting');
		expect('ids' in (state.intent ?? {})).toBe(false);
		const reconnect = reduceSession(state, { type: 'ReconnectRequested' });
		expect(reconnect.effects).toEqual([
			{ type: 'OpenSocket', connectionGeneration: 2, sessionCsrf: 'session-csrf' }
		]);
		const recovery = reduceSession(reconnect.state, {
			type: 'SocketOpened',
			connectionGeneration: 2
		});
		expect(recovery.state.tag).toBe('recovering_evidence');
		expect(recovery.effects).toEqual([
			{
				type: 'SendRetrieveLatest',
				connectionGeneration: 2,
				operationGeneration: 3
			}
		]);
		expect(recovery.effects.some((effect) => effect.type === 'SendSubmit')).toBe(false);
	});

	it('reconnects a known-ID intent with exact turn retrieval', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		const result = reduceSession(state, {
			type: 'SocketOpened',
			connectionGeneration: 2
		});
		expect(result.state.tag).toBe('recovering_exact');
		expect(result.effects).toEqual([
			{
				type: 'SendRetrieveExact',
				by: 'turn',
				id: 'turn:one',
				connectionGeneration: 2,
				operationGeneration: 3
			}
		]);
	});

	it('treats previous, other-device latest, delivery, and active_turn_id as evidence only', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });

		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 2,
			delivery: delivery('action:other', 'turn:other')
		});
		expect(state.tag).toBe('recovering_evidence');

		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: found('latest', delivery('action:other', 'turn:other'))
		});
		expect(state).toMatchObject({
			tag: 'recovery_required',
			evidence: { latestChanged: true }
		});
		expect('ids' in (state.intent ?? {})).toBe(false);

		const replaying = reduceSession(state, { type: 'ReplayAuthorized' });
		const refused = reduceSession(replaying.state, {
			type: 'SubmitAnswered',
			connectionGeneration: 2,
			operationGeneration: 4,
			response: {
				protocol: 'player/v1',
				status: 'refused',
				idempotency_key: 'intent-key',
				refusal: {
					code: 'turn_in_progress',
					message: 'busy',
					active_turn_id: 'turn:other'
				}
			}
		});
		expect(refused.state).toMatchObject({
			tag: 'recovery_required',
			replayRefusal: { code: 'turn_in_progress', active_turn_id: 'turn:other' }
		});
		expect('ids' in (refused.state.intent ?? {})).toBe(false);
	});

	it('replays retained text and key exactly once only after authorization', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: refusedLatest('not_found')
		});
		expect(state).toMatchObject({
			tag: 'recovery_required',
			replayExplanation: expect.stringContaining('idempotently')
		});

		const replay = reduceSession(state, { type: 'ReplayAuthorized' });
		expect(replay.effects).toEqual([
			{
				type: 'SendSubmit',
				connectionGeneration: 2,
				operationGeneration: 4,
				text: 'Open the iron door',
				idempotencyKey: 'intent-key'
			}
		]);
		expect(reduceSession(replay.state, { type: 'ReplayAuthorized' }).effects).toEqual([]);

		const acceptedReplay = reduceSession(replay.state, {
			type: 'SubmitAnswered',
			connectionGeneration: 2,
			operationGeneration: 4,
			response: accepted()
		});
		state = acceptedReplay.state;
		expect(state).toMatchObject({
			tag: 'recovering_exact',
			intent: { ids: { turnId: 'turn:one' } }
		});
		expect(state.operation).toMatchObject({ kind: 'exact', generation: 5, id: 'turn:one' });
		expect(acceptedReplay.effects).toEqual([
			{
				type: 'SendRetrieveExact',
				connectionGeneration: 2,
				operationGeneration: 5,
				by: 'turn',
				id: 'turn:one'
			}
		]);
	});

	it('coalesces equal terminal evidence and rejects conflicting known content', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 1,
			delivery: delivery()
		});
		const duplicate = reduceSession(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 1,
			delivery: delivery()
		});
		expect(duplicate.state).toBe(state);

		const conflict = reduceSession(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 1,
			delivery: delivery('action:one', 'turn:one', 'Contradiction.')
		});
		expect(conflict.state).toMatchObject({ tag: 'protocol_error' });
	});

	it('ignores stale connection/operation events and fails closed on current correlation mismatch', () => {
		const state = watermarking();
		expect(
			reduceSession(state, {
				type: 'SocketClosed',
				connectionGeneration: 0
			}).state
		).toBe(state);
		expect(
			reduceSession(state, {
				type: 'RetrieveAnswered',
				connectionGeneration: 1,
				operationGeneration: 0,
				response: refusedLatest('not_found')
			}).state
		).toBe(state);

		const wrongBy = reduceSession(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 1,
			operationGeneration: 1,
			response: {
				protocol: 'player/v1',
				status: 'refused',
				by: 'turn',
				id: 'turn:other',
				refusal: { code: 'not_found', message: 'missing' }
			}
		});
		expect(wrongBy.state).toMatchObject({ tag: 'protocol_error' });

		const wrongKind = reduceSession(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 1,
			response: accepted()
		});
		expect(wrongKind.state).toMatchObject({ tag: 'protocol_error' });
	});

	it('restarts watermarking after a disconnect instead of treating the unsent intent as uncertain', () => {
		let state = watermarking();
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		expect(state).toMatchObject({ tag: 'reconnecting', resume: { kind: 'watermark' } });
		state = dispatch(state, { type: 'ReconnectRequested' });
		const opened = reduceSession(state, { type: 'SocketOpened', connectionGeneration: 2 });
		expect(opened.state.tag).toBe('watermarking');
		expect(opened.effects).toEqual([
			{
				type: 'SendRetrieveLatest',
				connectionGeneration: 2,
				operationGeneration: 2
			}
		]);
	});

	it('preserves a definitive refusal across reconnect and acknowledges it explicitly', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: {
				protocol: 'player/v1',
				status: 'refused',
				refusal: { code: 'invalid_field', field: 'text', message: 'invalid' }
			}
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		expect(state).toMatchObject({ tag: 'reconnecting', resume: { kind: 'refused' } });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		expect(state).toMatchObject({ tag: 'refused', refusal: { code: 'invalid_field' } });
		state = dispatch(state, { type: 'RefusalAcknowledged' });
		expect(state.tag).toBe('idle');
		const next = reduceSession(state, {
			type: 'IntentCreated',
			text: 'Use the brass key',
			idempotencyKey: 'second-key'
		});
		expect(next.state.tag).toBe('watermarking');
	});

	it('keeps an initial active-turn refusal unresolved without adopting its turn ID', () => {
		const result = reduceSession(submittingWithEmptyWatermark(), {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: {
				protocol: 'player/v1',
				status: 'refused',
				idempotency_key: 'intent-key',
				refusal: {
					code: 'turn_in_progress',
					message: 'busy',
					active_turn_id: 'turn:other'
				}
			}
		});
		expect(result.state).toMatchObject({
			tag: 'recovery_required',
			replayRefusal: { code: 'turn_in_progress', active_turn_id: 'turn:other' }
		});
		expect('ids' in (result.state.intent ?? {})).toBe(false);
		expect(reduceSession(result.state, { type: 'ReplayAuthorized' }).effects[0]).toMatchObject({
			type: 'SendSubmit',
			text: 'Open the iron door',
			idempotencyKey: 'intent-key'
		});
	});

	it.each(['not_ready', 'unavailable'] as const)(
		'preserves recovery latest %s as typed unavailable evidence',
		(code) => {
			let state = submittingWithEmptyWatermark();
			state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
			state = dispatch(state, { type: 'ReconnectRequested' });
			state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
			state = dispatch(state, {
				type: 'RetrieveAnswered',
				connectionGeneration: 2,
				operationGeneration: 3,
				response: refusedLatest(code)
			});
			expect(state).toMatchObject({
				tag: 'recovery_required',
				evidence: { latestRefusal: { code } }
			});
			if (state.tag !== 'recovery_required') throw new Error('expected recovery_required');
			expect(state.evidence).not.toHaveProperty('latestChanged');
		}
	);

	it('coalesces delivery-first exact recovery with the later solicited response', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 2,
			delivery: delivery()
		});
		expect(state).toMatchObject({ tag: 'terminal', pendingExact: { generation: 3 } });
		const coalesced = reduceSession(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: found('turn', delivery(), 'turn:one')
		});
		expect(coalesced.state).toMatchObject({ tag: 'terminal', delivery: delivery() });
		expect(coalesced.state).not.toHaveProperty('pendingExact');
	});

	it('coalesces retrieval-first exact recovery with a later delivery', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: found('turn', delivery(), 'turn:one')
		});
		expect(state.tag).toBe('terminal');
		const coalesced = reduceSession(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 2,
			delivery: delivery()
		});
		expect(coalesced.state).toBe(state);
	});

	it('correlates effect failures to the exact current operation', () => {
		const state = watermarking();
		expect(
			reduceSession(state, {
				type: 'EffectFailed',
				connectionGeneration: 1,
				operationGeneration: 0,
				message: 'old'
			}).state
		).toBe(state);
		expect(
			reduceSession(state, {
				type: 'EffectFailed',
				connectionGeneration: 1,
				operationGeneration: 2,
				message: 'future'
			}).state
		).toMatchObject({ tag: 'protocol_error' });
		const current = reduceSession(state, {
			type: 'EffectFailed',
			connectionGeneration: 1,
			operationGeneration: 1,
			message: 'send failed'
		});
		expect(current.state).toMatchObject({
			tag: 'reconnecting',
			resume: { kind: 'watermark' }
		});
	});

	it('preserves replay refusal evidence across another reconnect and unchanged latest result', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: {
				protocol: 'player/v1',
				status: 'refused',
				idempotency_key: 'intent-key',
				refusal: {
					code: 'turn_in_progress',
					message: 'busy',
					active_turn_id: 'turn:other'
				}
			}
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		expect(state).toMatchObject({
			tag: 'recovering_evidence',
			replayRefusal: { code: 'turn_in_progress', active_turn_id: 'turn:other' }
		});
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: refusedLatest('not_found')
		});
		expect(state).toMatchObject({
			tag: 'recovery_required',
			replayRefusal: { code: 'turn_in_progress', active_turn_id: 'turn:other' },
			evidence: { latestChanged: false }
		});
	});

	it('constructs each recovery phase without leaking stale phase properties', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 2,
			delivery: delivery('action:other', 'turn:other')
		});
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: found('latest', delivery('action:other', 'turn:other'))
		});
		expect(state.tag).toBe('recovery_required');
		expect(Object.hasOwn(state, 'operation')).toBe(false);

		const replay = reduceSession(state, { type: 'ReplayAuthorized' });
		expect(replay.state.tag).toBe('submitting');
		for (const stale of ['evidence', 'replayRefusal', 'refusal', 'replayExplanation']) {
			expect(Object.hasOwn(replay.state, stale)).toBe(false);
		}

		const exact = reduceSession(replay.state, {
			type: 'SubmitAnswered',
			connectionGeneration: 2,
			operationGeneration: 4,
			response: accepted()
		});
		expect(exact.state.tag).toBe('recovering_exact');
		for (const stale of ['evidence', 'replayRefusal', 'refusal', 'replayExplanation']) {
			expect(Object.hasOwn(exact.state, stale)).toBe(false);
		}
	});

	it('restores an authoritative terminal after reconnect and acknowledges it before a new intent', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 1,
			delivery: delivery()
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		expect(state).toMatchObject({ tag: 'reconnecting', resume: { kind: 'terminal' } });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		expect(state).toMatchObject({ tag: 'terminal', delivery: delivery() });
		expect(Object.hasOwn(state, 'pendingExact')).toBe(false);

		const idle = reduceSession(state, { type: 'TerminalAcknowledged' });
		expect(idle.state.tag).toBe('idle');
		expect(idle.effects).toEqual([]);
		const next = reduceSession(idle.state, {
			type: 'IntentCreated',
			text: 'Take another step',
			idempotencyKey: 'next-key'
		});
		expect(next.state.tag).toBe('watermarking');
	});

	it('logs out immediately from active phases and is a no-op while signed out', () => {
		const initial = createSessionState();
		expect(reduceSession(initial, { type: 'LogoutRequested' })).toEqual({
			state: initial,
			effects: []
		});

		let terminal = submittingWithEmptyWatermark();
		terminal = dispatch(terminal, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		terminal = dispatch(terminal, {
			type: 'DeliveryReceived',
			connectionGeneration: 1,
			delivery: delivery()
		});
		for (const active of [connected(), watermarking(), terminal]) {
			const result = reduceSession(active, { type: 'LogoutRequested' });
			expect(result.state).toEqual({
				tag: 'signed_out',
				authenticationGeneration: active.authenticationGeneration,
				connectionGeneration: active.connectionGeneration,
				operationGeneration: active.operationGeneration
			});
			expect(result.effects).toEqual([
				{ type: 'CloseSocket', connectionGeneration: active.connectionGeneration },
				{ type: 'Logout', sessionCsrf: 'session-csrf' }
			]);
		}
	});

	it('accepted replay exact retrieval reaches terminal', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: refusedLatest('not_found')
		});
		state = dispatch(state, { type: 'ReplayAuthorized' });
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 2,
			operationGeneration: 4,
			response: accepted()
		});
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 5,
			response: found('turn', delivery(), 'turn:one')
		});
		expect(state).toMatchObject({ tag: 'terminal', delivery: delivery() });
	});

	it('keeps delivery-first exact terminal when retrieval refuses and clears its tombstone', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 2,
			delivery: delivery()
		});
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: {
				protocol: 'player/v1',
				status: 'refused',
				by: 'turn',
				id: 'turn:one',
				refusal: { code: 'not_ready', message: 'not ready' }
			}
		});
		expect(state).toMatchObject({ tag: 'terminal', delivery: delivery() });
		expect(Object.hasOwn(state, 'pendingExact')).toBe(false);
	});

	it('fails closed when delivery-first exact retrieval returns conflicting known content', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 2,
			delivery: delivery()
		});
		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: found('turn', delivery('action:one', 'turn:one', 'Conflict.'), 'turn:one')
		});
		expect(state).toMatchObject({ tag: 'protocol_error' });
	});

	it('does not acknowledge a terminal until its pending exact response is consumed', () => {
		let state = submittingWithEmptyWatermark();
		state = dispatch(state, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		state = dispatch(state, { type: 'SocketClosed', connectionGeneration: 1 });
		state = dispatch(state, { type: 'ReconnectRequested' });
		state = dispatch(state, { type: 'SocketOpened', connectionGeneration: 2 });
		state = dispatch(state, {
			type: 'DeliveryReceived',
			connectionGeneration: 2,
			delivery: delivery()
		});
		const pending = state;
		expect(pending).toMatchObject({ tag: 'terminal', pendingExact: { generation: 3 } });
		expect(reduceSession(pending, { type: 'TerminalAcknowledged' }).state).toBe(pending);

		state = dispatch(state, {
			type: 'RetrieveAnswered',
			connectionGeneration: 2,
			operationGeneration: 3,
			response: found('turn', delivery(), 'turn:one')
		});
		expect(state.tag).toBe('terminal');
		expect(Object.hasOwn(state, 'pendingExact')).toBe(false);
		expect(dispatch(state, { type: 'TerminalAcknowledged' }).tag).toBe('idle');
	});

	it('correlates authentication results by reducer-owned generation', () => {
		const state = createSessionState();
		const first = reduceSession(state, {
			type: 'AuthenticateRequested',
			credential: 'first'
		});
		expect(first.state).toMatchObject({ tag: 'authenticating', authenticationGeneration: 1 });
		expect(first.effects).toEqual([
			{ type: 'Authenticate', authenticationGeneration: 1, credential: 'first' }
		]);

		const canceled = reduceSession(first.state, { type: 'LogoutRequested' });
		expect(canceled.state).toEqual({
			tag: 'signed_out',
			authenticationGeneration: 1,
			connectionGeneration: 0,
			operationGeneration: 0
		});
		expect(canceled.effects).toEqual([
			{ type: 'CancelAuthentication', authenticationGeneration: 1 }
		]);

		const second = reduceSession(canceled.state, {
			type: 'AuthenticateRequested',
			credential: 'second'
		});
		expect(second.state).toMatchObject({ tag: 'authenticating', authenticationGeneration: 2 });
		expect(second.effects).toEqual([
			{ type: 'Authenticate', authenticationGeneration: 2, credential: 'second' }
		]);

		const lateSuccess = reduceSession(second.state, {
			type: 'Authenticated',
			authenticationGeneration: 1,
			sessionCsrf: 'stale-csrf'
		});
		expect(lateSuccess.state).toBe(second.state);
		expect(lateSuccess.effects).toEqual([{ type: 'Logout', sessionCsrf: 'stale-csrf' }]);
		const lateRefusal = reduceSession(second.state, {
			type: 'AuthenticationRefused',
			authenticationGeneration: 1,
			refusal: { code: 'authentication_refused', message: 'late' }
		});
		expect(lateRefusal).toEqual({ state: second.state, effects: [] });

		const acceptedSecond = reduceSession(second.state, {
			type: 'Authenticated',
			authenticationGeneration: 2,
			sessionCsrf: 'second-csrf'
		});
		expect(acceptedSecond.state).toMatchObject({
			tag: 'connecting',
			authenticationGeneration: 2,
			sessionCsrf: 'second-csrf'
		});
	});

	it('cleans up unmatched auth success and fails closed on future auth results', () => {
		let state = dispatch(createSessionState(), {
			type: 'AuthenticateRequested',
			credential: 'first'
		});
		state = dispatch(state, { type: 'LogoutRequested' });
		const unmatched = reduceSession(state, {
			type: 'Authenticated',
			authenticationGeneration: 1,
			sessionCsrf: 'canceled-csrf'
		});
		expect(unmatched.state).toBe(state);
		expect(unmatched.effects).toEqual([{ type: 'Logout', sessionCsrf: 'canceled-csrf' }]);

		state = dispatch(state, { type: 'AuthenticateRequested', credential: 'second' });
		const future = reduceSession(state, {
			type: 'AuthenticationRefused',
			authenticationGeneration: 3,
			refusal: { code: 'authentication_refused', message: 'future' }
		});
		expect(future.state).toMatchObject({
			tag: 'protocol_error',
			protocolFailure: { kind: 'correlation' }
		});
	});

	it('ignores abandoned connection events before and after reconnect', () => {
		const lateProtocol = {
			type: 'ProtocolFailed' as const,
			connectionGeneration: 1,
			failure: { kind: 'invalid_document' as const, path: '$', message: 'late' }
		};

		let submit = submittingWithEmptyWatermark();
		submit = dispatch(submit, { type: 'SocketClosed', connectionGeneration: 1 });
		for (const event of [
			{
				type: 'SubmitAnswered' as const,
				connectionGeneration: 1,
				operationGeneration: 2,
				response: accepted()
			},
			{
				type: 'EffectFailed' as const,
				connectionGeneration: 1,
				operationGeneration: 2,
				message: 'late'
			},
			lateProtocol
		]) {
			expect(reduceSession(submit, event).state).toBe(submit);
		}

		let latest = watermarking();
		latest = dispatch(latest, { type: 'SocketClosed', connectionGeneration: 1 });
		expect(
			reduceSession(latest, {
				type: 'RetrieveAnswered',
				connectionGeneration: 1,
				operationGeneration: 1,
				response: refusedLatest('not_found')
			}).state
		).toBe(latest);

		let exact = submittingWithEmptyWatermark();
		exact = dispatch(exact, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		exact = dispatch(exact, { type: 'SocketClosed', connectionGeneration: 1 });
		exact = dispatch(exact, { type: 'ReconnectRequested' });
		exact = dispatch(exact, { type: 'SocketOpened', connectionGeneration: 2 });
		exact = dispatch(exact, { type: 'SocketClosed', connectionGeneration: 2 });
		const lateExact = {
			type: 'RetrieveAnswered' as const,
			connectionGeneration: 2,
			operationGeneration: 3,
			response: found('turn', delivery(), 'turn:one')
		};
		expect(reduceSession(exact, lateExact).state).toBe(exact);
		exact = dispatch(exact, { type: 'ReconnectRequested' });
		exact = dispatch(exact, { type: 'SocketOpened', connectionGeneration: 3 });
		expect(reduceSession(exact, lateExact).state).toBe(exact);
	});

	it('lets the first disconnect callback and reason win exactly once', () => {
		const active = submittingWithEmptyWatermark();
		const failed = dispatch(active, {
			type: 'SocketFailed',
			connectionGeneration: 1,
			message: 'first transport failure'
		});
		expect(failed).toMatchObject({
			tag: 'reconnecting',
			transportFailure: 'first transport failure'
		});
		expect(reduceSession(failed, { type: 'SocketClosed', connectionGeneration: 1 }).state).toBe(
			failed
		);
		expect(
			reduceSession(failed, {
				type: 'SocketFailed',
				connectionGeneration: 1,
				message: 'later failure'
			}).state
		).toBe(failed);

		const closed = dispatch(active, { type: 'SocketClosed', connectionGeneration: 1 });
		expect(reduceSession(closed, { type: 'SocketClosed', connectionGeneration: 1 }).state).toBe(
			closed
		);
		expect(
			reduceSession(closed, {
				type: 'SocketFailed',
				connectionGeneration: 1,
				message: 'too late'
			}).state
		).toBe(closed);
	});

	function acceptedSecondSession(): SessionState {
		let state = createSessionState();
		state = dispatch(state, { type: 'AuthenticateRequested', credential: 'first' });
		state = dispatch(state, { type: 'LogoutRequested' });
		state = dispatch(state, { type: 'AuthenticateRequested', credential: 'second' });
		state = dispatch(state, {
			type: 'Authenticated',
			authenticationGeneration: 2,
			sessionCsrf: 'second-csrf'
		});
		return dispatch(state, { type: 'SocketOpened', connectionGeneration: 1 });
	}

	it('fails signed out and cleans both sessions when stale auth succeeds after B is idle', () => {
		const idle = acceptedSecondSession();
		const result = reduceSession(idle, {
			type: 'Authenticated',
			authenticationGeneration: 1,
			sessionCsrf: 'stale-csrf'
		});
		expect(result.state).toEqual({
			tag: 'signed_out',
			authenticationGeneration: 2,
			connectionGeneration: 1,
			operationGeneration: 0
		});
		expect(result.effects).toEqual([
			{ type: 'CloseSocket', connectionGeneration: 1 },
			{ type: 'Logout', sessionCsrf: 'second-csrf' },
			{ type: 'Logout', sessionCsrf: 'stale-csrf' }
		]);
		expect(
			reduceSession(result.state, { type: 'SocketClosed', connectionGeneration: 1 }).state
		).toBe(result.state);
		expect(
			reduceSession(result.state, { type: 'SocketOpened', connectionGeneration: 1 }).state
		).toBe(result.state);
	});

	it('fails signed out and clears both sessions when stale auth succeeds while B waits', () => {
		let waiting = dispatch(acceptedSecondSession(), {
			type: 'IntentCreated',
			text: 'Open the iron door',
			idempotencyKey: 'intent-key'
		});
		waiting = dispatch(waiting, {
			type: 'RetrieveAnswered',
			connectionGeneration: 1,
			operationGeneration: 1,
			response: refusedLatest('not_found')
		});
		waiting = dispatch(waiting, {
			type: 'SubmitAnswered',
			connectionGeneration: 1,
			operationGeneration: 2,
			response: accepted()
		});
		expect(waiting.tag).toBe('waiting');

		const result = reduceSession(waiting, {
			type: 'Authenticated',
			authenticationGeneration: 1,
			sessionCsrf: 'stale-csrf'
		});
		expect(result.state).toEqual({
			tag: 'signed_out',
			authenticationGeneration: 2,
			connectionGeneration: 1,
			operationGeneration: 2
		});
		expect(result.effects).toEqual([
			{ type: 'CloseSocket', connectionGeneration: 1 },
			{ type: 'Logout', sessionCsrf: 'second-csrf' },
			{ type: 'Logout', sessionCsrf: 'stale-csrf' }
		]);
		expect(
			reduceSession(result.state, {
				type: 'DeliveryReceived',
				connectionGeneration: 1,
				delivery: delivery()
			}).state
		).toBe(result.state);
	});
});
