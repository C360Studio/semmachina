import {
	isCanonicalPredicate,
	isLocationEntityId,
	isScopedEntityId,
	type DeploymentConfig,
	type DeploymentScope
} from './deployment-config';
import {
	isAuthorizedProjectionPrincipal,
	type AuthenticatedProjectionPrincipal
} from './projection-principal';

export const MAX_GRAPHQL_RESPONSE_BYTES = 1_048_576;
export const MAX_RELATIONSHIPS_PER_PLACE = 999;
export const DEFAULT_PROJECTION_DEADLINE_MS = 5000;
const MAX_TRIPLES_PER_ENTITY = 2048;
const LOCATION_KIND = 'world.entity.kind';
const LOCATION_NAME = 'world.entity.name';
const LATITUDE = 'geo.location.latitude';
const LONGITUDE = 'geo.location.longitude';
const CONNECTS_TO = 'location.relation.connects-to';

export const LOCATIONS_QUERY = `query SemMachinaLocations($prefix: String!, $limit: Int!) {
  entitiesByPrefix(prefix: $prefix, limit: $limit) {
    id
    triples { subject predicate object datatype }
  }
}`;

export const RELATIONSHIPS_QUERY = `query SemMachinaRelationships($entityId: String!, $direction: String!) {
  relationships(entityId: $entityId, direction: $direction) { from to predicate }
}`;

export const ENTITY_QUERY = `query SemMachinaEntity($id: String!) {
  entity(id: $id) { id triples { subject predicate object datatype } }
}`;

export type ProjectionErrorCode =
	| 'unauthorized'
	| 'scope_violation'
	| 'dangling_relationship'
	| 'projection_capacity_exceeded'
	| 'invalid_clock'
	| 'invalid_upstream'
	| 'upstream_unavailable';

export class ProjectionError extends Error {
	readonly code: ProjectionErrorCode;

	constructor(code: ProjectionErrorCode) {
		super(code);
		this.name = 'ProjectionError';
		this.code = code;
	}
}

export interface PlaceProjection {
	readonly id: string;
	readonly label: string;
	readonly position?: Readonly<{ latitude: number; longitude: number }>;
	readonly connections: readonly string[];
}

export type ClockProjection =
	| { readonly state: 'not_configured' }
	| {
			readonly state: 'configured';
			readonly label: string;
			readonly value: number;
			readonly unit: string;
	  };

export interface WorldProjection {
	readonly places: readonly PlaceProjection[];
	readonly clock: ClockProjection;
}

interface RawTriple {
	readonly subject: string;
	readonly predicate: string;
	readonly object: string | number | boolean;
	readonly datatype?: string;
}

interface ParsedLocation {
	readonly id: string;
	readonly label: string;
	readonly position?: Readonly<{ latitude: number; longitude: number }>;
	readonly authoredConnections: readonly string[];
}

interface Relationship {
	readonly from: string;
	readonly to: string;
	readonly predicate: string;
}

type JsonRecord = Record<string, unknown>;

function record(value: unknown): JsonRecord | undefined {
	return typeof value === 'object' && value !== null && !Array.isArray(value)
		? (value as JsonRecord)
		: undefined;
}

function validateDatatypeObject(
	object: unknown,
	datatype: unknown,
	scope: DeploymentScope
): object is string | number | boolean {
	if (datatype === '@id') {
		if (typeof object !== 'string' || !isScopedEntityId(object, scope.basePrefix)) {
			throw new ProjectionError('scope_violation');
		}
		return true;
	}
	if (datatype === 'xsd:string') return typeof object === 'string';
	if (datatype === undefined && typeof object === 'string') return true;
	if (datatype !== undefined && datatype !== '') return false;
	return typeof object === 'boolean' || (typeof object === 'number' && Number.isFinite(object));
}

function parseRawTriple(value: unknown, entityId: string, scope: DeploymentScope): RawTriple {
	const candidate = record(value);
	if (candidate === undefined) throw new ProjectionError('invalid_upstream');
	const { subject, predicate, object, datatype } = candidate;
	if (
		typeof subject !== 'string' ||
		subject !== entityId ||
		typeof predicate !== 'string' ||
		!isCanonicalPredicate(predicate) ||
		(datatype !== undefined && typeof datatype !== 'string') ||
		!validateDatatypeObject(object, datatype, scope)
	) {
		throw new ProjectionError('invalid_upstream');
	}
	if ('source' in candidate && typeof candidate.source !== 'string') {
		throw new ProjectionError('invalid_upstream');
	}
	if ('timestamp' in candidate && typeof candidate.timestamp !== 'string') {
		throw new ProjectionError('invalid_upstream');
	}
	if (
		'confidence' in candidate &&
		(typeof candidate.confidence !== 'number' ||
			!Number.isFinite(candidate.confidence) ||
			candidate.confidence < 0 ||
			candidate.confidence > 1)
	) {
		throw new ProjectionError('invalid_upstream');
	}
	if ('context' in candidate && typeof candidate.context !== 'string') {
		throw new ProjectionError('invalid_upstream');
	}
	return { subject, predicate, object, ...(datatype === undefined ? {} : { datatype }) };
}

