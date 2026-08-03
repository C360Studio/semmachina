import { describe, expect, it } from 'vitest';

import {
	COMPANION_ID,
	failedDelivery,
	mutableFixture,
	noRollDelivery,
	rolledDelivery
} from '../player-v1/fixtures';
import { parsePlayerFrame, type TurnDelivery } from '../player-v1/parser';
import { createResolutionLedger, projectTerminal } from './resolution-projection';

function parsedDelivery(fixture: unknown): TurnDelivery {
	const parsed = parsePlayerFrame(
		JSON.stringify({ protocol: 'player/v1', type: 'turn_delivery', delivery: fixture })
	);
	if (!parsed.ok || parsed.value.type !== 'turn_delivery')
		throw new Error('invalid delivery fixture');
	return parsed.value.delivery;
}

function withIdentity(delivery: unknown, actionID: string): unknown {
	const changed = structuredClone(delivery) as {
		result: Record<string, unknown>;
		narration?: Record<string, unknown>;
	};
	changed.result.action_id = actionID;
	changed.result.turn_id = `turn-${actionID}`;
	if (changed.narration !== undefined) changed.narration.turn_id = `turn-${actionID}`;
	return changed;
}

describe('resolution terminal projection', () => {
	it('projects a rolled completion without losing canonical result or narration fields', () => {
		const original = createResolutionLedger();
		const projected = projectTerminal(original, parsedDelivery(rolledDelivery));

		expect(projected.status).toBe('added');
		expect(original.entries).toEqual([]);
		expect(projected.ledger).not.toBe(original);
		expect(projected.ledger.entries).toEqual([
			{
				turn_id: rolledDelivery.result.turn_id,
				action_id: rolledDelivery.result.action_id,
				player_id: rolledDelivery.result.player_id,
				phase: 'complete',
				resolved_at: rolledDelivery.result.resolved_at,
				narration_ref: rolledDelivery.result.narration_ref,
				narration: rolledDelivery.narration,
				verdict: rolledDelivery.result.resolution.verdict,
				band: rolledDelivery.result.resolution.band,
				roll: {
					kind: 'rolled',
					mechanic: '2d6-pbta/v1',
					dice: [4, 4],
					modifiers: [{ source: 'equipment', value: 1, note: 'crowbar' }],
					modifier_total: 1,
					total: 9
				},
				companion_resolution: rolledDelivery.result.companion_resolution
			}
		]);
	});

	it('represents a complete automatic outcome as an explicit not-required roll', () => {
		const projected = projectTerminal(createResolutionLedger(), parsedDelivery(noRollDelivery));

		expect(projected.status).toBe('added');
		expect(projected.ledger.entries[0]).toMatchObject({
			phase: 'complete',
			verdict: noRollDelivery.result.resolution.verdict,
			band: 'auto',
			roll: { kind: 'not_required' }
		});
	});

	it('projects a failed terminal and preserves its reason without inventing resolution data', () => {
		const projected = projectTerminal(createResolutionLedger(), parsedDelivery(failedDelivery));

		expect(projected.ledger.entries).toEqual([
			{
				turn_id: failedDelivery.result.turn_id,
				action_id: failedDelivery.result.action_id,
				player_id: failedDelivery.result.player_id,
				phase: 'failed',
				resolved_at: failedDelivery.result.resolved_at,
				failure_reason: 'effect-invalid'
			}
		]);
		expect(projected.ledger.entries[0]).not.toHaveProperty('verdict');
		expect(projected.ledger.entries[0]).not.toHaveProperty('roll');
		expect(projected.ledger.entries[0]).not.toHaveProperty('narration');
	});

	it('preserves every canonical field that is present on a failed terminal', () => {
		const fixture = mutableFixture(rolledDelivery) as unknown as Record<string, unknown>;
		const result = fixture.result as Record<string, unknown>;
		result.phase = 'failed';
		result.failure_reason = 'turn-stranded';

		const projected = projectTerminal(createResolutionLedger(), parsedDelivery(fixture));
		expect(projected.ledger.entries[0]).toEqual({
			turn_id: rolledDelivery.result.turn_id,
			action_id: rolledDelivery.result.action_id,
			player_id: rolledDelivery.result.player_id,
			phase: 'failed',
			resolved_at: rolledDelivery.result.resolved_at,
			failure_reason: 'turn-stranded',
			narration_ref: rolledDelivery.result.narration_ref,
			narration: rolledDelivery.narration,
			verdict: rolledDelivery.result.resolution.verdict,
			band: rolledDelivery.result.resolution.band,
			roll: {
				kind: 'rolled',
				...rolledDelivery.result.resolution.roll
			},
			companion_resolution: rolledDelivery.result.companion_resolution
		});
	});

	it('copies dice, modifier order, and declared totals without recalculation', () => {
		const fixture = mutableFixture(rolledDelivery);
		fixture.result.resolution.roll.dice = [1, 6];
		fixture.result.resolution.roll.modifiers = [
			{ source: 'position', value: -2, note: 'first' },
			{ source: 'trait', value: 3, note: 'second' }
		];
		fixture.result.resolution.roll.modifier_total = 99;
		fixture.result.resolution.roll.total = -77;

		const projected = projectTerminal(createResolutionLedger(), parsedDelivery(fixture));
		expect(projected.ledger.entries[0].roll).toEqual({
			kind: 'rolled',
			mechanic: '2d6-pbta/v1',
			dice: [1, 6],
			modifiers: [
				{ source: 'position', value: -2, note: 'first' },
				{ source: 'trait', value: 3, note: 'second' }
			],
			modifier_total: 99,
			total: -77
		});
	});

	it.each(['silent', 'quip', 'question', 'warning', 'recall'] as const)(
		'preserves the %s companion decision without a hint level',
		(kind) => {
			const fixture = mutableFixture(rolledDelivery) as unknown as {
				result: Record<string, unknown>;
			};
			fixture.result.companion_resolution = { companion_id: COMPANION_ID, kind };

			const projected = projectTerminal(createResolutionLedger(), parsedDelivery(fixture));
			expect(projected.ledger.entries[0].companion_resolution).toEqual({
				companion_id: COMPANION_ID,
				kind
			});
			expect(projected.ledger.entries[0].companion_resolution).not.toHaveProperty('hint_level');
		}
	);

	it.each(['nudge', 'connect', 'next-step'] as const)(
		'preserves the %s hint level exactly',
		(hint_level) => {
			const fixture = mutableFixture(rolledDelivery);
			fixture.result.companion_resolution = {
				companion_id: COMPANION_ID,
				kind: 'hint',
				hint_level
			};

			const projected = projectTerminal(createResolutionLedger(), parsedDelivery(fixture));
			expect(projected.ledger.entries[0].companion_resolution).toEqual({
				companion_id: COMPANION_ID,
				kind: 'hint',
				hint_level
			});
		}
	);

	it('keeps entries immutable and sorted by turn ID then action ID', () => {
		const initial = createResolutionLedger();
		const last = parsedDelivery(withIdentity(parsedDelivery(failedDelivery), 'z'));
		const first = parsedDelivery(withIdentity(parsedDelivery(noRollDelivery), 'a'));
		const afterLast = projectTerminal(initial, last).ledger;
		const afterFirst = projectTerminal(afterLast, first).ledger;

		expect(initial.entries).toEqual([]);
		expect(afterLast.entries.map((entry) => entry.action_id)).toEqual(['z']);
		expect(afterFirst.entries.map((entry) => entry.action_id)).toEqual(['a', 'z']);
		expect(Object.isFrozen(afterFirst)).toBe(true);
		expect(Object.isFrozen(afterFirst.entries)).toBe(true);
		expect(Object.isFrozen(afterFirst.entries[0])).toBe(true);
	});

	it('returns the original ledger identity for a canonical duplicate', () => {
		const delivery = parsedDelivery(rolledDelivery);
		const added = projectTerminal(createResolutionLedger(), delivery).ledger;
		const duplicate = projectTerminal(added, delivery);

		expect(duplicate).toEqual({ status: 'duplicate', ledger: added });
		expect(duplicate.ledger).toBe(added);
	});

	it('treats parser-stripped additive fields as a duplicate', () => {
		const added = projectTerminal(createResolutionLedger(), parsedDelivery(rolledDelivery)).ledger;
		const additive = mutableFixture(rolledDelivery) as unknown as Record<string, unknown>;
		additive.future_delivery = true;
		(additive.result as Record<string, unknown>).future_result = 'ignored';

		const duplicate = projectTerminal(added, parsedDelivery(additive));
		expect(duplicate.status).toBe('duplicate');
		expect(duplicate.ledger).toBe(added);
	});

	it.each([
		['player identity', ['result', 'player_id'], 'c360.semmachina.world1.starter.player.p2'],
		['resolved timestamp', ['result', 'resolved_at'], '2026-07-28T09:15:31Z'],
		['narration prose', ['narration', 'prose'], 'Different prose.'],
		['verdict', ['result', 'resolution', 'verdict', 'risk'], 'moderate'],
		['ordered modifiers', ['result', 'resolution', 'roll', 'modifiers', 0, 'note'], 'rope'],
		['companion decision', ['result', 'companion_resolution', 'hint_level'], 'nudge']
	] as const)(
		'conflicts on a known %s difference in either arrival order',
		(_name, path, value) => {
			const changed = mutableFixture(rolledDelivery) as unknown as Record<string, unknown>;
			let cursor: unknown = changed;
			for (const segment of path.slice(0, -1))
				cursor = (cursor as Record<PropertyKey, unknown>)[segment];
			(cursor as Record<PropertyKey, unknown>)[path.at(-1)!] = value;
			const left = parsedDelivery(rolledDelivery);
			const right = parsedDelivery(changed);

			for (const [first, second] of [
				[left, right],
				[right, left]
			] as const) {
				const existing = projectTerminal(createResolutionLedger(), first).ledger;
				const conflict = projectTerminal(existing, second);
				expect(conflict).toEqual({
					status: 'conflict',
					ledger: existing,
					error: { code: 'terminal_conflict' }
				});
				expect(conflict.ledger).toBe(existing);
			}
		}
	);

	it('uses the turn/action tuple as identity and does not collide concatenated IDs', () => {
		const first = parsedDelivery(withIdentity(parsedDelivery(failedDelivery), 'a-b'));
		if (first.result.phase !== 'failed') throw new Error('expected failed fixture');
		const second: TurnDelivery = {
			...first,
			result: { ...first.result, turn_id: 'turn-a', action_id: 'b' }
		};

		const ledger = projectTerminal(createResolutionLedger(), first).ledger;
		const added = projectTerminal(ledger, second);
		expect(added.status).toBe('added');
		expect(added.ledger.entries).toHaveLength(2);
	});

	it.each([
		['missing delivered prose', (fixture: Record<string, unknown>) => delete fixture.narration],
		[
			'malformed delivered prose',
			(fixture: Record<string, unknown>) =>
				((fixture.narration as Record<string, unknown>).prose = '')
		]
	])('admits no partial view when the parser rejects %s', (_name, mutate) => {
		const fixture = mutableFixture(rolledDelivery) as unknown as Record<string, unknown>;
		mutate(fixture);
		const ledger = createResolutionLedger();
		const parsed = parsePlayerFrame(
			JSON.stringify({ protocol: 'player/v1', type: 'turn_delivery', delivery: fixture })
		);

		expect(parsed.ok).toBe(false);
		expect(ledger.entries).toEqual([]);
	});
});
