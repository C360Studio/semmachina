import {
	isCanonicalEntityId,
	isCanonicalPredicate,
	isScopedEntityId,
	type ClockConfig,
	type DeploymentScope
} from './deployment-config';
import type { ClockProjection } from './world-projection';

export type ClockProjectionError =
	'clock_missing' | 'clock_ambiguous' | 'clock_malformed' | 'clock_out_of_scope';

export type ClockProjectionResult =
	| { readonly ok: true; readonly value: ClockProjection }
	| { readonly ok: false; readonly error: { readonly code: ClockProjectionError } };

type JsonRecord = Record<string, unknown>;

function record(value: unknown): JsonRecord | undefined {
	return typeof value === 'object' && value !== null && !Array.isArray(value)
		? (value as JsonRecord)
		: undefined;
}

function failure(error: ClockProjectionError): ClockProjectionResult {
	return Object.freeze({ ok: false, error: Object.freeze({ code: error }) });
}

function entityIdFailure(
	value: unknown,
	scope: DeploymentScope
): ClockProjectionResult | undefined {
	if (typeof value !== 'string' || !isCanonicalEntityId(value)) {
		return failure('clock_malformed');
	}
	if (!isScopedEntityId(value, scope.basePrefix)) return failure('clock_out_of_scope');
	return undefined;
}

function validateMetadata(triple: JsonRecord): boolean {
	if ('source' in triple && typeof triple.source !== 'string') return false;
	if ('timestamp' in triple && typeof triple.timestamp !== 'string') return false;
	if ('context' in triple && typeof triple.context !== 'string') return false;
	return !(
		'confidence' in triple &&
		(typeof triple.confidence !== 'number' ||
			!Number.isFinite(triple.confidence) ||
			triple.confidence < 0 ||
			triple.confidence > 1)
	);
}

export function projectClockFact(
	config: ClockConfig,
	scope: DeploymentScope,
	rawEntity: unknown
): ClockProjectionResult {
	if (config.state === 'not_configured') {
		return Object.freeze({
			ok: true,
			value: Object.freeze({ state: 'not_configured' })
		});
	}

	const configIdFailure = entityIdFailure(config.entityId, scope);
	if (configIdFailure !== undefined) return configIdFailure;
	if (rawEntity === null || rawEntity === undefined) return failure('clock_missing');

	const entity = record(rawEntity);
	if (entity === undefined || !Array.isArray(entity.triples)) {
		return failure('clock_malformed');
	}
	const returnedIdFailure = entityIdFailure(entity.id, scope);
	if (returnedIdFailure !== undefined) return returnedIdFailure;
	if (entity.id !== config.entityId) return failure('clock_out_of_scope');

	const matches: Array<{ readonly object: unknown; readonly datatype: unknown }> = [];
	for (const rawTriple of entity.triples) {
		const triple = record(rawTriple);
		if (triple === undefined) return failure('clock_malformed');
		const subjectFailure = entityIdFailure(triple.subject, scope);
		if (subjectFailure !== undefined) return subjectFailure;
		if (triple.subject !== entity.id) return failure('clock_malformed');
		if (typeof triple.predicate !== 'string' || !isCanonicalPredicate(triple.predicate)) {
			return failure('clock_malformed');
		}
		if (triple.datatype !== undefined && typeof triple.datatype !== 'string') {
			return failure('clock_malformed');
		}

		if (triple.datatype === '@id') {
			const objectFailure = entityIdFailure(triple.object, scope);
			if (objectFailure !== undefined) return objectFailure;
		} else if (triple.datatype === 'xsd:string') {
			if (typeof triple.object !== 'string') return failure('clock_malformed');
		} else if (triple.datatype === undefined || triple.datatype === '') {
			if (
				typeof triple.object !== 'boolean' &&
				(typeof triple.object !== 'number' || !Number.isFinite(triple.object))
			) {
				return failure('clock_malformed');
			}
		} else {
			return failure('clock_malformed');
		}
		if (!validateMetadata(triple)) return failure('clock_malformed');
		if (triple.predicate === config.predicate) {
			matches.push({ object: triple.object, datatype: triple.datatype });
		}
	}

	if (matches.length === 0) return failure('clock_missing');
	if (matches.length > 1) return failure('clock_ambiguous');
	const match = matches[0];
	if (
		(match.datatype !== undefined && match.datatype !== '') ||
		typeof match.object !== 'number' ||
		!Number.isFinite(match.object)
	) {
		return failure('clock_malformed');
	}

	return Object.freeze({
		ok: true,
		value: Object.freeze({
			state: 'configured',
			label: config.label,
			value: match.object,
			unit: config.unit
		})
	});
}