function parseRawEntity(
	value: unknown,
	scope: DeploymentScope,
	expectedId?: string
): { id: string; triples: RawTriple[] } {
	const candidate = record(value);
	if (
		candidate === undefined ||
		typeof candidate.id !== 'string' ||
		!Array.isArray(candidate.triples)
	) {
		throw new ProjectionError('invalid_upstream');
	}
	if (expectedId !== undefined && candidate.id !== expectedId) {
		throw new ProjectionError('scope_violation');
	}
	if (candidate.triples.length > MAX_TRIPLES_PER_ENTITY) {
		throw new ProjectionError('projection_capacity_exceeded');
	}
	return {
		id: candidate.id,
		triples: candidate.triples.map((triple) =>
			parseRawTriple(triple, candidate.id as string, scope)
		)
	};
}

function oneTriple(triples: RawTriple[], predicate: string): RawTriple {
	const matches = triples.filter((triple) => triple.predicate === predicate);
	if (matches.length !== 1) throw new ProjectionError('invalid_upstream');
	return matches[0];
}

function parseLocation(value: unknown, scope: DeploymentScope): ParsedLocation {
	const entity = parseRawEntity(value, scope);
	if (!isLocationEntityId(entity.id, scope.locationPrefix)) {
		throw new ProjectionError('scope_violation');
	}
	const kind = oneTriple(entity.triples, LOCATION_KIND);
	const name = oneTriple(entity.triples, LOCATION_NAME);
	if (kind.object !== 'location' || kind.datatype !== 'xsd:string') {
		throw new ProjectionError('invalid_upstream');
	}
	if (
		typeof name.object !== 'string' ||
		name.object.length === 0 ||
		name.object.length > 512 ||
		name.datatype !== 'xsd:string'
	) {
		throw new ProjectionError('invalid_upstream');
	}

	const latitudeFacts = entity.triples.filter((triple) => triple.predicate === LATITUDE);
	const longitudeFacts = entity.triples.filter((triple) => triple.predicate === LONGITUDE);
	if (latitudeFacts.length !== longitudeFacts.length || latitudeFacts.length > 1) {
		throw new ProjectionError('invalid_upstream');
	}
	let position: ParsedLocation['position'];
	if (latitudeFacts.length === 1) {
		const latitude = latitudeFacts[0].object;
		const longitude = longitudeFacts[0].object;
		if (
			(latitudeFacts[0].datatype !== undefined && latitudeFacts[0].datatype !== '') ||
			(longitudeFacts[0].datatype !== undefined && longitudeFacts[0].datatype !== '') ||
			typeof latitude !== 'number' ||
			!Number.isFinite(latitude) ||
			latitude < -90 ||
			latitude > 90 ||
			typeof longitude !== 'number' ||
			!Number.isFinite(longitude) ||
			longitude < -180 ||
			longitude > 180
		) {
			throw new ProjectionError('invalid_upstream');
		}
		position = Object.freeze({ latitude, longitude });
	}

	const authoredConnections: string[] = [];
	for (const triple of entity.triples.filter((candidate) => candidate.predicate === CONNECTS_TO)) {
		if (
			typeof triple.object !== 'string' ||
			triple.datatype !== '@id' ||
			!isLocationEntityId(triple.object, scope.locationPrefix) ||
			authoredConnections.includes(triple.object)
		) {
			throw new ProjectionError('scope_violation');
		}
		authoredConnections.push(triple.object);
	}
	return Object.freeze({
		id: entity.id,
		label: name.object,
		...(position === undefined ? {} : { position }),
		authoredConnections: Object.freeze(authoredConnections.sort())
	});
}

function readRepresentation(
	candidate: JsonRecord,
	keys: readonly [string, string, string]
): Relationship | undefined {
	const values = keys.map((key) => candidate[key]);
	if (values.every((value) => value === undefined)) return undefined;
	if (!values.every((value) => typeof value === 'string')) {
		throw new ProjectionError('invalid_upstream');
	}
	return { from: values[0] as string, to: values[1] as string, predicate: values[2] as string };
}

