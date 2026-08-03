import { describe, expect, expectTypeOf, it } from 'vitest';

import {
	acceptedFrame,
	deliveryFrame,
	failedDeliveryFrame,
	mutableFixture,
	noRollDelivery,
	operationRefusedFrame,
	refusedFrame,
	retrievalFoundFrame,
	retrievalRefusedFrame,
	rolledDelivery,
	TURN_ID
} from './fixtures';
import {
	parsePlayerFrame,
	parseRetrieveRequest,
	parseSubmitAction,
	type CompleteTurnDelivery,
	type CompletedTurnResolution,
	type DeliveredNarration,
	type TurnDelivery
} from './parser';

function parseFixture(fixture: unknown) {
	return parsePlayerFrame(JSON.stringify(fixture));
}

function expectFailure(fixture: unknown, kind?: string) {
	const parsed = typeof fixture === 'string' ? parsePlayerFrame(fixture) : parseFixture(fixture);
	expect(parsed.ok).toBe(false);
	if (!parsed.ok && kind !== undefined) expect(parsed.error.kind).toBe(kind);
	return parsed;
}

type FixtureKey = string | number;
type FixtureMutation = (fixture: unknown) => void;

function fixtureContainer(value: unknown): Record<string, unknown> | unknown[] {
	if (typeof value !== 'object' || value === null) throw new Error('fixture path is not an object');
	return value as Record<string, unknown> | unknown[];
}

function fixtureValue(container: Record<string, unknown> | unknown[], key: FixtureKey): unknown {
	return Array.isArray(container) ? container[key as number] : container[key as string];
}

function fixtureParent(fixture: unknown, path: FixtureKey[]) {
	let current = fixtureContainer(fixture);
	for (const key of path.slice(0, -1)) current = fixtureContainer(fixtureValue(current, key));
	return { current, key: path.at(-1) as FixtureKey };
}

function setFixture(fixture: unknown, path: FixtureKey[], value: unknown): void {
	const { current, key } = fixtureParent(fixture, path);
	if (Array.isArray(current)) current[key as number] = value;
	else current[key as string] = value;
}

function deleteFixture(fixture: unknown, path: FixtureKey[]): void {
	const { current, key } = fixtureParent(fixture, path);
	if (Array.isArray(current)) delete current[key as number];
	else delete current[key as string];
}

function setField(path: FixtureKey[], value: unknown): FixtureMutation {
	return (fixture) => setFixture(fixture, path, value);
}

function deleteField(path: FixtureKey[]): FixtureMutation {
	return (fixture) => deleteFixture(fixture, path);
}

