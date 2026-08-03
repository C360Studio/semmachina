import { describe, expect, it } from 'vitest';

import { loadDeploymentConfig, type DeploymentEnvironment } from './deployment-config';
import { loadSurfaceConfig } from './surface-config';

const safeEnvironment: DeploymentEnvironment = {
	SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:8080/graphql',
	SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
	SEMMACHINA_WORLD_ORG: 'c360',
	SEMMACHINA_WORLD_NAMESPACE: 'bellweather',
	SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze',
	SEMMACHINA_PUBLIC_ORIGIN: 'https://play.example.test',
	SEMMACHINA_TLS_POSTURE: 'trusted_loopback_proxy',
	SEMMACHINA_CREATOR_CREDENTIAL: 'creator-secret-that-is-long',
	SEMMACHINA_PLAYER_BEARER: 'player-bearer-that-is-distinct',
	SEMMACHINA_PLAYER_WS_URL: 'ws://127.0.0.1:8081/play',
	SEMMACHINA_PLAYER_ID: 'c360.semmachina.bellweather.bellweather-maze.player.detective'
};

function load(overrides: DeploymentEnvironment = {}) {
	const environment = { ...safeEnvironment, ...overrides };
	return loadSurfaceConfig(environment, loadDeploymentConfig(environment));
}

describe('surface deployment configuration', () => {
	it('loads and freezes the bounded server-owned mapping', () => {
		const config = load();
		expect(config.publicOrigin).toBe('https://play.example.test');
		expect(config.tlsPosture).toBe('trusted_loopback_proxy');
		expect(config.sessionTtlSeconds).toBe(300);
		expect(config.player).toEqual({
			id: safeEnvironment.SEMMACHINA_PLAYER_ID,
			bearer: safeEnvironment.SEMMACHINA_PLAYER_BEARER,
			wsUrl: safeEnvironment.SEMMACHINA_PLAYER_WS_URL
		});
		expect(Object.isFrozen(config)).toBe(true);
		expect(Object.isFrozen(config.player)).toBe(true);
	});

	it.each([
		['SEMMACHINA_PUBLIC_ORIGIN', 'http://play.example.test'],
		['SEMMACHINA_PUBLIC_ORIGIN', 'https://play.example.test/path'],
		['SEMMACHINA_TLS_POSTURE', 'direct_tls'],
		['SEMMACHINA_PLAYER_WS_URL', 'wss://player.example.test/play'],
		['SEMMACHINA_PLAYER_WS_URL', 'ws://10.0.0.2/play'],
		['SEMMACHINA_PLAYER_WS_URL', 'ws://127.0.0.1/play?player=other'],
		['SEMMACHINA_PLAYER_ID', 'c360.semmachina.bellweather.bellweather-maze.npc.detective'],
		['SEMMACHINA_PLAYER_ID', 'other.semmachina.bellweather.bellweather-maze.player.detective'],
		['SEMMACHINA_SESSION_TTL_SECONDS', '59'],
		['SEMMACHINA_SESSION_TTL_SECONDS', '3601'],
		['HOST', '0.0.0.0'],
		['ADDRESS_HEADER', 'x-forwarded-for'],
		['XFF_DEPTH', '1']
	] as const)('refuses unsafe %s configuration', (key, value) => {
		expect(() => load({ [key]: value })).toThrow();
	});

	it('requires creator and player credentials to be distinct', () => {
		expect(() =>
			load({ SEMMACHINA_PLAYER_BEARER: safeEnvironment.SEMMACHINA_CREATOR_CREDENTIAL })
		).toThrow();
	});

	it('accepts only the configured TTL range', () => {
		expect(load({ SEMMACHINA_SESSION_TTL_SECONDS: '60' }).sessionTtlSeconds).toBe(60);
		expect(load({ SEMMACHINA_SESSION_TTL_SECONDS: '3600' }).sessionTtlSeconds).toBe(3600);
	});
});