function parseRelationship(value: unknown, scope: DeploymentScope): Relationship {
	const candidate = record(value);
	if (candidate === undefined) throw new ProjectionError('invalid_upstream');
	const corrected = readRepresentation(candidate, ['from', 'to', 'predicate']);
	const beta159 = readRepresentation(candidate, ['from_entity_id', 'to_entity_id', 'edge_type']);
	if (corrected === undefined && beta159 === undefined)
		throw new ProjectionError('invalid_upstream');
	if (
		corrected !== undefined &&
		beta159 !== undefined &&
		(corrected.from !== beta159.from ||
			corrected.to !== beta159.to ||
			corrected.predicate !== beta159.predicate)
	) {
		throw new ProjectionError('invalid_upstream');
	}
	const relationship = corrected ?? (beta159 as Relationship);
	if (
		!isScopedEntityId(relationship.from, scope.basePrefix) ||
		!isScopedEntityId(relationship.to, scope.basePrefix)
	) {
		throw new ProjectionError('scope_violation');
	}
	if (!isCanonicalPredicate(relationship.predicate)) {
		throw new ProjectionError('invalid_upstream');
	}
	return relationship;
}

function abortable<T>(operation: Promise<T>, signal: AbortSignal): Promise<T> {
	if (signal.aborted) return Promise.reject(new ProjectionError('upstream_unavailable'));
	return new Promise<T>((resolve, reject) => {
		const abort = () => reject(new ProjectionError('upstream_unavailable'));
		signal.addEventListener('abort', abort, { once: true });
		operation.then(
			(value) => {
				signal.removeEventListener('abort', abort);
				resolve(value);
			},
			(error: unknown) => {
				signal.removeEventListener('abort', abort);
				reject(error);
			}
		);
	});
}

async function readBoundedJson(response: Response, signal: AbortSignal): Promise<unknown> {
	const contentLength = response.headers.get('content-length');
	if (contentLength !== null && Number(contentLength) > MAX_GRAPHQL_RESPONSE_BYTES) {
		throw new ProjectionError('projection_capacity_exceeded');
	}
	if (response.body === null) throw new ProjectionError('invalid_upstream');
	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let bytes = 0;
	let text = '';
	try {
		while (true) {
			const { done, value } = await abortable(reader.read(), signal);
			if (done) break;
			bytes += value.byteLength;
			if (bytes > MAX_GRAPHQL_RESPONSE_BYTES) {
				await reader.cancel();
				throw new ProjectionError('projection_capacity_exceeded');
			}
			text += decoder.decode(value, { stream: true });
		}
		text += decoder.decode();
	} catch (error) {
		if (signal.aborted) void reader.cancel();
		if (error instanceof ProjectionError) throw error;
		throw new ProjectionError('upstream_unavailable');
	}
	try {
		return JSON.parse(text);
	} catch {
		throw new ProjectionError('invalid_upstream');
	}
}

function extractGraphQLField(document: unknown, field: string): unknown {
	const root = record(document);
	if (root === undefined) throw new ProjectionError('invalid_upstream');
	if ('errors' in root && (!Array.isArray(root.errors) || root.errors.length > 0)) {
		throw new ProjectionError('invalid_upstream');
	}
	const data = record(root.data);
	if (data === undefined || !(field in data)) throw new ProjectionError('invalid_upstream');
	return data[field];
}

function createGraphQLClient(config: DeploymentConfig, fetcher: typeof fetch) {
	return async (
		query: string,
		variables: JsonRecord,
		field: string,
		signal: AbortSignal
	): Promise<unknown> => {
		let response: Response;
		const headers: Record<string, string> = {
			accept: 'application/json',
			'content-type': 'application/json'
		};
		if (config.graphql.authentication.kind === 'bearer') {
			headers.authorization = `Bearer ${config.graphql.authentication.token}`;
		}
		try {
			response = await abortable(
				fetcher(config.graphql.endpoint, {
					method: 'POST',
					headers,
					body: JSON.stringify({ query, variables }),
					redirect: 'error',
					signal
				}),
				signal
			);
		} catch {
			throw new ProjectionError('upstream_unavailable');
		}
		if (!response.ok) throw new ProjectionError('upstream_unavailable');
		const contentType = response.headers.get('content-type')?.toLowerCase() ?? '';
		if (!contentType.startsWith('application/json')) throw new ProjectionError('invalid_upstream');
		return extractGraphQLField(await readBoundedJson(response, signal), field);
	};
}

export interface ProjectionAdapterOptions {
	readonly deadlineMs?: number;
}

