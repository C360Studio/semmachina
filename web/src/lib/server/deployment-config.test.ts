import { describe, expect, it } from 'vitest';

import { loadDeploymentConfig } from './deployment-config';

const baseEnvironment = {
	SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:8080/graphql',
	SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
	SEMMACHINA_WORLD_ORG: 'c360',
	SEMMACHINA_WORLD_NAMESPACE: 'bellweather',
	SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze'
} as const;

describe('loadDeploymentConfig', () => {
	it('derives and freezes the active base and location prefixes on the server', () => {
		const config = loadDeploymentConfig(baseEnvironment);
		expect(config.scope).toEqual({
			organization: 'c360',
			platform: 'semmachina',
			worldNamespace: 'bellweather',
			template: 'bellweather-maze',
			basePrefix: 'c360.semmachina.bellweather.bellweather-maze',
			locationPrefix: 'c360.semmachina.bellweather.bellweather-maze.location'
		});
		expect(Object.isFrozen(config)).toBe(true);
		expect(Object.isFrozen(config.scope)).toBe(true);
	});

	it.each([
		['SEMMACHINA_GRAPHQL_URL', ''],
		['SEMMACHINA_GRAPHQL_URL', 'file:///tmp/graph'],
		['SEMMACHINA_GRAPHQL_URL', 'http://user:secret@127.0.0.1/graphql'],
		['SEMMACHINA_WORLD_ORG', 'bad.org'],
		['SEMMACHINA_WORLD_NAMESPACE', ''],
		['SEMMACHINA_WORLD_TEMPLATE', 'bad/template'],
		['SEMMACHINA_GRAPHQL_POSTURE', 'private_network']
	])('fails closed for invalid server configuration %s=%s', (key, value) => {
		expect(() => loadDeploymentConfig({ ...baseEnvironment, [key]: value })).toThrow();
	});

	it('requires the endpoint to match its declared deployment posture', () => {
		expect(() =>
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'http://graph.example.test/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: 'loopback'
			})
		).toThrow();
		expect(() =>
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'http://localhost:8080/graphql'
			})
		).toThrow();
		expect(() =>
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'http://graph.example.test/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: 'auth_proxy'
			})
		).toThrow();
		expect(() =>
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'https://graph.example.test/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: 'auth_proxy'
			})
		).toThrow();
		expect(
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'https://graph.example.test/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: 'auth_proxy',
				SEMMACHINA_GRAPHQL_AUTH_TOKEN: 'server-only-proxy-token'
			}).graphql.posture
		).toBe('auth_proxy');
		expect(() =>
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'http://192.168.1.20/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: undefined
			})
		).toThrow();
		expect(
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'http://graph.internal/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: 'network_policy'
			}).graphql.posture
		).toBe('network_policy');
		expect(
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_URL: 'http://[::1]:8080/graphql'
			}).graphql.posture
		).toBe('loopback');
		expect(() =>
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_AUTH_TOKEN: 'server-only-proxy-token'
			})
		).toThrow();
	});

	it('creates a fresh opaque deployment identity for each assembled configuration', () => {
		expect(loadDeploymentConfig(baseEnvironment).deploymentInstance).not.toBe(
			loadDeploymentConfig(baseEnvironment).deploymentInstance
		);
	});

	it('pins prefix enumeration to beta.159 capacity rather than browser or env authority', () => {
		expect(
			loadDeploymentConfig({ ...baseEnvironment, SEMMACHINA_GRAPHQL_PLACE_LIMIT: '3' }).graphql
				.placeLimit
		).toBe(1000);
	});

	it('validates the server-fixed overall projection deadline', () => {
		expect(loadDeploymentConfig(baseEnvironment).graphql.projectionDeadlineMs).toBe(5000);
		expect(
			loadDeploymentConfig({
				...baseEnvironment,
				SEMMACHINA_GRAPHQL_PROJECTION_DEADLINE_MS: '30000'
			}).graphql.projectionDeadlineMs
		).toBe(30000);
		for (const value of ['999', '30001', '1.5']) {
			expect(() =>
				loadDeploymentConfig({
					...baseEnvironment,
					SEMMACHINA_GRAPHQL_PROJECTION_DEADLINE_MS: value
				})
			).toThrow();
		}
	});

	it('returns an explicit not-configured clock when every clock key is absent', () => {
		expect(loadDeploymentConfig(baseEnvironment).clock).toEqual({ state: 'not_configured' });
	});

	it('accepts one exact in-scope typed clock fact from server configuration', () => {
		const config = loadDeploymentConfig({
			...baseEnvironment,
			SEMMACHINA_CLOCK_ENTITY_ID: 'c360.semmachina.bellweather.bellweather-maze.campaign.main',
			SEMMACHINA_CLOCK_PREDICATE: 'campaign.clock.current',
			SEMMACHINA_CLOCK_LABEL: 'Village time',
			SEMMACHINA_CLOCK_UNIT: 'minute',
			SEMMACHINA_CLOCK_VALUE_TYPE: 'number'
		});
		expect(config.clock).toEqual({
			state: 'configured',
			entityId: 'c360.semmachina.bellweather.bellweather-maze.campaign.main',
			predicate: 'campaign.clock.current',
			label: 'Village time',
			unit: 'minute',
			valueType: 'number'
		});
	});

	it.each([
		[
			'only entity',
			{ SEMMACHINA_CLOCK_ENTITY_ID: 'c360.semmachina.bellweather.bellweather-maze.campaign.main' }
		],
		[
			'foreign entity',
			{
				SEMMACHINA_CLOCK_ENTITY_ID: 'other.semmachina.bellweather.bellweather-maze.campaign.main',
				SEMMACHINA_CLOCK_PREDICATE: 'campaign.clock.current',
				SEMMACHINA_CLOCK_LABEL: 'Village time',
				SEMMACHINA_CLOCK_UNIT: 'minute',
				SEMMACHINA_CLOCK_VALUE_TYPE: 'number'
			}
		],
		[
			'malformed predicate',
			{
				SEMMACHINA_CLOCK_ENTITY_ID: 'c360.semmachina.bellweather.bellweather-maze.campaign.main',
				SEMMACHINA_CLOCK_PREDICATE: 'clock',
				SEMMACHINA_CLOCK_LABEL: 'Village time',
				SEMMACHINA_CLOCK_UNIT: 'minute',
				SEMMACHINA_CLOCK_VALUE_TYPE: 'number'
			}
		],
		[
			'unknown value type',
			{
				SEMMACHINA_CLOCK_ENTITY_ID: 'c360.semmachina.bellweather.bellweather-maze.campaign.main',
				SEMMACHINA_CLOCK_PREDICATE: 'campaign.clock.current',
				SEMMACHINA_CLOCK_LABEL: 'Village time',
				SEMMACHINA_CLOCK_UNIT: 'minute',
				SEMMACHINA_CLOCK_VALUE_TYPE: 'timestamp'
			}
		]
	])('refuses partial or unsafe clock configuration: %s', (_name, clockEnvironment) => {
		expect(() => loadDeploymentConfig({ ...baseEnvironment, ...clockEnvironment })).toThrow();
	});
});
