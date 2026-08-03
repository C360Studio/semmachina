const COMPONENT = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;
const PREDICATE = /^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$/;
const MAX_ENTITY_ID_BYTES = 256;
const PLACE_QUERY_LIMIT = 1000;
const DEFAULT_PROJECTION_DEADLINE_MS = 5000;
const MIN_PROJECTION_DEADLINE_MS = 1000;
const MAX_PROJECTION_DEADLINE_MS = 30_000;
const AUTH_TOKEN = /^[A-Za-z0-9._~+/=-]{16,4096}$/;

declare const deploymentInstanceBrand: unique symbol;

export interface DeploymentInstanceIdentity {
	readonly [deploymentInstanceBrand]: true;
}

export type GraphQLPosture = 'loopback' | 'network_policy' | 'auth_proxy';

export interface DeploymentScope {
	readonly organization: string;
	readonly platform: 'semmachina';
	readonly worldNamespace: string;
	readonly template: string;
	readonly basePrefix: string;
	readonly locationPrefix: string;
}

export type ClockConfig =
	| { readonly state: 'not_configured' }
	| {
			readonly state: 'configured';
			readonly entityId: string;
			readonly predicate: string;
			readonly label: string;
			readonly unit: string;
			readonly valueType: 'number';
	  };

export interface DeploymentConfig {
	readonly deploymentInstance: DeploymentInstanceIdentity;
	readonly graphql: {
		readonly endpoint: string;
		readonly posture: GraphQLPosture;
		readonly placeLimit: number;
		readonly projectionDeadlineMs: number;
		readonly authentication:
			{ readonly kind: 'none' } | { readonly kind: 'bearer'; readonly token: string };
	};
	readonly scope: DeploymentScope;
	readonly clock: ClockConfig;
}

export type DeploymentEnvironment = Readonly<Record<string, string | undefined>>;

function required(environment: DeploymentEnvironment, key: string): string {
	const value = environment[key];
	if (value === undefined || value === '') throw new Error(`missing ${key}`);
	return value;
}

function component(environment: DeploymentEnvironment, key: string): string {
	const value = required(environment, key);
	if (!COMPONENT.test(value)) throw new Error(`invalid ${key}`);
	return value;
}

function parseIPv4(hostname: string): number[] | undefined {
	const parts = hostname.split('.');
	if (parts.length !== 4) return undefined;
	const octets = parts.map(Number);
	if (
		octets.some(
			(value, index) =>
				!Number.isInteger(value) || value < 0 || value > 255 || String(value) !== parts[index]
		)
	) {
		return undefined;
	}
	return octets;
}

function isLoopback(hostname: string): boolean {
	if (hostname === '[::1]') return true;
	const octets = parseIPv4(hostname);
	return octets?.[0] === 127;
}

function graphqlConfig(environment: DeploymentEnvironment): DeploymentConfig['graphql'] {
	const rawEndpoint = required(environment, 'SEMMACHINA_GRAPHQL_URL');
	let endpoint: URL;
	try {
		endpoint = new URL(rawEndpoint);
	} catch {
		throw new Error('invalid SEMMACHINA_GRAPHQL_URL');
	}
	if (
		!['http:', 'https:'].includes(endpoint.protocol) ||
		endpoint.username !== '' ||
		endpoint.password !== '' ||
		endpoint.pathname !== '/graphql' ||
		endpoint.search !== '' ||
		endpoint.hash !== ''
	) {
		throw new Error('unsafe SEMMACHINA_GRAPHQL_URL');
	}

	const posture = required(environment, 'SEMMACHINA_GRAPHQL_POSTURE');
	if (!['loopback', 'network_policy', 'auth_proxy'].includes(posture)) {
		throw new Error('invalid SEMMACHINA_GRAPHQL_POSTURE');
	}
	if (posture === 'loopback' && !isLoopback(endpoint.hostname)) {
		throw new Error('GraphQL loopback posture requires a loopback endpoint');
	}
	if (posture === 'auth_proxy' && endpoint.protocol !== 'https:') {
		throw new Error('GraphQL auth proxy posture requires HTTPS');
	}
	const authToken = environment.SEMMACHINA_GRAPHQL_AUTH_TOKEN;
	if (posture === 'auth_proxy' && (authToken === undefined || !AUTH_TOKEN.test(authToken))) {
		throw new Error('GraphQL auth proxy posture requires a valid server token');
	}
	if (posture !== 'auth_proxy' && authToken !== undefined) {
		throw new Error('GraphQL auth token requires auth_proxy posture');
	}
	const rawDeadline = environment.SEMMACHINA_GRAPHQL_PROJECTION_DEADLINE_MS;
	if (rawDeadline !== undefined && !/^\d+$/.test(rawDeadline)) {
		throw new Error('invalid GraphQL projection deadline');
	}
	const projectionDeadlineMs =
		rawDeadline === undefined ? DEFAULT_PROJECTION_DEADLINE_MS : Number(rawDeadline);
	if (
		projectionDeadlineMs < MIN_PROJECTION_DEADLINE_MS ||
		projectionDeadlineMs > MAX_PROJECTION_DEADLINE_MS
	) {
		throw new Error('GraphQL projection deadline outside supported range');
	}

	return Object.freeze({
		endpoint: endpoint.toString(),
		posture: posture as GraphQLPosture,
		placeLimit: PLACE_QUERY_LIMIT,
		projectionDeadlineMs,
		authentication:
			posture === 'auth_proxy'
				? Object.freeze({ kind: 'bearer' as const, token: authToken as string })
				: Object.freeze({ kind: 'none' as const })
	});
}

