import type {
	DeploymentConfig,
	DeploymentEnvironment,
	DeploymentInstanceIdentity
} from './deployment-config';
import { isCanonicalEntityId, isScopedEntityId } from './deployment-config';

const SECRET = /^[\x21-\x7e]{16,4096}$/;
const DEFAULT_SESSION_TTL_SECONDS = 300;
const MIN_SESSION_TTL_SECONDS = 60;
const MAX_SESSION_TTL_SECONDS = 3600;

export interface SurfaceConfig {
	readonly deploymentInstance: DeploymentInstanceIdentity;
	readonly publicOrigin: string;
	readonly publicHost: string;
	readonly tlsPosture: 'trusted_loopback_proxy';
	readonly creatorCredential: string;
	readonly sessionTtlSeconds: number;
	readonly player: {
		readonly id: string;
		readonly bearer: string;
		readonly wsUrl: string;
	};
}

function required(environment: DeploymentEnvironment, key: string): string {
	const value = environment[key];
	if (value === undefined || value === '') throw new Error(`missing ${key}`);
	return value;
}

function publicOrigin(environment: DeploymentEnvironment): URL {
	let value: URL;
	try {
		value = new URL(required(environment, 'SEMMACHINA_PUBLIC_ORIGIN'));
	} catch {
		throw new Error('invalid SEMMACHINA_PUBLIC_ORIGIN');
	}
	if (
		value.protocol !== 'https:' ||
		value.username !== '' ||
		value.password !== '' ||
		value.pathname !== '/' ||
		value.search !== '' ||
		value.hash !== '' ||
		value.origin === 'null'
	) {
		throw new Error('unsafe SEMMACHINA_PUBLIC_ORIGIN');
	}
	return value;
}

function isLiteralLoopback(hostname: string): boolean {
	if (hostname === '[::1]') return true;
	const octets = hostname.split('.');
	return (
		octets.length === 4 &&
		octets[0] === '127' &&
		octets.every((octet) => /^\d+$/.test(octet) && Number(octet) <= 255)
	);
}

function playerWebSocketUrl(environment: DeploymentEnvironment): string {
	let value: URL;
	try {
		value = new URL(required(environment, 'SEMMACHINA_PLAYER_WS_URL'));
	} catch {
		throw new Error('invalid SEMMACHINA_PLAYER_WS_URL');
	}
	if (
		value.protocol !== 'ws:' ||
		!isLiteralLoopback(value.hostname) ||
		value.username !== '' ||
		value.password !== '' ||
		value.pathname !== '/play' ||
		value.search !== '' ||
		value.hash !== ''
	) {
		throw new Error('unsafe SEMMACHINA_PLAYER_WS_URL');
	}
	return value.toString();
}

function sessionTtl(environment: DeploymentEnvironment): number {
	const raw = environment.SEMMACHINA_SESSION_TTL_SECONDS;
	if (raw === undefined) return DEFAULT_SESSION_TTL_SECONDS;
	if (!/^\d+$/.test(raw)) throw new Error('invalid SEMMACHINA_SESSION_TTL_SECONDS');
	const value = Number(raw);
	if (value < MIN_SESSION_TTL_SECONDS || value > MAX_SESSION_TTL_SECONDS) {
		throw new Error('SEMMACHINA_SESSION_TTL_SECONDS outside supported range');
	}
	return value;
}

export function loadSurfaceConfig(
	environment: DeploymentEnvironment,
	deployment: DeploymentConfig
): SurfaceConfig {
	const origin = publicOrigin(environment);
	if (environment.ADDRESS_HEADER !== undefined || environment.XFF_DEPTH !== undefined) {
		throw new Error('adapter proxy address environment is forbidden');
	}
	if (required(environment, 'SEMMACHINA_TLS_POSTURE') !== 'trusted_loopback_proxy') {
		throw new Error('unsupported SEMMACHINA_TLS_POSTURE');
	}
	const bindHost = environment.HOST ?? '127.0.0.1';
	if (!isLiteralLoopback(bindHost === '::1' ? '[::1]' : bindHost)) {
		throw new Error('trusted loopback proxy posture requires loopback HOST');
	}
	const creatorCredential = required(environment, 'SEMMACHINA_CREATOR_CREDENTIAL');
	const playerBearer = required(environment, 'SEMMACHINA_PLAYER_BEARER');
	if (!SECRET.test(creatorCredential) || !SECRET.test(playerBearer)) {
		throw new Error('invalid server credential');
	}
	if (creatorCredential === playerBearer) throw new Error('server credentials must be distinct');
	const playerId = required(environment, 'SEMMACHINA_PLAYER_ID');
	if (
		!isCanonicalEntityId(playerId) ||
		!isScopedEntityId(playerId, deployment.scope.basePrefix) ||
		playerId.split('.')[4] !== 'player'
	) {
		throw new Error('SEMMACHINA_PLAYER_ID is outside the configured player scope');
	}
	return Object.freeze({
		deploymentInstance: deployment.deploymentInstance,
		publicOrigin: origin.origin,
		publicHost: origin.host,
		tlsPosture: 'trusted_loopback_proxy' as const,
		creatorCredential,
		sessionTtlSeconds: sessionTtl(environment),
		player: Object.freeze({
			id: playerId,
			bearer: playerBearer,
			wsUrl: playerWebSocketUrl(environment)
		})
	});
}
