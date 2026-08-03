import { createHash, randomBytes, timingSafeEqual } from 'node:crypto';
import { inspect } from 'node:util';

import type { DeploymentConfig } from './deployment-config';
import {
	issueProjectionPrincipal,
	type AuthenticatedProjectionPrincipal
} from './projection-principal';
import type { SurfaceConfig } from './surface-config';

const PREAUTH_COOKIE = '__Host-semmachina_preauth';
const SESSION_COOKIE = '__Host-semmachina_session';
const MAX_LOGIN_BODY_BYTES = 8192;
const MAX_PREAUTH_PROOFS = 1024;
const MAX_SESSIONS = 1024;
const PREAUTH_TTL_MS = 60_000;

interface Proof {
	readonly csrf: string;
	readonly expiresAt: number;
}

interface SessionProof extends Proof {
	readonly lease: SessionLease;
	readonly controller: AbortController;
}

function isSessionProof(proof: Proof): proof is SessionProof {
	return 'controller' in proof && proof.controller instanceof AbortController;
}

export interface SessionLease {
	readonly identity: object;
	readonly expiresAt: number;
	readonly signal: AbortSignal;
}

export interface UpgradeAuthorization {
	readonly playerId: string;
	readonly playerBearer: string;
	readonly playerWsUrl: string;
	readonly protocol: 'semmachina.player.v1';
	readonly lease: SessionLease;
}

export interface SurfaceSessionAuthority {
	readonly handlePreauth: (request: Request) => Promise<Response>;
	readonly handleLogin: (request: Request) => Promise<Response>;
	readonly handleLogout: (request: Request) => Promise<Response>;
	readonly authorizeProjection: (request: Request) => AuthenticatedProjectionPrincipal | null;
	readonly authorizeUpgrade: (request: Request) => UpgradeAuthorization | null;
}

interface SessionDependencies {
	readonly now?: () => number;
	readonly random?: () => string;
	readonly maxPreauthProofs?: number;
	readonly maxSessions?: number;
	readonly isTransportAttested: (request: Request) => boolean;
}

function json(status: number, value: unknown, cookies: readonly string[] = []): Response {
	const headers = new Headers({
		'cache-control': 'no-store',
		'content-type': 'application/json; charset=utf-8'
	});
	for (const cookie of cookies) headers.append('set-cookie', cookie);
	return new Response(JSON.stringify(value), { status, headers });
}

function unauthorized(): Response {
	return json(401, { error: { code: 'unauthorized' } });
}

function opaque(): string {
	return randomBytes(32).toString('base64url');
}

function secureTransport(
	request: Request,
	config: SurfaceConfig,
	isTransportAttested: (request: Request) => boolean
): boolean {
	return (
		isTransportAttested(request) &&
		request.headers.get('host') === config.publicHost &&
		request.headers.get('x-forwarded-proto') === 'https'
	);
}

function exactOrigin(request: Request, config: SurfaceConfig): boolean {
	return request.headers.get('origin') === config.publicOrigin;
}

function parseCookie(request: Request, name: string): string | null {
	const header = request.headers.get('cookie');
	if (header === null || header.length > 8192) return null;
	const matches = header
		.split(';')
		.map((part) => part.trim())
		.filter((part) => part.startsWith(`${name}=`))
		.map((part) => part.slice(name.length + 1));
	return matches.length === 1 && /^[A-Za-z0-9_-]{43}$/.test(matches[0]) ? matches[0] : null;
}

function setCookie(name: string, value: string, maxAge: number): string {
	return `${name}=${value}; Path=/; Max-Age=${maxAge}; HttpOnly; Secure; SameSite=Strict`;
}

function clearCookie(name: string): string {
	return `${name}=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict`;
}

function credentialEqual(received: unknown, expected: string): boolean {
	const candidate = typeof received === 'string' && received.length <= 4096 ? received : '';
	const left = createHash('sha256').update(candidate).digest();
	const right = createHash('sha256').update(expected).digest();
	return timingSafeEqual(left, right) && typeof received === 'string';
}