export function createWorldProjectionAdapter(
	config: DeploymentConfig,
	fetcher: typeof fetch,
	options: ProjectionAdapterOptions = {}
) {
	const deadlineMs = options.deadlineMs ?? config.graphql.projectionDeadlineMs;
	if (!Number.isInteger(deadlineMs) || deadlineMs < 1 || deadlineMs > 30_000) {
		throw new Error('invalid projection deadline');
	}
	const query = createGraphQLClient(config, fetcher);
	const scope = config.scope;

	async function queryRelationships(
		location: ParsedLocation,
		knownIds: ReadonlySet<string>,
		signal: AbortSignal
	) {
		const raw = await query(
			RELATIONSHIPS_QUERY,
			{ entityId: location.id, direction: 'outgoing' },
			'relationships',
			signal
		);
		if (!Array.isArray(raw)) throw new ProjectionError('invalid_upstream');
		if (raw.length > MAX_RELATIONSHIPS_PER_PLACE) {
			throw new ProjectionError('projection_capacity_exceeded');
		}
		const relationships = raw.map((value) => parseRelationship(value, scope));
		const connections = new Set<string>();
		for (const relationship of relationships) {
			if (relationship.from !== location.id) throw new ProjectionError('invalid_upstream');
			if (relationship.predicate !== CONNECTS_TO) continue;
			if (
				!isLocationEntityId(relationship.from, scope.locationPrefix) ||
				!isLocationEntityId(relationship.to, scope.locationPrefix)
			) {
				throw new ProjectionError('scope_violation');
			}
			if (!knownIds.has(relationship.to)) throw new ProjectionError('dangling_relationship');
			if (connections.has(relationship.to)) {
				throw new ProjectionError('invalid_upstream');
			}
			connections.add(relationship.to);
		}
		const projected = [...connections].sort();
		if (
			projected.length !== location.authoredConnections.length ||
			projected.some((value, index) => value !== location.authoredConnections[index])
		) {
			throw new ProjectionError('invalid_upstream');
		}
		return Object.freeze(projected);
	}

	async function projectClock(signal: AbortSignal): Promise<ClockProjection> {
		const clock = config.clock;
		if (clock.state === 'not_configured') return Object.freeze({ state: 'not_configured' });
		const raw = await query(ENTITY_QUERY, { id: clock.entityId }, 'entity', signal);
		if (!isScopedEntityId(clock.entityId, scope.basePrefix) || raw === null) {
			throw new ProjectionError('invalid_clock');
		}
		let entity: ReturnType<typeof parseRawEntity>;
		try {
			entity = parseRawEntity(raw, scope, clock.entityId);
		} catch {
			throw new ProjectionError('invalid_clock');
		}
		const facts = entity.triples.filter((triple) => triple.predicate === clock.predicate);
		if (
			facts.length !== 1 ||
			(facts[0].datatype !== undefined && facts[0].datatype !== '') ||
			typeof facts[0].object !== 'number' ||
			!Number.isFinite(facts[0].object)
		) {
			throw new ProjectionError('invalid_clock');
		}
		return Object.freeze({
			state: 'configured',
			label: clock.label,
			value: facts[0].object,
			unit: clock.unit
		});
	}

	return Object.freeze({
		scope,
		async projectWorld(principal: AuthenticatedProjectionPrincipal): Promise<WorldProjection> {
			if (!isAuthorizedProjectionPrincipal(principal, config)) {
				throw new ProjectionError('unauthorized');
			}
			const controller = new AbortController();
			const deadline = setTimeout(() => controller.abort(), deadlineMs);
			try {
				const rawLocations = await query(
					LOCATIONS_QUERY,
					{ prefix: scope.locationPrefix, limit: config.graphql.placeLimit },
					'entitiesByPrefix',
					controller.signal
				);
				if (!Array.isArray(rawLocations)) throw new ProjectionError('invalid_upstream');
				if (rawLocations.length >= config.graphql.placeLimit) {
					throw new ProjectionError('projection_capacity_exceeded');
				}
				const locations = rawLocations
					.map((value) => parseLocation(value, scope))
					.sort((a, b) => a.id.localeCompare(b.id));
				const knownIds = new Set(locations.map((location) => location.id));
				if (knownIds.size !== locations.length) throw new ProjectionError('invalid_upstream');
				for (const location of locations) {
					if (location.authoredConnections.some((target) => !knownIds.has(target))) {
						throw new ProjectionError('dangling_relationship');
					}
				}
				const places: PlaceProjection[] = [];
				for (const location of locations) {
					const connections = await queryRelationships(location, knownIds, controller.signal);
					places.push(
						Object.freeze({
							id: location.id,
							label: location.label,
							...(location.position === undefined ? {} : { position: location.position }),
							connections
						})
					);
				}
				return Object.freeze({
					places: Object.freeze(places),
					clock: await projectClock(controller.signal)
				});
			} finally {
				clearTimeout(deadline);
			}
		}
	});
}
