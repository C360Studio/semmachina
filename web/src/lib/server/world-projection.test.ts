import { describe, expect, it, vi } from 'vitest';

import { loadDeploymentConfig } from './deployment-config';
import { issueProjectionPrincipal } from './projection-principal';
import {
	ENTITY_QUERY,
	LOCATIONS_QUERY,
	RELATIONSHIPS_QUERY,
	MAX_GRAPHQL_RESPONSE_BYTES,
	MAX_RELATIONSHIPS_PER_PLACE,
	createWorldProjectionAdapter
} from './world-projection';
import { assembleWorldProjectionRoute, createWorldProjectionRoute } from './world-projection-route';

const LOCATION_A = 'c360.semmachina.bellweather.bellweather-maze.location.green';
const LOCATION_B = 'c360.semmachina.bellweather.bellweather-maze.location.maze';
const FOREIGN_LOCATION = 'other.semmachina.bellweather.bellweather-maze.location.road';
const CLOCK_ID = 'c360.semmachina.bellweather.bellweather-maze.campaign.main';

interface RawTriple {
	subject: string;
	predicate: string;
	object: unknown;
	datatype?: string;
}

const environment = {
	SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:8080/graphql',
	SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
	SEMMACHINA_WORLD_ORG: 'c360',
	SEMMACHINA_WORLD_NAMESPACE: 'bellweather',
	SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze'
};

function entity(id: string, name = 'A place') {
	return {
		id,
		triples: [
			{ subject: id, predicate: 'world.entity.kind', object: 'location', datatype: 'xsd:string' },
			{ subject: id, predicate: 'world.entity.name', object: name, datatype: 'xsd:string' }
		] as RawTriple[],
		storage_ref: { bucket: 'must-not-leak' },
		message_type: { domain: 'private' },
		version: 42,
		updated_at: '2026-08-03T00:00:00Z'
	};
}

function jsonResponse(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json' }
	});
}

function queuedFetch(responses: Response[]) {
	return vi.fn<typeof fetch>(async () => {
		const response = responses.shift();
		if (response === undefined) throw new Error('unexpected upstream request');
		return response;
	});
}