function clockConfig(environment: DeploymentEnvironment, scope: DeploymentScope): ClockConfig {
	const keys = [
		'SEMMACHINA_CLOCK_ENTITY_ID',
		'SEMMACHINA_CLOCK_PREDICATE',
		'SEMMACHINA_CLOCK_LABEL',
		'SEMMACHINA_CLOCK_UNIT',
		'SEMMACHINA_CLOCK_VALUE_TYPE'
	] as const;
	const present = keys.filter((key) => environment[key] !== undefined && environment[key] !== '');
	if (present.length === 0) return Object.freeze({ state: 'not_configured' });
	if (present.length !== keys.length) throw new Error('partial clock configuration');

	const entityId = required(environment, 'SEMMACHINA_CLOCK_ENTITY_ID');
	if (!isScopedEntityId(entityId, scope.basePrefix))
		throw new Error('clock entity is outside deployment scope');
	const predicate = required(environment, 'SEMMACHINA_CLOCK_PREDICATE');
	if (!PREDICATE.test(predicate)) throw new Error('invalid clock predicate');
	const label = required(environment, 'SEMMACHINA_CLOCK_LABEL');
	const unit = required(environment, 'SEMMACHINA_CLOCK_UNIT');
	if (label.length > 128 || unit.length > 32) throw new Error('clock display metadata too long');
	const valueType = required(environment, 'SEMMACHINA_CLOCK_VALUE_TYPE');
	if (valueType !== 'number') throw new Error('unsupported clock value type');
	return Object.freeze({ state: 'configured', entityId, predicate, label, unit, valueType });
}

export function isCanonicalEntityId(value: string): boolean {
	return (
		value.length <= MAX_ENTITY_ID_BYTES &&
		value.split('.').length === 6 &&
		value.split('.').every((part) => COMPONENT.test(part))
	);
}

export function isScopedEntityId(value: string, prefix: string): boolean {
	return isCanonicalEntityId(value) && value.split('.').slice(0, 4).join('.') === prefix;
}

export function isLocationEntityId(value: string, locationPrefix: string): boolean {
	return isCanonicalEntityId(value) && value.split('.').slice(0, 5).join('.') === locationPrefix;
}

export function isCanonicalPredicate(value: string): boolean {
	return PREDICATE.test(value);
}

export function loadDeploymentConfig(environment: DeploymentEnvironment): DeploymentConfig {
	const organization = component(environment, 'SEMMACHINA_WORLD_ORG');
	const worldNamespace = component(environment, 'SEMMACHINA_WORLD_NAMESPACE');
	const template = component(environment, 'SEMMACHINA_WORLD_TEMPLATE');
	const basePrefix = `${organization}.semmachina.${worldNamespace}.${template}`;
	const locationPrefix = `${basePrefix}.location`;
	const scope = Object.freeze({
		organization,
		platform: 'semmachina' as const,
		worldNamespace,
		template,
		basePrefix,
		locationPrefix
	});
	const config = {
		deploymentInstance: Object.freeze({}) as DeploymentInstanceIdentity,
		graphql: graphqlConfig(environment),
		scope,
		clock: clockConfig(environment, scope)
	};
	return Object.freeze(config);
}
