import { createPlayerSocket, type PlayerSocketDependencies } from './player-socket';
import {
	createSessionState,
	reduceSession,
	type SessionEffect,
	type SessionEvent,
	type SessionState
} from './session-machine';

export interface SessionController {
	getState(): SessionState;
	subscribe(subscriber: (state: SessionState) => void): () => void;
	dispatch(event: SessionEvent): void;
	destroy(): void;
}

export interface SessionControllerDependencies {
	readonly fetch: typeof fetch;
	readonly origin: string;
	readonly createWebSocket: PlayerSocketDependencies['createWebSocket'];
}

const authenticationRefused = (authenticationGeneration: number): SessionEvent => ({
	type: 'AuthenticationRefused',
	authenticationGeneration,
	refusal: { code: 'authentication_refused', message: 'Authentication refused' }
});

function isAbort(error: unknown): boolean {
	return error instanceof DOMException
		? error.name === 'AbortError'
		: typeof error === 'object' && error !== null && 'name' in error && error.name === 'AbortError';
}

async function exactCsrf(response: Response): Promise<string> {
	if (response.status !== 200) throw new Error('authentication refused');
	const body: unknown = await response.json();
	if (typeof body !== 'object' || body === null || Array.isArray(body)) {
		throw new Error('authentication refused');
	}
	const keys = Object.keys(body);
	const csrf = (body as Record<string, unknown>).csrf;
	if (
		keys.length !== 1 ||
		keys[0] !== 'csrf' ||
		typeof csrf !== 'string' ||
		!/^[A-Za-z0-9_-]{43}$/.test(csrf)
	) {
		throw new Error('authentication refused');
	}
	return csrf;
}

export function createSessionController(
	dependencies: SessionControllerDependencies
): SessionController {
	let state = createSessionState();
	let destroyed = false;
	let draining = false;
	const queuedEvents: SessionEvent[] = [];
	const subscribers = new Set<(state: SessionState) => void>();
	const authentications = new Map<number, AbortController>();
	const base = new URL(dependencies.origin);
	const endpoint = (path: string) => new URL(path, base.origin).toString();

	const dispatch = (event: SessionEvent): void => {
		if (destroyed) return;
		queuedEvents.push(event);
		if (draining) return;
		draining = true;
		try {
			while (!destroyed && queuedEvents.length > 0) {
				const current = queuedEvents.shift() as SessionEvent;
				const transition = reduceSession(state, current);
				state = transition.state;
				for (const subscriber of [...subscribers]) {
					if (destroyed) break;
					try {
						subscriber(state);
					} catch {
						subscribers.delete(subscriber);
					}
				}
				if (destroyed) break;
				for (const effect of transition.effects) execute(effect);
			}
		} finally {
			draining = false;
		}
	};

	const playerSocket = createPlayerSocket({
		origin: dependencies.origin,
		createWebSocket: dependencies.createWebSocket,
		emit: dispatch
	});

	const authenticate = async (
		authenticationGeneration: number,
		credential: string,
		controller: AbortController
	) => {
		const common: RequestInit = {
			method: 'POST',
			credentials: 'same-origin',
			cache: 'no-store',
			redirect: 'error',
			signal: controller.signal
		};
		try {
			const preauth = await exactCsrf(
				await dependencies.fetch(endpoint('/api/auth/preauth'), common)
			);
			const sessionCsrf = await exactCsrf(
				await dependencies.fetch(endpoint('/api/auth/login'), {
					...common,
					headers: { 'content-type': 'application/json' },
					body: JSON.stringify({ credential, csrf: preauth })
				})
			);
			if (destroyed) {
				await logout(sessionCsrf);
				return;
			}
			dispatch({ type: 'Authenticated', authenticationGeneration, sessionCsrf });
		} catch (error) {
			if (!isAbort(error)) dispatch(authenticationRefused(authenticationGeneration));
		} finally {
			if (authentications.get(authenticationGeneration) === controller) {
				authentications.delete(authenticationGeneration);
			}
		}
	};

	const logout = async (sessionCsrf: string) => {
		try {
			await dependencies.fetch(endpoint('/api/auth/logout'), {
				method: 'POST',
				credentials: 'same-origin',
				cache: 'no-store',
				redirect: 'error',
				headers: { 'x-csrf-token': sessionCsrf }
			});
		} catch {
			// Logout is best-effort and intentionally has no result event.
		}
	};

	function execute(effect: SessionEffect): void {
		try {
			switch (effect.type) {
				case 'Authenticate': {
					const controller = new AbortController();
					authentications.set(effect.authenticationGeneration, controller);
					void authenticate(effect.authenticationGeneration, effect.credential, controller);
					return;
				}
				case 'CancelAuthentication':
					authentications.get(effect.authenticationGeneration)?.abort();
					return;
				case 'OpenSocket':
					playerSocket.open(effect.connectionGeneration, effect.sessionCsrf);
					return;
				case 'CloseSocket':
					playerSocket.close(effect.connectionGeneration);
					return;
				case 'Logout':
					void logout(effect.sessionCsrf);
					return;
				case 'SendSubmit':
				case 'SendRetrieveLatest':
				case 'SendRetrieveExact':
					playerSocket.send(effect);
					return;
			}
		} catch {
			// Adapters convert operational failures into events where the protocol has one.
		}
	}

	return {
		getState: () => state,
		subscribe(subscriber) {
			if (destroyed) return () => undefined;
			subscribers.add(subscriber);
			let subscribed = true;
			try {
				subscriber(state);
			} catch {
				subscribers.delete(subscriber);
				subscribed = false;
			}
			return () => {
				if (!subscribed) return;
				subscribed = false;
				subscribers.delete(subscriber);
			};
		},
		dispatch,
		destroy() {
			if (destroyed) return;
			destroyed = true;
			queuedEvents.length = 0;
			for (const controller of authentications.values()) controller.abort();
			authentications.clear();
			playerSocket.destroy();
			subscribers.clear();
		}
	};
}