describe('parsePlayerFrame', () => {
	it('narrows parsed complete deliveries to completed resolution and required narration types', () => {
		expectTypeOf<
			CompleteTurnDelivery['result']['resolution']
		>().toMatchTypeOf<CompletedTurnResolution>();
		expectTypeOf<CompleteTurnDelivery['narration']>().toMatchTypeOf<DeliveredNarration>();
		const parsed = parseFixture(deliveryFrame);
		expect(parsed.ok).toBe(true);
		if (parsed.ok && parsed.value.type === 'turn_delivery') {
			const { delivery } = parsed.value;
			if (delivery.result.phase === 'complete') {
				expectTypeOf(delivery.result.resolution).toMatchTypeOf<CompletedTurnResolution>();
			}
		}
	});

	it('rejects structurally incomplete complete deliveries at compile time', () => {
		const verdict = {
			plausibility: 'certain',
			risk: 'none',
			consequence: 'none',
			requires_roll: false
		} as const;

		// @ts-expect-error A completed resolution always has an outcome band.
		const missingBand: CompletedTurnResolution = { verdict };
		// @ts-expect-error An automatic completion cannot carry a rolled outcome band.
		const invalidAutomaticBand: CompletedTurnResolution = { verdict, band: 'full' };
		// @ts-expect-error A complete delivery always carries its delivered narration.
		const missingNarration: TurnDelivery = {
			protocol: 'player/v1',
			result: {
				protocol: 'player/v1',
				turn_id: 'turn-act-1',
				action_id: 'act-1',
				player_id: 'c360.semmachina.world1.starter.player.p1',
				phase: 'complete',
				resolution: { verdict, band: 'auto' },
				narration_ref: 'obj://ARTIFACTS/narration/turn-act-1',
				resolved_at: '2026-07-28T09:15:30Z'
			}
		};
		// @ts-expect-error Complete result and delivered narration bands must match.
		const mismatchedNarration: TurnDelivery = {
			protocol: 'player/v1',
			result: {
				protocol: 'player/v1',
				turn_id: 'turn-act-1',
				action_id: 'act-1',
				player_id: 'c360.semmachina.world1.starter.player.p1',
				phase: 'complete',
				resolution: { verdict, band: 'auto' },
				narration_ref: 'obj://ARTIFACTS/narration/turn-act-1',
				resolved_at: '2026-07-28T09:15:30Z'
			},
			narration: { turn_id: 'turn-act-1', band: 'full', prose: 'Mismatch.' }
		};

		expect(missingBand).toEqual({ verdict });
		expect(invalidAutomaticBand.band).toBe('full');
		expect(missingNarration.result.phase).toBe('complete');
		expect(mismatchedNarration.narration?.band).toBe('full');
	});

	it.each([
		['accepted submission', acceptedFrame],
		['refused submission', refusedFrame],
		['rolled delivery', deliveryFrame],
		['terminal failure', failedDeliveryFrame],
		['found retrieval', retrievalFoundFrame],
		['refused retrieval', retrievalRefusedFrame],
		['operation refusal', operationRefusedFrame]
	])('accepts the complete %s document', (_name, fixture) => {
		const parsed = parseFixture(fixture);
		expect(parsed.ok).toBe(true);
		if (parsed.ok) expect(parsed.value.type).toBe(fixture.type);
	});

	it('accepts a coherent no-roll resolution', () => {
		const fixture = { ...deliveryFrame, delivery: noRollDelivery };
		expect(parseFixture(fixture).ok).toBe(true);
	});

	it('accepts additive server result fields without admitting them to the typed value', () => {
		const fixture = mutableFixture(deliveryFrame) as Record<string, unknown>;
		fixture.added_in_a_later_protocol = 'ignored';
		const parsed = parseFixture(fixture);
		expect(parsed.ok).toBe(true);
		if (parsed.ok) expect('added_in_a_later_protocol' in parsed.value).toBe(false);
	});

	it('accepts and strips additive fields at every nested delivery shape', () => {
		const fixture = mutableFixture(deliveryFrame);
		for (const path of [
			['future_frame'],
			['delivery', 'future_delivery'],
			['delivery', 'result', 'future_result'],
			['delivery', 'result', 'resolution', 'future_resolution'],
			['delivery', 'result', 'resolution', 'verdict', 'future_verdict'],
			['delivery', 'result', 'resolution', 'roll', 'future_roll'],
			['delivery', 'result', 'resolution', 'roll', 'modifiers', 0, 'future_modifier'],
			['delivery', 'result', 'companion_resolution', 'future_companion'],
			['delivery', 'narration', 'future_narration']
		] satisfies FixtureKey[][]) {
			setFixture(fixture, path, true);
		}

		const parsed = parseFixture(fixture);
		expect(parsed.ok).toBe(true);
		if (parsed.ok) expect(JSON.stringify(parsed.value)).not.toContain('future_');
	});

	it('accepts and strips additive fields in submit, retrieval, and operation responses', () => {
		for (const [fixture, payloadKey, hasRefusal] of [
			[mutableFixture(acceptedFrame), 'response', false],
			[mutableFixture(retrievalRefusedFrame), 'retrieval', true],
			[mutableFixture(operationRefusedFrame), 'operation', true]
		] as const) {
			setFixture(fixture, ['future_frame'], true);
			setFixture(fixture, [payloadKey, 'future_payload'], true);
			if (hasRefusal) setFixture(fixture, [payloadKey, 'refusal', 'future_refusal'], true);
			const parsed = parseFixture(fixture);
			expect(parsed.ok).toBe(true);
			if (parsed.ok) expect(JSON.stringify(parsed.value)).not.toContain('future_');
		}
	});

	it.each([
		'unauthenticated',
		'unsupported_protocol',
		'server_owned_field',
		'unknown_field',
		'malformed_request',
		'invalid_field',
		'unavailable'
	])('accepts the %s submission refusal code', (code) => {
		const fixture = mutableFixture(refusedFrame);
		setFixture(fixture, ['response', 'refusal', 'code'], code);
		expect(parseFixture(fixture).ok).toBe(true);
	});

	it('accepts turn_in_progress only with an active turn ID', () => {
		const fixture = mutableFixture(refusedFrame);
		setFixture(fixture, ['response', 'refusal', 'code'], 'turn_in_progress');
		setFixture(fixture, ['response', 'refusal', 'active_turn_id'], TURN_ID);
		expect(parseFixture(fixture).ok).toBe(true);
		deleteFixture(fixture, ['response', 'refusal', 'active_turn_id']);
		expectFailure(fixture);
	});

	it.each(['malformed_request', 'not_found', 'not_ready', 'unavailable'])(
		'accepts the %s retrieval refusal code',
		(code) => {
			const fixture = mutableFixture(retrievalRefusedFrame);
			setFixture(fixture, ['retrieval', 'refusal', 'code'], code);
			expect(parseFixture(fixture).ok).toBe(true);
		}
	);

	it('accepts the malformed-operation refusal without an operation name', () => {
		const fixture = mutableFixture(operationRefusedFrame);
		setFixture(fixture, ['operation', 'refusal', 'code'], 'malformed_operation');
		deleteFixture(fixture, ['operation', 'refusal', 'operation']);
		expect(parseFixture(fixture).ok).toBe(true);
	});

	it.each([
		['not JSON', '{'],
		['not an object', '[]'],
		['missing type', JSON.stringify({ protocol: 'player/v1' })],
		['non-string type', JSON.stringify({ protocol: 'player/v1', type: 42 })],
		['unknown type', JSON.stringify({ protocol: 'player/v1', type: 'turn_progress' })]
	])('fails closed for %s', (_name, raw) => {
		expectFailure(raw);
	});

	it('rejects a missing selected payload and a second known payload', () => {
		expectFailure({ protocol: 'player/v1', type: 'submit_response' });
		expectFailure({ ...acceptedFrame, delivery: rolledDelivery });
	});

	it.each([
		['accepted with refusal', setField(['response', 'refusal'], refusedFrame.response.refusal)],
		['accepted missing key', deleteField(['response', 'idempotency_key'])],
		['accepted missing identity', deleteField(['response', 'action_id'])],
		['accepted mismatched turn', setField(['response', 'turn_id'], 'turn-act-other')],
		['accepted missing arrival', deleteField(['response', 'arrived_at'])]
	])('rejects %s', (_name, mutate) => {
		const fixture = mutableFixture(acceptedFrame);
		mutate(fixture);
		expectFailure(fixture);
	});

	it.each([
		'2024-02-29T23:59:59Z',
		'2000-02-29T12:30:45.123456789Z',
		'0000-02-29T00:00:00Z',
		'0001-01-01T00:00:00.000000001Z',
		'0001-01-01T00:00:00+01:00',
		'0001-01-01T01:00:00.000000001+01:00',
		'9999-12-31T23:59:59-23:59'
	])('accepts Go-compatible nonzero RFC3339 timestamp %s', (timestamp) => {
		const fixture = mutableFixture(acceptedFrame);
		setFixture(fixture, ['response', 'arrived_at'], timestamp);
		expect(parseFixture(fixture).ok).toBe(true);
	});

	it.each([
		'0001-01-01T00:00:00Z',
		'0001-01-01T00:00:00.000000000Z',
		'0001-01-01T00:00:00+00:00',
		'0001-01-01T01:00:00+01:00',
		'0000-12-31T23:00:00-01:00',
		'0001-01-01T00:00:00.0000000001Z',
		'2023-02-29T00:00:00Z',
		'1900-02-29T00:00:00Z',
		'2024-00-01T00:00:00Z',
		'2024-13-01T00:00:00Z',
		'2024-04-31T00:00:00Z',
		'2024-01-00T00:00:00Z',
		'2024-01-01T24:00:00Z',
		'2024-01-01T00:60:00Z',
		'2024-01-01T00:00:60Z',
		'2024-01-01T00:00:00+24:00',
		'2024-01-01T00:00:00-00:60',
		'2024-01-01 00:00:00Z',
		'2024-01-01T00:00:00z',
		'2024-01-01T00:00:00',
		'2024-01-01T00:00:00.Z',
		'10000-01-01T00:00:00Z'
	])('rejects impossible, malformed, or Go-zero timestamp %s', (timestamp) => {
		const fixture = mutableFixture(acceptedFrame);
		setFixture(fixture, ['response', 'arrived_at'], timestamp);
		expectFailure(fixture);
	});

	it.each([
		['refused with identity', setField(['response', 'action_id'], 'act-1')],
		['refused with arrival', setField(['response', 'arrived_at'], '2026-01-01T00:00:00Z')],
		['refused without refusal', deleteField(['response', 'refusal'])],
		['refused with unknown code', setField(['response', 'refusal', 'code'], 'busy')]
	])('rejects %s', (_name, mutate) => {
		const fixture = mutableFixture(refusedFrame);
		mutate(fixture);
		expectFailure(fixture);
	});

	it.each([
		['latest with id', setField(['retrieval', 'id'], TURN_ID)],
		['named lookup without id', deleteField(['retrieval', 'id'])],
		['found without delivery', deleteField(['retrieval', 'delivery'])],
		['found with refusal', setField(['retrieval', 'refusal'], { code: 'not_found', message: 'x' })],
		['found for another turn', setField(['retrieval', 'id'], 'turn-act-other')]
	])('rejects %s', (_name, mutate) => {
		const fixture =
			_name === 'latest with id'
				? mutableFixture(retrievalRefusedFrame)
				: mutableFixture(retrievalFoundFrame);
		mutate(fixture);
		expectFailure(fixture);
	});

	it.each([
		['complete without resolution', deleteField(['delivery', 'result', 'resolution'])],
		['complete without narration ref', deleteField(['delivery', 'result', 'narration_ref'])],
		[
			'complete with failure reason',
			setField(['delivery', 'result', 'failure_reason'], 'effect-invalid')
		],
		['failed without reason', deleteField(['delivery', 'result', 'failure_reason'])],
		['failed with unknown reason', setField(['delivery', 'result', 'failure_reason'], 'timeout')],
		['nonterminal phase', setField(['delivery', 'result', 'phase'], 'narrating')],
		['mismatched action', setField(['delivery', 'result', 'action_id'], 'act-other')],
		['noncanonical player', setField(['delivery', 'result', 'player_id'], 'p1')],
		[
			'invalid narration ref',
			setField(['delivery', 'result', 'narration_ref'], 'https://example.test')
		]
	])('rejects a result that is %s', (_name, mutate) => {
		const fixture = _name.startsWith('failed')
			? mutableFixture(failedDeliveryFrame)
			: mutableFixture(deliveryFrame);
		mutate(fixture);
		expectFailure(fixture);
	});

	it.each([
		[
			'unknown plausibility',
			setField(['delivery', 'result', 'resolution', 'verdict', 'plausibility'], 'maybe')
		],
		['unknown risk', setField(['delivery', 'result', 'resolution', 'verdict', 'risk'], 'dire')],
		[
			'unknown consequence',
			setField(['delivery', 'result', 'resolution', 'verdict', 'consequence'], 'doom')
		],
		['unknown band', setField(['delivery', 'result', 'resolution', 'band'], 'great')],
		[
			'roll forbidden by verdict',
			setField(['delivery', 'result', 'resolution', 'verdict', 'requires_roll'], false)
		],
		['bad mechanic', setField(['delivery', 'result', 'resolution', 'roll', 'mechanic'], 'd20/v1')],
		['bad die', setField(['delivery', 'result', 'resolution', 'roll', 'dice'], [4, 7])],
		[
			'unknown modifier source',
			setField(['delivery', 'result', 'resolution', 'roll', 'modifiers', 0, 'source'], 'luck')
		],
		[
			'non-integer modifier total',
			setField(['delivery', 'result', 'resolution', 'roll', 'modifier_total'], 1.5)
		],
		['non-integer roll total', setField(['delivery', 'result', 'resolution', 'roll', 'total'], 9.5)]
	])('rejects a resolution with %s', (_name, mutate) => {
		const fixture = mutableFixture(deliveryFrame);
		mutate(fixture);
		expectFailure(fixture);
	});

	it('preserves delivered totals and band without client arithmetic or band lookup', () => {
		const fixture = mutableFixture(deliveryFrame);
		fixture.delivery.result.resolution.roll.modifier_total = -2;
		fixture.delivery.result.resolution.roll.total = 42;
		fixture.delivery.result.resolution.band = 'full';
		fixture.delivery.narration.band = 'full';
		const parsed = parseFixture(fixture);
		expect(parsed.ok).toBe(true);
		if (parsed.ok && parsed.value.type === 'turn_delivery') {
			const resolution = parsed.value.delivery.result.resolution;
			expect(resolution?.band).toBe('full');
			expect(resolution?.roll?.modifier_total).toBe(-2);
			expect(resolution?.roll?.total).toBe(42);
		}
	});

	it.each([Number.MIN_SAFE_INTEGER, Number.MAX_SAFE_INTEGER])(
		'accepts delivered safe-integer boundary %s without interpreting it',
		(total) => {
			const fixture = mutableFixture(deliveryFrame);
			fixture.delivery.result.resolution.roll.total = total;
			expect(parseFixture(fixture).ok).toBe(true);
		}
	);

	it.each([Number.MIN_SAFE_INTEGER - 1, Number.MAX_SAFE_INTEGER + 1])(
		'rejects unsafe delivered integer %s',
		(total) => {
			const fixture = mutableFixture(deliveryFrame);
			fixture.delivery.result.resolution.roll.total = total;
			expectFailure(fixture);
		}
	);

	it('rejects an unsafe JSON integer after the runtime has rounded it', () => {
		const raw = JSON.stringify(deliveryFrame).replace('"total":9', '"total":9007199254740993');
		expectFailure(raw);
	});

	it.each([
		['missing narration', deleteField(['delivery', 'narration'])],
		['foreign turn narration', setField(['delivery', 'narration', 'turn_id'], 'turn-act-other')],
		['wrong narration band', setField(['delivery', 'narration', 'band'], 'full')],
		['blank narration', setField(['delivery', 'narration', 'prose'], '')]
	])('rejects %s', (_name, mutate) => {
		const fixture = mutableFixture(deliveryFrame);
		mutate(fixture);
		expectFailure(fixture);
	});

	it.each([
		[
			'unknown companion kind',
			setField(['delivery', 'result', 'companion_resolution', 'kind'], 'aside')
		],
		[
			'hint without level',
			deleteField(['delivery', 'result', 'companion_resolution', 'hint_level'])
		],
		[
			'non-hint with level',
			setField(['delivery', 'result', 'companion_resolution', 'kind'], 'silent')
		]
	])('rejects %s', (_name, mutate) => {
		const fixture = mutableFixture(deliveryFrame);
		mutate(fixture);
		expectFailure(fixture);
	});

	it('returns a typed failure and no partial value', () => {
		const fixture = mutableFixture(deliveryFrame);
		fixture.delivery.result.resolution.roll.mechanic = 'd20/v1';
		const parsed = expectFailure(fixture, 'invalid_field');
		expect('value' in parsed).toBe(false);
		if (!parsed.ok) {
			expect(parsed.error.path).toContain('delivery.result.resolution.roll.mechanic');
			expect(parsed.error.message.length).toBeGreaterThan(0);
		}
	});
});

