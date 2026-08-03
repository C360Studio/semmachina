import { describe, expect, it, vi } from 'vitest';

import type { SessionState } from './session-machine';
import { createSessionController } from './session-controller';
import type { WebSocketLike } from './player-socket';

const response = (status: number, body: unknown) =>
	new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
const PREAUTH_CSRF = 'p'.repeat(43);
const SESSION_CSRF = 's'.repeat(43);

function setup(fetchImpl: typeof fetch) {
	const sockets: WebSocketLike[] = [];
	const controller = createSessionController({
		origin: 'https://play.example.test/path?q=1',
		fetch: fetchImpl,
		createWebSocket: () => {
			const socket = {
				readyState: 0,
				onopen: null,
				onclose: null,
				onerror: null,
				onmessage: null,
				send: vi.fn(),
				close: vi.fn()
			} satisfies WebSocketLike;
			sockets.push(socket);
			return socket;
		}
	});
	return { controller, sockets };
}

describe('session controller', () => {
	it('publishes the reduced authenticating state before exact two-step authentication', async () => {
		const calls: Array<{ input: RequestInfo | URL; init?: RequestInit }> = [];
		let observed: SessionState | undefined;
		const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
			calls.push({ input, init });
			expect(observed?.tag).toBe('authenticating');
			return calls.length === 1
				? response(200, { csrf: PREAUTH_CSRF })
				: response(200, { csrf: SESSION_CSRF });
		}) as unknown as typeof fetch;
		const { controller, sockets } = setup(fetchImpl);
		controller.subscribe((state) => {
			observed = state;
		});
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'creator-secret' });
		await vi.waitFor(() => expect(sockets).toHaveLength(1));
		expect(calls.map(({ input }) => input)).toEqual([
			'https://play.example.test/api/auth/preauth',
			'https://play.example.test/api/auth/login'
		]);
		for (const { init } of calls) {
			expect(init).toMatchObject({
				method: 'POST',
				credentials: 'same-origin',
				cache: 'no-store',
				redirect: 'error'
			});
			expect(init?.signal).toBeInstanceOf(AbortSignal);
		}
		expect(calls[0].init?.headers).toBeUndefined();
		expect(calls[0].init?.body).toBeUndefined();
		expect(calls[1].init?.headers).toEqual({ 'content-type': 'application/json' });
		expect(JSON.parse(calls[1].init?.body as string)).toEqual({
			credential: 'creator-secret',
			csrf: PREAUTH_CSRF
		});
		expect(controller.getState()).toMatchObject({
			tag: 'connecting',
			authenticationGeneration: 1,
			sessionCsrf: SESSION_CSRF
		});
	});

	it.each([
		['status', async () => response(401, { csrf: 'no' })],
		['shape', async () => response(200, { csrf: PREAUTH_CSRF, extra: true })],
		['empty', async () => response(200, { csrf: '' })],
		['short', async () => response(200, { csrf: 'a'.repeat(42) })],
		['long', async () => response(200, { csrf: 'a'.repeat(44) })],
		['whitespace', async () => response(200, { csrf: `${'a'.repeat(42)} ` })],
		['punctuation', async () => response(200, { csrf: `${'a'.repeat(42)}!` })],
		['unicode', async () => response(200, { csrf: `${'a'.repeat(42)}é` })],
		[
			'network',
			async () => {
				throw new Error('offline');
			}
		]
	])(
		'turns non-abort %s failure into an indistinguishable refusal',
		async (_name, implementation) => {
			const { controller } = setup(vi.fn(implementation) as unknown as typeof fetch);
			controller.dispatch({ type: 'AuthenticateRequested', credential: 'bad' });
			await vi.waitFor(() => expect(controller.getState().tag).toBe('signed_out'));
			expect(controller.getState()).toMatchObject({
				authenticationRefusal: { code: 'authentication_refused', message: 'Authentication refused' }
			});
		}
	);

	it('aborts only the matching authentication and still emits a late canceled success when fetch ignores abort', async () => {
		let resolvePreauth!: (value: Response) => void;
		const signalStates: boolean[] = [];
		const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
			signalStates.push(init?.signal?.aborted ?? false);
			if (signalStates.length === 1)
				return new Promise<Response>((resolve) => {
					resolvePreauth = resolve;
				});
			return Promise.resolve(response(200, { csrf: SESSION_CSRF }));
		});
		const fetchImpl = fetchMock as unknown as typeof fetch;
		const { controller } = setup(fetchImpl);
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'secret' });
		controller.dispatch({ type: 'LogoutRequested' });
		resolvePreauth(response(200, { csrf: PREAUTH_CSRF }));
		await vi.waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(3));
		expect((fetchMock.mock.calls[0][1] as RequestInit).signal?.aborted).toBe(true);
		expect(fetchMock.mock.calls[2][0]).toBe('https://play.example.test/api/auth/logout');
	});

	it('executes every logout independently even when the first returns 401', async () => {
		const logoutTokens: string[] = [];
		const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
			if (String(input).endsWith('/preauth')) return response(200, { csrf: PREAUTH_CSRF });
			if (String(input).endsWith('/login')) return response(200, { csrf: SESSION_CSRF });
			logoutTokens.push((init?.headers as Record<string, string>)['x-csrf-token']);
			return response(logoutTokens.length === 1 ? 401 : 204, {});
		});
		const fetchImpl = fetchMock as unknown as typeof fetch;
		const { controller } = setup(fetchImpl);
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'first' });
		await vi.waitFor(() => expect(controller.getState().tag).toBe('connecting'));
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'ignored' });
		controller.dispatch({
			type: 'Authenticated',
			authenticationGeneration: 0,
			sessionCsrf: 'stale'
		});
		await vi.waitFor(() => expect(logoutTokens).toEqual([SESSION_CSRF, 'stale']));
		for (const call of fetchMock.mock.calls.slice(-2)) {
			expect(call[1]).toMatchObject({
				method: 'POST',
				credentials: 'same-origin',
				cache: 'no-store',
				redirect: 'error',
				headers: { 'x-csrf-token': expect.any(String) }
			});
			expect(call[1]?.body).toBeUndefined();
		}
	});

	it('serializes reentrant dispatch and provides immediate, causal, idempotent subscriptions', () => {
		const { controller } = setup(vi.fn() as unknown as typeof fetch);
		const seen: string[] = [];
		const unsubscribe = controller.subscribe((state) => {
			seen.push(state.tag);
			if (state.tag === 'authenticating') controller.dispatch({ type: 'LogoutRequested' });
		});
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'secret' });
		expect(seen).toEqual(['signed_out', 'authenticating', 'signed_out']);
		unsubscribe();
		unsubscribe();
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'again' });
		expect(seen).toHaveLength(3);
	});

	it('stops a subscriber snapshot immediately when an earlier subscriber destroys', () => {
		const { controller } = setup(vi.fn() as unknown as typeof fetch);
		const first: string[] = [];
		const second: string[] = [];
		controller.subscribe((state) => {
			first.push(state.tag);
			if (state.tag === 'authenticating') controller.destroy();
		});
		controller.subscribe((state) => second.push(state.tag));
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'secret' });
		expect(first).toEqual(['signed_out', 'authenticating']);
		expect(second).toEqual(['signed_out']);
	});

	it('contains a throwing initial subscriber and removes its inaccessible registration', () => {
		const { controller } = setup(
			vi.fn(() => new Promise<Response>(() => undefined)) as unknown as typeof fetch
		);
		let failedCalls = 0;
		expect(() =>
			controller.subscribe(() => {
				failedCalls += 1;
				throw new Error('subscriber failed');
			})
		).not.toThrow();
		const healthy: string[] = [];
		controller.subscribe((state) => healthy.push(state.tag));
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'secret' });
		expect(failedCalls).toBe(1);
		expect(healthy).toEqual(['signed_out', 'authenticating']);
	});

	it('destroy aborts auth, closes the socket, clears subscribers, and makes dispatch a no-op', async () => {
		let signal: AbortSignal | undefined;
		const fetchImpl = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
			signal = init?.signal ?? undefined;
			return new Promise<Response>(() => undefined);
		}) as unknown as typeof fetch;
		const { controller, sockets } = setup(fetchImpl);
		let calls = 0;
		controller.subscribe(() => {
			calls += 1;
		});
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'secret' });
		controller.destroy();
		expect(signal?.aborted).toBe(true);
		const before = controller.getState();
		controller.dispatch({
			type: 'AuthenticationRefused',
			authenticationGeneration: 1,
			refusal: { code: 'authentication_refused', message: 'Authentication refused' }
		});
		expect(controller.getState()).toBe(before);
		expect(calls).toBe(2);
		expect(sockets).toHaveLength(0);
	});

	it('closes an established socket on destroy', async () => {
		const fetchImpl = vi
			.fn()
			.mockResolvedValueOnce(response(200, { csrf: PREAUTH_CSRF }))
			.mockResolvedValueOnce(response(200, { csrf: SESSION_CSRF })) as unknown as typeof fetch;
		const { controller, sockets } = setup(fetchImpl);
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'secret' });
		await vi.waitFor(() => expect(sockets).toHaveLength(1));
		controller.destroy();
		expect(sockets[0].close).toHaveBeenCalledOnce();
	});

	it('logs out a session minted after destroy when fetch ignores abort', async () => {
		let resolvePreauth!: (value: Response) => void;
		const logoutTokens: string[] = [];
		const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
			if (String(input).endsWith('/preauth')) {
				return new Promise<Response>((resolve) => {
					resolvePreauth = resolve;
				});
			}
			if (String(input).endsWith('/login'))
				return Promise.resolve(response(200, { csrf: SESSION_CSRF }));
			logoutTokens.push((init?.headers as Record<string, string>)['x-csrf-token']);
			return Promise.resolve(response(401, {}));
		});
		const { controller, sockets } = setup(fetchMock as unknown as typeof fetch);
		const states: string[] = [];
		controller.subscribe((state) => states.push(state.tag));
		controller.dispatch({ type: 'AuthenticateRequested', credential: 'secret' });
		controller.destroy();
		resolvePreauth(response(200, { csrf: PREAUTH_CSRF }));
		await vi.waitFor(() => expect(logoutTokens).toEqual([SESSION_CSRF]));
		expect(fetchMock.mock.calls[2][1]).toMatchObject({
			method: 'POST',
			credentials: 'same-origin',
			cache: 'no-store',
			redirect: 'error',
			headers: { 'x-csrf-token': SESSION_CSRF }
		});
		expect(controller.getState().tag).toBe('authenticating');
		expect(states).toEqual(['signed_out', 'authenticating']);
		expect(sockets).toHaveLength(0);
	});
});
