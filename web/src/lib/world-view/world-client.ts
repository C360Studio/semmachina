import type { WorldProjection } from '../server/world-projection';

type JsonRecord = Record<string, unknown>;

function record(value: unknown): JsonRecord | undefined {
	return typeof value === 'object' && value !== null && !Array.isArray(value)
		? (value as JsonRecord)
		: undefined;
}

function hasExactKeys(
	value: JsonRecord,
	required: readonly string[],
	optional: readonly string[] = []
) {
	const keys = Object.keys(value);
	return (
		required.every((key) => keys.includes(key)) &&
		keys.every((key) => required.includes(key) || optional.includes(key))
	);
}

function parsePosition(value: unknown): Readonly<{ latitude: number; longitude: number }> {
	const candidate = record(value);
	if (
		candidate === undefined ||
		!hasExactKeys(candidate, ['latitude', 'longitude']) ||
		typeof candidate.latitude !== 'number' ||
		!Number.isFinite(candidate.latitude) ||
		candidate.latitude < -90 ||
		candidate.latitude > 90 ||
		typeof candidate.longitude !== 'number' ||
		!Number.isFinite(candidate.longitude) ||
		candidate.longitude < -180 ||
		candidate.longitude > 180
	) {
		throw new Error('World projection is invalid.');
	}
	return Object.freeze({ latitude: candidate.latitude, longitude: candidate.longitude });
}

function parseClock(value: unknown): WorldProjection['clock'] {
	const candidate = record(value);
	if (candidate === undefined || typeof candidate.state !== 'string') {
		throw new Error('World projection is invalid.');
	}
	if (candidate.state === 'not_configured') {
		if (!hasExactKeys(candidate, ['state'])) throw new Error('World projection is invalid.');
		return Object.freeze({ state: 'not_configured' });
	}
	if (
		candidate.state !== 'configured' ||
		!hasExactKeys(candidate, ['state', 'label', 'value', 'unit']) ||
		typeof candidate.label !== 'string' ||
		candidate.label.length === 0 ||
		typeof candidate.value !== 'number' ||
		!Number.isFinite(candidate.value) ||
		typeof candidate.unit !== 'string' ||
		candidate.unit.length === 0
	) {
		throw new Error('World projection is invalid.');
	}
	return Object.freeze({
		state: 'configured',
		label: candidate.label,
		value: candidate.value,
		unit: candidate.unit
	});
}

function parseWorld(value: unknown): WorldProjection {
	const candidate = record(value);
	if (
		candidate === undefined ||
		!hasExactKeys(candidate, ['places', 'clock']) ||
		!Array.isArray(candidate.places)
	) {
		throw new Error('World projection is invalid.');
	}

	const identities = new Set<string>();
	const places = candidate.places.map((value) => {
		const place = record(value);
		if (
			place === undefined ||
			!hasExactKeys(place, ['id', 'label', 'connections'], ['position']) ||
			typeof place.id !== 'string' ||
			place.id.length === 0 ||
			identities.has(place.id) ||
			typeof place.label !== 'string' ||
			place.label.length === 0 ||
			!Array.isArray(place.connections) ||
			!place.connections.every((connection) => typeof connection === 'string') ||
			new Set(place.connections).size !== place.connections.length
		) {
			throw new Error('World projection is invalid.');
		}
		identities.add(place.id);
		return Object.freeze({
			id: place.id,
			label: place.label,
			...(place.position === undefined ? {} : { position: parsePosition(place.position) }),
			connections: Object.freeze([...place.connections])
		});
	});

	if (places.some((place) => place.connections.some((connection) => !identities.has(connection)))) {
		throw new Error('World projection is invalid.');
	}
	return Object.freeze({ places: Object.freeze(places), clock: parseClock(candidate.clock) });
}

export async function loadWorld(fetcher: typeof fetch, origin: string): Promise<WorldProjection> {
	const endpoint = new URL('/api/world', new URL(origin).origin).toString();
	const response = await fetcher(endpoint, {
		method: 'GET',
		credentials: 'same-origin',
		cache: 'no-store',
		redirect: 'error'
	});
	if (response.status !== 200) throw new Error('World projection request failed.');
	let body: unknown;
	try {
		body = await response.json();
	} catch {
		throw new Error('World projection is invalid.');
	}
	return parseWorld(body);
}
