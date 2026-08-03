import { describe, expect, it } from 'vitest';

import { loadDeploymentConfig, type DeploymentEnvironment } from './deployment-config';
import { loadSurfaceConfig } from './surface-config';
import { createTrustedProxyBoundary, type RawTransportRequest } from './transport-boundary';

const environment: DeploymentEnvironment = {
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

function raw(overrides: Partial<RawTransportRequest> = {}): RawTransportRequest {
	return {
		rawHeaders: ['Host', 'play.example.test', 'X-Forwarded-Proto', 'https'],
		socket: { remoteAddress: '127.0.0.1' },
		headers: { host: 'play.example.test', 'x-forwarded-proto': 'https' },
		...overrides
	};
}

describe('raw trusted proxy boundary', () => {
	const deployment = loadDeploymentConfig(environment);
	const surface = loadSurfaceConfig(environment, deployment);
	const token = 'a'.repeat(43);

	it('overwrites a spoofed internal header and issues only its per-process attestation', () => {
		const boundary = createTrustedProxyBoundary(surface, token);
		const incoming = raw({
			rawHeaders: [
				'Host',
				'play.example.test',
				'X-Forwarded-Proto',
				'https',
				'X-SemMachina-Internal-Transport',
				'attacker'
			],
			headers: { 'x-semmachina-internal-transport': 'attacker' }
		});
		expect(boundary.attestRawRequest(incoming)).toBe(true);
		expect(incoming.headers['x-semmachina-internal-transport']).toBe(token);
		expect(
			boundary.isAttestedRequest(
				new Request('https://play.example.test/', {
					headers: { 'x-semmachina-internal-transport': 'attacker' }
				})
			)
		).toBe(false);
	});

	it.each([
		['remote peer', raw({ socket: { remoteAddress: '10.0.0.2' } })],
		['missing Host', raw({ rawHeaders: ['X-Forwarded-Proto', 'https'] })],
		[
			'duplicate Host',
			raw({
				rawHeaders: [
					'Host',
					'play.example.test',
					'Host',
					'evil.example.test',
					'X-Forwarded-Proto',
					'https'
				]
			})
		],
		[
			'duplicate proxy proof',
			raw({
				rawHeaders: [
					'Host',
					'play.example.test',
					'X-Forwarded-Proto',
					'https',
					'X-Forwarded-Proto',
					'http'
				]
			})
		],
		[
			'combined proxy proof',
			raw({
				rawHeaders: ['Host', 'play.example.test', 'X-Forwarded-Proto', 'https, http']
			})
		]
	])('refuses %s before issuing an attestation', (_name, incoming) => {
		const boundary = createTrustedProxyBoundary(surface, token);
		expect(boundary.attestRawRequest(incoming)).toBe(false);
		expect(incoming.headers['x-semmachina-internal-transport']).toBeUndefined();
	});
});