describe('world projection route', () => {
	it.each([
		'?prefix=other.world',
		'?entityId=' + encodeURIComponent(LOCATION_A),
		'?credentials=secret',
		'?query=%7Bentities%7D',
		'?graphql=%7Bentity%7D',
		'?clockEntityId=' + encodeURIComponent(CLOCK_ID)
	])('refuses browser authority %s before calling upstream', async (suffix) => {
		const projectWorld = vi.fn(async () => ({
			places: [],
			clock: { state: 'not_configured' as const }
		}));
		const route = createWorldProjectionRoute({
			projectWorld,
			authorize: async () => ({}) as never
		});
		const response = await route(new Request(`https://surface.test/api/world${suffix}`));
		expect(response.status).toBe(400);
		expect(projectWorld).not.toHaveBeenCalled();
	});

	it('refuses a browser Authorization header before calling upstream', async () => {
		const projectWorld = vi.fn(async () => ({
			places: [],
			clock: { state: 'not_configured' as const }
		}));
		const route = createWorldProjectionRoute({
			projectWorld,
			authorize: async () => ({}) as never
		});
		const response = await route(
			new Request('https://surface.test/api/world', {
				headers: { authorization: 'Bearer browser-secret' }
			})
		);
		expect(response.status).toBe(400);
		expect(projectWorld).not.toHaveBeenCalled();
	});

	it('returns only the closed projection from a parameterless GET', async () => {
		const config = loadDeploymentConfig(environment);
		const principal = issueProjectionPrincipal(config);
		const projectWorld = vi.fn(async () => ({
			places: [{ id: LOCATION_A, label: 'Green', connections: [] }],
			clock: { state: 'not_configured' as const }
		}));
		const response = await createWorldProjectionRoute({
			projectWorld,
			authorize: async () => principal
		})(new Request('https://surface.test/api/world'));
		expect(response.status).toBe(200);
		expect(await response.json()).toEqual({
			places: [{ id: LOCATION_A, label: 'Green', connections: [] }],
			clock: { state: 'not_configured' }
		});
	});

	it('denies by default without an authenticated principal and never invokes the adapter', async () => {
		const projectWorld = vi.fn();
		const response = await createWorldProjectionRoute({ projectWorld })(
			new Request('https://surface.test/api/world')
		);
		expect(response.status).toBe(401);
		expect(projectWorld).not.toHaveBeenCalled();
	});

	it('redacts unexpected upstream and credential detail from route failures', async () => {
		const config = loadDeploymentConfig(environment);
		const projectWorld = vi.fn(async () => {
			throw new Error('Bearer upstream-secret at graph.internal');
		});
		const response = await createWorldProjectionRoute({
			projectWorld,
			authorize: async () => issueProjectionPrincipal(config)
		})(new Request('https://surface.test/api/world'));
		const body = await response.text();
		expect(response.status).toBe(500);
		expect(body).toContain('projection_failed');
		expect(body).not.toContain('upstream-secret');
		expect(body).not.toContain('graph.internal');
	});

	it('validates deployment configuration eagerly while retaining default-deny assembly', async () => {
		const fetcher = vi.fn<typeof fetch>();
		expect(() =>
			assembleWorldProjectionRoute({
				environment: { ...environment, SEMMACHINA_GRAPHQL_URL: '' },
				fetcher
			})
		).toThrow();
		const route = assembleWorldProjectionRoute({ environment, fetcher });
		expect((await route(new Request('https://surface.test/api/world'))).status).toBe(401);
		expect(fetcher).not.toHaveBeenCalled();
	});

	it('assembles one immutable config and adapter for every request', async () => {
		const identities: object[] = [];
		const fetcher = queuedFetch([
			jsonResponse({ data: { entitiesByPrefix: [] } }),
			jsonResponse({ data: { entitiesByPrefix: [] } })
		]);
		const route = assembleWorldProjectionRoute({
			environment,
			fetcher,
			authorize: async (_request, config) => {
				identities.push(config.deploymentInstance);
				return issueProjectionPrincipal(config);
			}
		});
		expect((await route(new Request('https://surface.test/api/world'))).status).toBe(200);
		expect((await route(new Request('https://surface.test/api/world'))).status).toBe(200);
		expect(identities).toHaveLength(2);
		expect(identities[0]).toBe(identities[1]);
		expect(fetcher).toHaveBeenCalledTimes(2);
	});

	it('redacts the server proxy token when an authenticated upstream request fails', async () => {
		const token = 'server-only-proxy-token';
		const route = assembleWorldProjectionRoute({
			environment: {
				...environment,
				SEMMACHINA_GRAPHQL_URL: 'https://graph.example.test/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: 'auth_proxy',
				SEMMACHINA_GRAPHQL_AUTH_TOKEN: token
			},
			fetcher: vi.fn(async () => {
				throw new Error(`proxy refused ${token}`);
			}),
			authorize: async (_request, config) => issueProjectionPrincipal(config)
		});
		const response = await route(new Request('https://surface.test/api/world'));
		const body = await response.text();
		expect(response.status).toBe(502);
		expect(body).toContain('upstream_unavailable');
		expect(body).not.toContain(token);
	});
});

