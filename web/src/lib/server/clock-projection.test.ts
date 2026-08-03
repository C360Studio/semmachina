import { describe, expect, it, vi } from 'vitest';

import type { ClockConfig, DeploymentScope } from './deployment-config';
import { projectClockFact, type ClockProjectionError } from './clock-projection';

const scope: DeploymentScope = Object.freeze({
	organization: 'c360',
	platform: 'semmachina',
	worldNamespace: 'world1',
	template: 'village',
	basePrefix: 'c360.semmachina.world1.village',
	locationPrefix: 'c360.semmachina.world1.village.location'
});
const entityId = `${scope.basePrefix}.campaign.clock`;
const predicate = 'campaign.clock.current';
const configured: ClockConfig = Object.freeze({
	state: 'configured',
	entityId,
	predicate,
	label: 'Village time',
	unit: 'minute',
	valueType: 'number'
});

function rawClock(
	value: unknown = 317,
	overrides: Readonly<Record<string, unknown>> = {}
): unknown {
	return {
		id: entityId,
		triples: [{ subject: entityId, predicate, object: value }],
		...overrides
	};
}

function failure(code: ClockProjectionError) {
	return { ok: false, error: { code } } as const;
}

describe('projectClockFact', () => {
	it('returns not_configured without inspecting the raw entity', () => {
		const unreadable = new Proxy(
			{},
			{
				get() {
					throw new Error('raw entity was inspected');
				}
			}
		);

		expect(projectClockFact({ state: 'not_configured' }, scope, unreadable)).toEqual({
			ok: true,
			value: { state: 'not_configured' }
		});
	});

	it('copies configured display metadata and the current finite numeric fact', () => {
		expect(projectClockFact(configured, scope, rawClock())).toEqual({
			ok: true,
			value: {
				state: 'configured',
				label: 'Village time',
				value: 317,
				unit: 'minute'
			}
		});
		expect(projectClockFact(configured, scope, rawClock(318))).toMatchObject({
			ok: true,
			value: { value: 318 }
		});
	});

	it.each([undefined, null])('classifies a %s returned entity as missing', (raw) => {
		expect(projectClockFact(configured, scope, raw)).toEqual(failure('clock_missing'));
	});

	it('classifies zero matching facts as missing', () => {
		expect(
			projectClockFact(configured, scope, {
				id: entityId,
				triples: [{ subject: entityId, predicate: 'campaign.clock.previous', object: 316 }]
			})
		).toEqual(failure('clock_missing'));
	});

	it('classifies multiple matching facts as ambiguous even when values are equal', () => {
		for (const values of [
			[317, 318],
			[317, 317]
		]) {
			expect(
				projectClockFact(configured, scope, {
					id: entityId,
					triples: values.map((object) => ({ subject: entityId, predicate, object }))
				})
			).toEqual(failure('clock_ambiguous'));
		}
	});

	it('counts matching facts before applying clock-specific value validation', () => {
		expect(
			projectClockFact(configured, scope, {
				id: entityId,
				triples: [
					{ subject: entityId, predicate, object: 317 },
					{ subject: entityId, predicate, object: 'late', datatype: 'xsd:string' }
				]
			})
		).toEqual(failure('clock_ambiguous'));
	});

	it('allows generic typed and untyped facts to coexist with one clock fact', () => {
		expect(
			projectClockFact(configured, scope, {
				id: entityId,
				triples: [
					{
						subject: entityId,
						predicate: 'campaign.clock.name',
						object: 'Village clock',
						datatype: 'xsd:string',
						source: 'author',
						timestamp: '2026-08-03T12:00:00Z',
						confidence: 0.75,
						context: 'campaign'
					},
					{
						subject: entityId,
						predicate: 'campaign.clock.owner',
						object: `${scope.basePrefix}.campaign.owner`,
						datatype: '@id'
					},
					{ subject: entityId, predicate: 'campaign.clock.running', object: true },
					{ subject: entityId, predicate: 'campaign.clock.previous', object: 316 },
					{ subject: entityId, predicate, object: 317 }
				]
			})
		).toEqual({
			ok: true,
			value: { state: 'configured', label: 'Village time', value: 317, unit: 'minute' }
		});
	});

	it.each([
		['primitive entity', 'bad'],
		['missing triples', { id: entityId }],
		['non-array triples', { id: entityId, triples: {} }],
		['primitive triple', { id: entityId, triples: [3] }],
		['non-string subject', rawClock(317, { triples: [{ subject: 4, predicate, object: 317 }] })],
		[
			'mismatched in-scope subject',
			rawClock(317, {
				triples: [{ subject: `${scope.basePrefix}.campaign.other`, predicate, object: 317 }]
			})
		],
		[
			'non-canonical predicate',
			rawClock(317, { triples: [{ subject: entityId, predicate: 'clock', object: 317 }] })
		],
		[
			'non-string predicate',
			rawClock(317, { triples: [{ subject: entityId, predicate: 4, object: 317 }] })
		],
		['string value', rawClock('317')],
		['boolean value', rawClock(true)],
		['NaN value', rawClock(Number.NaN)],
		['infinite value', rawClock(Number.POSITIVE_INFINITY)],
		[
			'typed number',
			rawClock(317, {
				triples: [{ subject: entityId, predicate, object: 317, datatype: 'xsd:double' }]
			})
		],
		[
			'non-string datatype',
			rawClock(317, { triples: [{ subject: entityId, predicate, object: 317, datatype: null }] })
		]
	])('classifies a malformed %s as malformed', (_name, raw) => {
		expect(projectClockFact(configured, scope, raw)).toEqual(failure('clock_malformed'));
	});

	it.each([
		['source', 1],
		['timestamp', false],
		['context', {}],
		['confidence', -0.01],
		['confidence', 1.01],
		['confidence', Number.NaN]
	])('rejects malformed unrelated metadata %s=%s', (field, value) => {
		expect(
			projectClockFact(configured, scope, {
				id: entityId,
				triples: [
					{ subject: entityId, predicate, object: 317 },
					{
						subject: entityId,
						predicate: 'campaign.clock.note',
						object: 'note',
						datatype: 'xsd:string',
						[field]: value
					}
				]
			})
		).toEqual(failure('clock_malformed'));
	});

	it.each([
		['unsupported datatype', { object: 1, datatype: 'xsd:double' }],
		['string datatype with number', { object: 1, datatype: 'xsd:string' }],
		['id datatype with boolean', { object: true, datatype: '@id' }],
		['untyped string', { object: 'note' }],
		['untyped nonfinite', { object: Number.NEGATIVE_INFINITY }]
	])('rejects malformed unrelated value: %s', (_name, fields) => {
		expect(
			projectClockFact(configured, scope, {
				id: entityId,
				triples: [
					{ subject: entityId, predicate, object: 317 },
					{
						subject: entityId,
						predicate: 'campaign.clock.note',
						...fields
					}
				]
			})
		).toEqual(failure('clock_malformed'));
	});

	it.each([
		['configured entity', { ...configured, entityId: 'not-an-entity' }, rawClock()],
		['returned entity', configured, rawClock(317, { id: 'not-an-entity' })],
		[
			'fact subject',
			configured,
			rawClock(317, { triples: [{ subject: 'not-an-entity', predicate, object: 317 }] })
		],
		[
			'@id object',
			configured,
			{
				id: entityId,
				triples: [
					{ subject: entityId, predicate, object: 317 },
					{
						subject: entityId,
						predicate: 'campaign.clock.owner',
						object: 'not-an-entity',
						datatype: '@id'
					}
				]
			}
		]
	] as const)(
		'classifies malformed canonical syntax in the %s as malformed',
		(_name, config, raw) => {
			expect(projectClockFact(config as ClockConfig, scope, raw)).toEqual(
				failure('clock_malformed')
			);
		}
	);

	it.each([undefined, ''])('accepts an %s datatype', (datatype) => {
		expect(
			projectClockFact(configured, scope, {
				id: entityId,
				triples: [{ subject: entityId, predicate, object: 317, datatype }]
			})
		).toMatchObject({ ok: true, value: { value: 317 } });
	});

	it.each([
		[
			'configured entity',
			{ ...configured, entityId: 'c360.semmachina.world10.village.campaign.clock' },
			rawClock()
		],
		[
			'returned entity',
			configured,
			rawClock(317, { id: 'c360.semmachina.world10.village.campaign.clock' })
		],
		[
			'mismatched returned entity',
			configured,
			rawClock(317, { id: `${scope.basePrefix}.campaign.other` })
		],
		[
			'fact subject',
			configured,
			rawClock(317, {
				triples: [
					{
						subject: 'c360.semmachina.world10.village.campaign.clock',
						predicate,
						object: 317
					}
				]
			})
		],
		[
			'@id object',
			configured,
			{
				id: entityId,
				triples: [
					{ subject: entityId, predicate, object: 317 },
					{
						subject: entityId,
						predicate: 'campaign.clock.owner',
						object: 'c360.semmachina.world10.village.campaign.owner',
						datatype: '@id'
					}
				]
			}
		]
	] as const)('classifies an out-of-scope %s without prefix confusion', (_name, config, raw) => {
		expect(projectClockFact(config as ClockConfig, scope, raw)).toEqual(
			failure('clock_out_of_scope')
		);
	});

	it('does not depend on wall time or mutate its inputs', () => {
		vi.useFakeTimers();
		try {
			const raw = rawClock(317) as {
				id: string;
				triples: Array<Record<string, unknown>>;
			};
			const before = structuredClone(raw);
			vi.setSystemTime(new Date('2000-01-01T00:00:00Z'));
			const first = projectClockFact(configured, scope, raw);
			vi.setSystemTime(new Date('2099-12-31T23:59:59Z'));
			const second = projectClockFact(configured, scope, raw);

			expect(second).toEqual(first);
			expect(raw).toEqual(before);
		} finally {
			vi.useRealTimers();
		}
	});
});