describe('strict client request parsing', () => {
	it('accepts the only client-owned submission fields', () => {
		expect(
			parseSubmitAction({
				protocol: 'player/v1',
				text: 'I open the gate.',
				idempotency_key: 'key-1'
			}).ok
		).toBe(true);
	});

	it.each([
		'action_id',
		'player_id',
		'campaign_id',
		'scene_id',
		'arrived_at',
		'channel',
		'adapter',
		'reply_to'
	])('refuses server-owned field %s even when null', (field) => {
		const request = {
			protocol: 'player/v1',
			text: 'I act.',
			idempotency_key: 'key-1',
			[field]: null
		};
		expect(parseSubmitAction(request).ok).toBe(false);
	});

	it('refuses unknown submission fields, blank text, and invalid keys', () => {
		expect(
			parseSubmitAction({ protocol: 'player/v1', text: 'x', idempotency_key: 'k', spell: 'fire' })
				.ok
		).toBe(false);
		expect(parseSubmitAction({ protocol: 'player/v1', text: '  ', idempotency_key: 'k' }).ok).toBe(
			false
		);
		expect(
			parseSubmitAction({ protocol: 'player/v1', text: 'x', idempotency_key: 'bad\nkey' }).ok
		).toBe(false);
	});

	it.each([
		{ protocol: 'player/v1', type: 'retrieve_result', by: 'latest' },
		{ protocol: 'player/v1', type: 'retrieve_result', by: 'turn', id: TURN_ID },
		{ protocol: 'player/v1', type: 'retrieve_result', by: 'action', id: 'act-1' }
	])('accepts a complete retrieval request', (request) => {
		expect(parseRetrieveRequest(request).ok).toBe(true);
	});

	it('refuses ambiguous, malformed, and unknown retrieval requests', () => {
		expect(
			parseRetrieveRequest({
				protocol: 'player/v1',
				type: 'retrieve_result',
				by: 'latest',
				id: TURN_ID
			}).ok
		).toBe(false);
		expect(
			parseRetrieveRequest({ protocol: 'player/v1', type: 'retrieve_result', by: 'turn' }).ok
		).toBe(false);
		expect(
			parseRetrieveRequest({ protocol: 'player/v1', type: 'cast_spell', by: 'latest' }).ok
		).toBe(false);
		expect(
			parseRetrieveRequest({
				protocol: 'player/v1',
				type: 'retrieve_result',
				by: 'latest',
				player_id: 'p1'
			}).ok
		).toBe(false);
	});
});