async function loginBody(request: Request): Promise<{ credential: unknown; csrf: unknown } | null> {
	if (request.headers.get('content-type') !== 'application/json') return null;
	const contentLength = request.headers.get('content-length');
	if (
		contentLength !== null &&
		(!/^\d+$/.test(contentLength) || Number(contentLength) > MAX_LOGIN_BODY_BYTES)
	) {
		return null;
	}
	if (request.body === null) return null;
	const reader = request.body.getReader();
	const decoder = new TextDecoder('utf-8', { fatal: true });
	let bytes = 0;
	let text = '';
	try {
		for (;;) {
			const part = await reader.read();
			if (part.done) break;
			bytes += part.value.byteLength;
			if (bytes > MAX_LOGIN_BODY_BYTES) {
				await reader.cancel();
				return null;
			}
			text += decoder.decode(part.value, { stream: true });
		}
		text += decoder.decode();
	} catch {
		try {
			await reader.cancel();
		} catch {
			// The refusal path is identical whether the source accepts cancellation or has already failed.
		}
		return null;
	} finally {
		reader.releaseLock();
	}
	try {
		const parsed = JSON.parse(text) as unknown;
		if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null;
		const record = parsed as Record<string, unknown>;
		if (Object.keys(record).some((key) => !['credential', 'csrf'].includes(key))) return null;
		return { credential: record.credential, csrf: record.csrf };
	} catch {
		return null;
	}
}