describe('fixed beta.159 GraphQL adapter', () => {
	it('uses only fixed documents, server-derived variables, and no browser credentials', async () => {
		const config = loadDeploymentConfig(environment);
		const principal = issueProjectionPrincipal(config);
		const fetcher = queuedFetch([
			jsonResponse({ data: { entitiesByPrefix: [entity(LOCATION_A, 'Green')] } }),
			jsonResponse({ data: { relationships: [] } })
		]);
		const adapter = createWorldProjectionAdapter(config, fetcher);
		await adapter.projectWorld(principal);
		expect(fetcher).toHaveBeenCalledTimes(2);
		const requests = fetcher.mock.calls.map((call) => JSON.parse(String(call[1]?.body)));
		expect(requests).toEqual([
			{
				query: LOCATIONS_QUERY,
				variables: {
					prefix: 'c360.semmachina.bellweather.bellweather-maze.location',
					limit: 1000
				}
			},
			{
				query: RELATIONSHIPS_QUERY,
				variables: { entityId: LOCATION_A, direction: 'outgoing' }
			}
		]);
		for (const call of fetcher.mock.calls) {
			const headers = new Headers(call[1]?.headers);
			expect(headers.get('authorization')).toBeNull();
			expect(call[1]?.redirect).toBe('error');
		}
	});

	it('requires a genuine in-scope authenticated principal before any upstream dial', async () => {
		const config = loadDeploymentConfig(environment);
		const fetcher = vi.fn<typeof fetch>();
		const adapter = createWorldProjectionAdapter(config, fetcher);
		await expect(
			adapter.projectWorld({ scopeKey: config.scope.basePrefix } as never)
		).rejects.toMatchObject({
			code: 'unauthorized'
		});
		expect(fetcher).not.toHaveBeenCalled();
	});

	it('rejects a stale principal for a different deployment instance with the same scope', async () => {
		const first = loadDeploymentConfig(environment);
		const replacement = loadDeploymentConfig({
			...environment,
			SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:8081/graphql'
		});
		const fetcher = vi.fn<typeof fetch>();
		const adapter = createWorldProjectionAdapter(replacement, fetcher);
		await expect(adapter.projectWorld(issueProjectionPrincipal(first))).rejects.toMatchObject({
			code: 'unauthorized'
		});
		expect(fetcher).not.toHaveBeenCalled();
	});

	it('uses only the dedicated server token when auth_proxy posture is configured', async () => {
		const config = loadDeploymentConfig({
			...environment,
			SEMMACHINA_GRAPHQL_URL: 'https://graph.example.test/graphql',
			SEMMACHINA_GRAPHQL_POSTURE: 'auth_proxy',
			SEMMACHINA_GRAPHQL_AUTH_TOKEN: 'server-only-proxy-token'
		});
		const fetcher = queuedFetch([jsonResponse({ data: { entitiesByPrefix: [] } })]);
		await createWorldProjectionAdapter(config, fetcher).projectWorld(
			issueProjectionPrincipal(config)
		);
		const headers = new Headers(fetcher.mock.calls[0][1]?.headers);
		expect(headers.get('authorization')).toBe('Bearer server-only-proxy-token');
	});

	it('strips raw entity state and projects exact labels, authored coordinates, and directed edges', async () => {
		const config = loadDeploymentConfig(environment);
		const principal = issueProjectionPrincipal(config);
		const located = entity(LOCATION_A, 'Fete Green');
		located.triples.push(
			{ subject: LOCATION_A, predicate: 'geo.location.latitude', object: 51.5, datatype: '' },
			{ subject: LOCATION_A, predicate: 'geo.location.longitude', object: -0.1, datatype: '' },
			{
				subject: LOCATION_A,
				predicate: 'location.relation.connects-to',
				object: LOCATION_B,
				datatype: '@id'
			},
			{
				subject: LOCATION_A,
				predicate: 'world.entity.description',
				object: 'must not leak',
				datatype: 'xsd:string'
			}
		);
		const fetcher = queuedFetch([
			jsonResponse({ data: { entitiesByPrefix: [located, entity(LOCATION_B, 'Prize Maze')] } }),
			jsonResponse({
				data: {
					relationships: [
						{
							from_entity_id: LOCATION_A,
							to_entity_id: LOCATION_B,
							edge_type: 'location.relation.connects-to'
						}
					]
				}
			}),
			jsonResponse({ data: { relationships: [] } })
		]);
		const result = await createWorldProjectionAdapter(config, fetcher).projectWorld(principal);
		expect(result).toEqual({
			places: [
				{
					id: LOCATION_A,
					label: 'Fete Green',
					position: { latitude: 51.5, longitude: -0.1 },
					connections: [LOCATION_B]
				},
				{ id: LOCATION_B, label: 'Prize Maze', connections: [] }
			],
			clock: { state: 'not_configured' }
		});
		expect(JSON.stringify(result)).not.toContain('must not leak');
		expect(JSON.stringify(result)).not.toContain('storage_ref');
	});

	it.each([
		['out-of-prefix entity', { data: { entitiesByPrefix: [entity(FOREIGN_LOCATION)] } }],
		[
			'component-prefix collision',
			{
				data: {
					entitiesByPrefix: [
						entity('c360.semmachina.bellweather10.bellweather-maze.location.green')
					]
				}
			}
		],
		['GraphQL error', { errors: [{ message: 'backend detail' }] }],
		['missing field', { data: {} }],
		['wrong root shape', { data: { entitiesByPrefix: {} } }]
	])('fails the whole projection for %s', async (_name, response) => {
		const config = loadDeploymentConfig(environment);
		const adapter = createWorldProjectionAdapter(config, queuedFetch([jsonResponse(response)]));
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			name: 'ProjectionError'
		});
	});

	it('fails conservatively when prefix enumeration reaches its silent-truncation limit', async () => {
		const config = loadDeploymentConfig(environment);
		const adapter = createWorldProjectionAdapter(
			config,
			queuedFetch([
				jsonResponse({
					data: {
						entitiesByPrefix: Array.from({ length: 1000 }, (_, index) =>
							entity(`c360.semmachina.bellweather.bellweather-maze.location.place-${index}`)
						)
					}
				})
			])
		);
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code: 'projection_capacity_exceeded'
		});
	});

	it('rejects an out-of-prefix relationship endpoint instead of filtering it', async () => {
		const config = loadDeploymentConfig(environment);
		const adapter = createWorldProjectionAdapter(
			config,
			queuedFetch([
				jsonResponse({ data: { entitiesByPrefix: [entity(LOCATION_A)] } }),
				jsonResponse({
					data: {
						relationships: [
							{
								from_entity_id: LOCATION_A,
								to_entity_id: FOREIGN_LOCATION,
								edge_type: 'location.relation.connects-to'
							}
						]
					}
				})
			])
		);
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code: 'scope_violation'
		});
	});

	it('validates then omits a well-formed in-scope non-map relationship', async () => {
		const config = loadDeploymentConfig(environment);
		const adapter = createWorldProjectionAdapter(
			config,
			queuedFetch([
				jsonResponse({ data: { entitiesByPrefix: [entity(LOCATION_A)] } }),
				jsonResponse({
					data: {
						relationships: [
							{
								from_entity_id: LOCATION_A,
								to_entity_id: CLOCK_ID,
								edge_type: 'world.entity.belongs-to'
							}
						]
					}
				})
			])
		);
		expect(await adapter.projectWorld(issueProjectionPrincipal(config))).toEqual({
			places: [{ id: LOCATION_A, label: 'A place', connections: [] }],
			clock: { state: 'not_configured' }
		});
	});

	it('rejects a dangling authored connection before querying relationships', async () => {
		const config = loadDeploymentConfig(environment);
		const place = entity(LOCATION_A);
		place.triples.push({
			subject: LOCATION_A,
			predicate: 'location.relation.connects-to',
			object: LOCATION_B,
			datatype: '@id'
		});
		const fetcher = queuedFetch([jsonResponse({ data: { entitiesByPrefix: [place] } })]);
		const adapter = createWorldProjectionAdapter(config, fetcher);
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code: 'dangling_relationship'
		});
		expect(fetcher).toHaveBeenCalledTimes(1);
	});

	it('accepts corrected relationship fields but rejects conflicting dual representations', async () => {
		const config = loadDeploymentConfig(environment);
		const place = entity(LOCATION_A);
		place.triples.push({
			subject: LOCATION_A,
			predicate: 'location.relation.connects-to',
			object: LOCATION_B,
			datatype: '@id'
		});
		const places = { data: { entitiesByPrefix: [place, entity(LOCATION_B)] } };
		const corrected = {
			data: {
				relationships: [
					{ from: LOCATION_A, to: LOCATION_B, predicate: 'location.relation.connects-to' }
				]
			}
		};
		const valid = createWorldProjectionAdapter(
			config,
			queuedFetch([
				jsonResponse(places),
				jsonResponse(corrected),
				jsonResponse({ data: { relationships: [] } })
			])
		);
		const validProjection = await valid.projectWorld(issueProjectionPrincipal(config));
		expect(validProjection.places[0].connections).toEqual([LOCATION_B]);

		const conflicting = {
			data: {
				relationships: [
					{
						from: LOCATION_A,
						to: LOCATION_B,
						predicate: 'location.relation.connects-to',
						from_entity_id: LOCATION_B,
						to_entity_id: LOCATION_A,
						edge_type: 'location.relation.connects-to'
					}
				]
			}
		};
		const invalid = createWorldProjectionAdapter(
			config,
			queuedFetch([jsonResponse(places), jsonResponse(conflicting)])
		);
		await expect(invalid.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code: 'invalid_upstream'
		});
	});

	it('enforces byte and relationship count caps without returning partial projections', async () => {
		const config = loadDeploymentConfig(environment);
		const oversized = jsonResponse({
			data: { entitiesByPrefix: [] },
			padding: 'x'.repeat(MAX_GRAPHQL_RESPONSE_BYTES)
		});
		const byteLimited = createWorldProjectionAdapter(config, queuedFetch([oversized]));
		await expect(byteLimited.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code: 'projection_capacity_exceeded'
		});

		const place = entity(LOCATION_A);
		place.triples.push({
			subject: LOCATION_A,
			predicate: 'location.relation.connects-to',
			object: LOCATION_B,
			datatype: '@id'
		});
		const relationship = {
			from_entity_id: LOCATION_A,
			to_entity_id: LOCATION_B,
			edge_type: 'location.relation.connects-to'
		};
		const countLimited = createWorldProjectionAdapter(
			config,
			queuedFetch([
				jsonResponse({ data: { entitiesByPrefix: [place, entity(LOCATION_B)] } }),
				jsonResponse({
					data: {
						relationships: Array.from(
							{ length: MAX_RELATIONSHIPS_PER_PLACE + 1 },
							() => relationship
						)
					}
				})
			])
		);
		await expect(countLimited.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject(
			{
				code: 'projection_capacity_exceeded'
			}
		);
	});

	it.each([
		[
			'duplicate label',
			(candidate: ReturnType<typeof entity>) =>
				candidate.triples.push({
					subject: LOCATION_A,
					predicate: 'world.entity.name',
					object: 'Other',
					datatype: 'xsd:string'
				})
		],
		[
			'partial coordinate',
			(candidate: ReturnType<typeof entity>) =>
				candidate.triples.push({
					subject: LOCATION_A,
					predicate: 'geo.location.latitude',
					object: 42,
					datatype: ''
				})
		],
		[
			'malformed coordinate',
			(candidate: ReturnType<typeof entity>) =>
				candidate.triples.push(
					{
						subject: LOCATION_A,
						predicate: 'geo.location.latitude',
						object: 'north',
						datatype: ''
					},
					{ subject: LOCATION_A, predicate: 'geo.location.longitude', object: 2, datatype: '' }
				)
		],
		[
			'foreign triple subject',
			(candidate: ReturnType<typeof entity>) =>
				candidate.triples.push({
					subject: FOREIGN_LOCATION,
					predicate: 'world.entity.name',
					object: 'Other',
					datatype: 'xsd:string'
				})
		]
	])('rejects over-broad or malformed place data: %s', async (_name, mutate) => {
		const candidate = entity(LOCATION_A);
		mutate(candidate);
		const config = loadDeploymentConfig(environment);
		const adapter = createWorldProjectionAdapter(
			config,
			queuedFetch([jsonResponse({ data: { entitiesByPrefix: [candidate] } })])
		);
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			name: 'ProjectionError'
		});
	});

	it.each([
		[
			'string object with an untyped scalar datatype',
			(candidate: ReturnType<typeof entity>) =>
				candidate.triples.push({
					subject: LOCATION_A,
					predicate: 'world.entity.description',
					object: 'private',
					datatype: ''
				}),
			'invalid_upstream'
		],
		[
			'coordinate with a non-canonical numeric datatype',
			(candidate: ReturnType<typeof entity>) =>
				candidate.triples.push(
					{
						subject: LOCATION_A,
						predicate: 'geo.location.latitude',
						object: 42,
						datatype: 'xsd:double'
					},
					{ subject: LOCATION_A, predicate: 'geo.location.longitude', object: 2 }
				),
			'invalid_upstream'
		],
		[
			'foreign @id hidden behind a non-projected predicate',
			(candidate: ReturnType<typeof entity>) =>
				candidate.triples.push({
					subject: LOCATION_A,
					predicate: 'world.entity.belongs-to',
					object: FOREIGN_LOCATION,
					datatype: '@id'
				}),
			'scope_violation'
		]
	])('rejects datatype or hidden-reference violation: %s', async (_name, mutate, code) => {
		const config = loadDeploymentConfig(environment);
		const candidate = entity(LOCATION_A);
		mutate(candidate);
		const adapter = createWorldProjectionAdapter(
			config,
			queuedFetch([jsonResponse({ data: { entitiesByPrefix: [candidate] } })])
		);
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code
		});
	});

	it('returns not_configured without issuing an exact entity query', async () => {
		const fetcher = queuedFetch([jsonResponse({ data: { entitiesByPrefix: [] } })]);
		const config = loadDeploymentConfig(environment);
		const result = await createWorldProjectionAdapter(config, fetcher).projectWorld(
			issueProjectionPrincipal(config)
		);
		expect(result.clock).toEqual({ state: 'not_configured' });
		expect(fetcher).toHaveBeenCalledTimes(1);
	});

	it.each([
		['missing', []],
		['ambiguous', [1, 2]],
		['malformed', ['noon']]
	])('rejects a configured clock fact that is %s', async (_name, values) => {
		const clockEnvironment = {
			...environment,
			SEMMACHINA_CLOCK_ENTITY_ID: CLOCK_ID,
			SEMMACHINA_CLOCK_PREDICATE: 'campaign.clock.current',
			SEMMACHINA_CLOCK_LABEL: 'Village time',
			SEMMACHINA_CLOCK_UNIT: 'minute',
			SEMMACHINA_CLOCK_VALUE_TYPE: 'number'
		};
		const clockEntity = {
			id: CLOCK_ID,
			triples: values.map((value) => ({
				subject: CLOCK_ID,
				predicate: 'campaign.clock.current',
				object: value
			}))
		};
		const config = loadDeploymentConfig(clockEnvironment);
		const adapter = createWorldProjectionAdapter(
			config,
			queuedFetch([
				jsonResponse({ data: { entitiesByPrefix: [] } }),
				jsonResponse({ data: { entity: clockEntity } })
			])
		);
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code: 'invalid_clock'
		});
	});

	it('uses the fixed exact entity query for a configured clock', async () => {
		const config = loadDeploymentConfig({
			...environment,
			SEMMACHINA_CLOCK_ENTITY_ID: CLOCK_ID,
			SEMMACHINA_CLOCK_PREDICATE: 'campaign.clock.current',
			SEMMACHINA_CLOCK_LABEL: 'Village time',
			SEMMACHINA_CLOCK_UNIT: 'minute',
			SEMMACHINA_CLOCK_VALUE_TYPE: 'number'
		});
		const fetcher = queuedFetch([
			jsonResponse({ data: { entitiesByPrefix: [] } }),
			jsonResponse({
				data: {
					entity: {
						id: CLOCK_ID,
						triples: [
							{
								subject: CLOCK_ID,
								predicate: 'campaign.clock.current',
								object: 317,
								datatype: ''
							}
						]
					}
				}
			})
		]);
		const result = await createWorldProjectionAdapter(config, fetcher).projectWorld(
			issueProjectionPrincipal(config)
		);
		expect(result.clock).toEqual({
			state: 'configured',
			label: 'Village time',
			value: 317,
			unit: 'minute'
		});
		const finalBody = JSON.parse(String(fetcher.mock.calls.at(-1)?.[1]?.body));
		expect(finalBody).toEqual({ query: ENTITY_QUERY, variables: { id: CLOCK_ID } });
	});

	it('rejects a configured clock number with a non-canonical datatype', async () => {
		const config = loadDeploymentConfig({
			...environment,
			SEMMACHINA_CLOCK_ENTITY_ID: CLOCK_ID,
			SEMMACHINA_CLOCK_PREDICATE: 'campaign.clock.current',
			SEMMACHINA_CLOCK_LABEL: 'Village time',
			SEMMACHINA_CLOCK_UNIT: 'minute',
			SEMMACHINA_CLOCK_VALUE_TYPE: 'number'
		});
		const adapter = createWorldProjectionAdapter(
			config,
			queuedFetch([
				jsonResponse({ data: { entitiesByPrefix: [] } }),
				jsonResponse({
					data: {
						entity: {
							id: CLOCK_ID,
							triples: [
								{
									subject: CLOCK_ID,
									predicate: 'campaign.clock.current',
									object: 317,
									datatype: 'xsd:double'
								}
							]
						}
					}
				})
			])
		);
		await expect(adapter.projectWorld(issueProjectionPrincipal(config))).rejects.toMatchObject({
			code: 'invalid_clock'
		});
	});

	it('supports 999 places, calls, and validated raw edges without a self connection', async () => {
		const config = loadDeploymentConfig({
			...environment,
			SEMMACHINA_GRAPHQL_PROJECTION_DEADLINE_MS: '30000'
		});
		const locations = Array.from({ length: 999 }, (_, index) =>
			entity(
				`c360.semmachina.bellweather.bellweather-maze.location.place-${String(index).padStart(3, '0')}`,
				`Place ${index}`
			)
		);
		const ids = locations.map((location) => location.id);
		const source = locations[0];
		source.triples.push(
			...ids.slice(1).map((target) => ({
				subject: source.id,
				predicate: 'location.relation.connects-to',
				object: target,
				datatype: '@id'
			}))
		);
		const relationships = [
			...ids.slice(1).map((target) => ({
				from_entity_id: source.id,
				to_entity_id: target,
				edge_type: 'location.relation.connects-to'
			})),
			{
				from_entity_id: source.id,
				to_entity_id: CLOCK_ID,
				edge_type: 'world.entity.belongs-to'
			}
		];
		const fetcher = vi.fn<typeof fetch>(async (_input, init) => {
			const body = JSON.parse(String(init?.body)) as {
				query: string;
				variables: { entityId?: string };
			};
			if (body.query === LOCATIONS_QUERY) {
				return jsonResponse({ data: { entitiesByPrefix: locations } });
			}
			return jsonResponse({
				data: { relationships: body.variables.entityId === source.id ? relationships : [] }
			});
		});
		const projection = await createWorldProjectionAdapter(config, fetcher).projectWorld(
			issueProjectionPrincipal(config)
		);
		expect(projection.places).toHaveLength(999);
		expect(projection.places.find((place) => place.id === source.id)?.connections).toHaveLength(
			MAX_RELATIONSHIPS_PER_PLACE - 1
		);
		expect(relationships).toHaveLength(MAX_RELATIONSHIPS_PER_PLACE);
		expect(fetcher).toHaveBeenCalledTimes(1000);
	});

	it.each(['request', 'body'] as const)(
		'aborts a stalled upstream %s at the overall projection deadline',
		async (stall) => {
			vi.useFakeTimers();
			try {
				const config = loadDeploymentConfig(environment);
				const fetcher = vi.fn<typeof fetch>(async () => {
					if (stall === 'request') return new Promise<Response>(() => undefined);
					return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
						headers: { 'content-type': 'application/json' }
					});
				});
				const pending = createWorldProjectionAdapter(config, fetcher, {
					deadlineMs: 25
				}).projectWorld(issueProjectionPrincipal(config));
				const assertion = expect(pending).rejects.toMatchObject({ code: 'upstream_unavailable' });
				await vi.advanceTimersByTimeAsync(25);
				await assertion;
			} finally {
				vi.useRealTimers();
			}
		}
	);

	it('does not expose or call unsafe global spatial search', () => {
		const fetcher = vi.fn<typeof fetch>();
		const adapter = createWorldProjectionAdapter(loadDeploymentConfig(environment), fetcher);
		expect(adapter).not.toHaveProperty('spatialSearch');
		expect(fetcher).not.toHaveBeenCalled();
	});
});