export function createSurfaceSessionAuthority(
	config: SurfaceConfig,
	deployment: DeploymentConfig,
	dependencies: SessionDependencies
): SurfaceSessionAuthority {
	if (config.deploymentInstance !== deployment.deploymentInstance) {
		throw new Error('surface and projection configurations do not share a deployment instance');
	}
	const now = dependencies.now ?? Date.now;
	const random = dependencies.random ?? opaque;
	const ttlMs = config.sessionTtlSeconds * 1000;
	const preauthTtlMs = Math.min(PREAUTH_TTL_MS, ttlMs);
	const maxPreauthProofs = dependencies.maxPreauthProofs ?? MAX_PREAUTH_PROOFS;
	const maxSessions = dependencies.maxSessions ?? MAX_SESSIONS;
	if (maxPreauthProofs < 1 || maxSessions < 1) throw new Error('invalid session store capacity');
	const preauth = new Map<string, Proof>();
	const sessions = new Map<string, SessionProof>();

	function sweep(store: Map<string, Proof>): void {
		const currentTime = now();
		for (const [token, proof] of store) {
			if (proof.expiresAt <= currentTime) {
				if (isSessionProof(proof)) proof.controller.abort();
				store.delete(token);
			}
		}
	}

	function live<T extends Proof>(store: Map<string, T>, token: string | null): T | null {
		if (token === null) return null;
		const proof = store.get(token);
		if (proof === undefined) return null;
		if (proof.expiresAt <= now()) {
			if (isSessionProof(proof)) proof.controller.abort();
			store.delete(token);
			return null;
		}
		return proof;
	}

	function liveSession(request: Request): SessionProof | null {
		if (!secureTransport(request, config, dependencies.isTransportAttested)) return null;
		return live(sessions, parseCookie(request, SESSION_COOKIE));
	}

	async function handlePreauth(request: Request): Promise<Response> {
		if (
			request.method !== 'POST' ||
			!secureTransport(request, config, dependencies.isTransportAttested) ||
			!exactOrigin(request, config)
		) {
			return unauthorized();
		}
		sweep(preauth);
		if (preauth.size >= maxPreauthProofs)
			return json(503, { error: { code: 'capacity_exceeded' } });
		const token = random();
		const csrf = random();
		preauth.set(token, Object.freeze({ csrf, expiresAt: now() + preauthTtlMs }));
		return json(200, { csrf }, [setCookie(PREAUTH_COOKIE, token, preauthTtlMs / 1000)]);
	}

	async function handleLogin(request: Request): Promise<Response> {
		if (
			request.method !== 'POST' ||
			!secureTransport(request, config, dependencies.isTransportAttested) ||
			!exactOrigin(request, config)
		) {
			return unauthorized();
		}
		const body = await loginBody(request);
		const preauthToken = parseCookie(request, PREAUTH_COOKIE);
		const proof = live(preauth, preauthToken);
		if (proof !== null) preauth.delete(preauthToken as string);
		if (
			body === null ||
			proof === null ||
			typeof body.csrf !== 'string' ||
			body.csrf !== proof.csrf ||
			!credentialEqual(body.credential, config.creatorCredential)
		) {
			return unauthorized();
		}
		sweep(sessions);
		const previousSession = parseCookie(request, SESSION_COOKIE);
		if (previousSession !== null) {
			sessions.get(previousSession)?.controller.abort();
			sessions.delete(previousSession);
		}
		if (sessions.size >= maxSessions) {
			return json(503, { error: { code: 'capacity_exceeded' } });
		}
		const token = random();
		const csrf = random();
		const expiresAt = now() + ttlMs;
		const controller = new AbortController();
		const lease = Object.freeze({
			identity: Object.freeze({}),
			expiresAt,
			signal: controller.signal
		});
		sessions.set(token, Object.freeze({ csrf, expiresAt, controller, lease }));
		return json(200, { csrf }, [
			setCookie(SESSION_COOKIE, token, config.sessionTtlSeconds),
			clearCookie(PREAUTH_COOKIE)
		]);
	}

	async function handleLogout(request: Request): Promise<Response> {
		if (request.method !== 'POST') return unauthorized();
		const token = parseCookie(request, SESSION_COOKIE);
		const proof = liveSession(request);
		if (
			!exactOrigin(request, config) ||
			proof === null ||
			request.headers.get('x-csrf-token') !== proof.csrf
		) {
			return unauthorized();
		}
		proof.controller.abort();
		sessions.delete(token as string);
		return new Response(null, {
			status: 204,
			headers: { 'cache-control': 'no-store', 'set-cookie': clearCookie(SESSION_COOKIE) }
		});
	}

	function authorizeProjection(request: Request): AuthenticatedProjectionPrincipal | null {
		return liveSession(request) === null ? null : issueProjectionPrincipal(deployment);
	}

	function authorizeUpgrade(request: Request): UpgradeAuthorization | null {
		const url = new URL(request.url);
		const connectionTokens = (request.headers.get('connection') ?? '')
			.split(',')
			.map((token) => token.trim().toLowerCase());
		if (
			request.method !== 'GET' ||
			url.pathname !== '/api/player' ||
			url.search !== '' ||
			!exactOrigin(request, config) ||
			!connectionTokens.includes('upgrade') ||
			request.headers.get('upgrade')?.toLowerCase() !== 'websocket'
		) {
			return null;
		}
		const proof = liveSession(request);
		if (proof === null) return null;
		const protocols = request.headers
			.get('sec-websocket-protocol')
			?.match(/^semmachina\.player\.v1[\t ]*,[\t ]*csrf\.([A-Za-z0-9_-]{43})$/);
		if (protocols?.[1] !== proof.csrf) {
			return null;
		}
		const authorization = Object.create(null) as UpgradeAuthorization;
		Object.defineProperties(authorization, {
			playerId: { value: config.player.id, enumerable: true },
			playerBearer: { value: config.player.bearer, enumerable: false },
			playerWsUrl: { value: config.player.wsUrl, enumerable: true },
			protocol: { value: 'semmachina.player.v1', enumerable: true },
			lease: { value: proof.lease, enumerable: false },
			toJSON: {
				value: () => ({
					playerId: config.player.id,
					playerWsUrl: config.player.wsUrl,
					protocol: 'semmachina.player.v1'
				}),
				enumerable: false
			},
			[inspect.custom]: {
				value: () =>
					`UpgradeAuthorization { playerId: '${config.player.id}', playerWsUrl: '${config.player.wsUrl}', protocol: 'semmachina.player.v1' }`,
				enumerable: false
			}
		});
		return Object.freeze(authorization);
	}

	return Object.freeze({
		handlePreauth,
		handleLogin,
		handleLogout,
		authorizeProjection,
		authorizeUpgrade
	});
}
