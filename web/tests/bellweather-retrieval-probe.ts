import type { Browser } from '@playwright/test';

import {
	parsePlayerFrame,
	parseRetrieveRequest,
	parseSubmitAction,
	type RetrieveRequest,
	type RetrieveResponse
} from '../src/lib/player-v1/parser';
import { emitSurfaceCheckpoint, requireExactHTTPStatus } from './bellweather-surface-contract.mjs';

const browserOrigin = 'https://127.0.0.1:4181';
const playerWebSocketURL = 'wss://127.0.0.1:4181/api/player';

export function parseExactCsrfEnvelope(value: unknown): string {
	if (value === null || typeof value !== 'object' || Array.isArray(value)) {
		throw new Error('retrieval probe authentication response has an invalid shape');
	}
	const keys = Object.keys(value);
	const csrf = (value as Record<string, unknown>).csrf;
	if (
		keys.length !== 1 ||
		keys[0] !== 'csrf' ||
		typeof csrf !== 'string' ||
		!/^[A-Za-z0-9_-]{43}$/.test(csrf)
	) {
		throw new Error('retrieval probe authentication response has an invalid shape');
	}
	return csrf;
}

export function canonicalLatestRetrieveRequest(): RetrieveRequest {
	const parsed = parseRetrieveRequest({
		protocol: 'player/v1',
		type: 'retrieve_result',
		by: 'latest'
	});
	if (!parsed.ok) throw new Error('canonical latest retrieval request is invalid');
	return parsed.value;
}

export function validateRetrievalProbeOutbound(payload: string): 'latest_retrieval' {
	let document: unknown;
	try {
		document = JSON.parse(payload) as unknown;
	} catch {
		throw new Error('retrieval probe emitted invalid JSON');
	}
	if (parseSubmitAction(document).ok) {
		throw new Error('retrieval probe emitted forbidden submit frame');
	}
	const retrieval = parseRetrieveRequest(document);
	if (!retrieval.ok || retrieval.value.by !== 'latest' || 'id' in retrieval.value) {
		throw new Error('retrieval probe emitted a noncanonical frame');
	}
	return 'latest_retrieval';
}

export function parseLatestRetrieveResponse(raw: string): RetrieveResponse {
	const parsed = parsePlayerFrame(raw);
	if (
		!parsed.ok ||
		parsed.value.type !== 'retrieve_response' ||
		parsed.value.retrieval.by !== 'latest' ||
		parsed.value.retrieval.id !== undefined
	) {
		throw new Error('retrieval probe response is not a latest retrieval response');
	}
	return parsed.value.retrieval;
}

export function requireEmptyLatestRetrieveResponse(response: RetrieveResponse): void {
	if (
		response.by !== 'latest' ||
		response.id !== undefined ||
		response.status !== 'refused' ||
		response.refusal.code !== 'not_found' ||
		'delivery' in response
	) {
		throw new Error('retrieval probe did not prove empty latest history');
	}
}

interface AuthenticationResult {
	readonly status: number;
	readonly body: unknown;
}

async function authenticationRequest(
	page: import('@playwright/test').Page,
	path: '/api/auth/preauth' | '/api/auth/login',
	body?: { readonly credential: string; readonly csrf: string }
): Promise<AuthenticationResult> {
	return page.evaluate(
		async ({ path, body }) => {
			const response = await fetch(path, {
				method: 'POST',
				credentials: 'same-origin',
				cache: 'no-store',
				redirect: 'error',
				...(body === undefined
					? {}
					: { headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) })
			});
			let responseBody: unknown = null;
			try {
				responseBody = (await response.json()) as unknown;
			} catch {
				// The closed validator reports the bounded failure outside the page.
			}
			return { status: response.status, body: responseBody };
		},
		{ path, body }
	);
}

function requireAuthentication(result: AuthenticationResult, label: string): string {
	if (result.status !== 200) throw new Error(`retrieval probe ${label} returned unexpected status`);
	return parseExactCsrfEnvelope(result.body);
}

export async function runLatestRetrievalProbe(
	browser: Browser,
	creatorCredential: string
): Promise<RetrieveResponse> {
	const context = await browser.newContext({ ignoreHTTPSErrors: true });
	const requests: Array<{ url: string; headers: Record<string, string> }> = [];
	const websocketURLs: string[] = [];
	const outboundFailures: string[] = [];
	let retrievalsSent = 0;
	try {
		const page = await context.newPage();
		page.on('request', (request) =>
			requests.push({ url: request.url(), headers: request.headers() })
		);
		page.on('websocket', (socket) => {
			websocketURLs.push(socket.url());
			socket.on('framesent', ({ payload }) => {
				if (typeof payload !== 'string') {
					outboundFailures.push('binary outbound frame');
					return;
				}
				try {
					validateRetrievalProbeOutbound(payload);
					retrievalsSent += 1;
					emitSurfaceCheckpoint('retrieval_sent');
				} catch (error) {
					outboundFailures.push(
						error instanceof Error ? error.message : 'retrieval probe outbound validation failed'
					);
				}
			});
		});

		const documentResponse = await page.goto(browserOrigin);
		requireExactHTTPStatus(documentResponse, 200, 'retrieval probe surface document');
		const preauthCsrf = requireAuthentication(
			await authenticationRequest(page, '/api/auth/preauth'),
			'preauth'
		);
		const sessionCsrf = requireAuthentication(
			await authenticationRequest(page, '/api/auth/login', {
				credential: creatorCredential,
				csrf: preauthCsrf
			}),
			'login'
		);
		const request = JSON.stringify(canonicalLatestRetrieveRequest());
		const raw = await page.evaluate(
			({ playerWebSocketURL, sessionCsrf, request }) =>
				new Promise<string>((resolve, reject) => {
					let answered = false;
					const socket = new WebSocket(playerWebSocketURL, [
						'semmachina.player.v1',
						`csrf.${sessionCsrf}`
					]);
					const timer = window.setTimeout(() => {
						socket.close();
						reject(new Error('retrieval probe timed out awaiting a response'));
					}, 10_000);
					socket.onopen = () => {
						if (socket.protocol !== 'semmachina.player.v1') {
							window.clearTimeout(timer);
							socket.close();
							reject(new Error('retrieval probe negotiated an unexpected protocol'));
							return;
						}
						socket.send(request);
					};
					socket.onmessage = (event) => {
						window.clearTimeout(timer);
						if (typeof event.data !== 'string') {
							socket.close();
							reject(new Error('retrieval probe received a binary response'));
							return;
						}
						answered = true;
						socket.close(1000);
						resolve(event.data);
					};
					socket.onerror = () => {
						window.clearTimeout(timer);
						reject(new Error('retrieval probe WebSocket failed'));
					};
					socket.onclose = () => {
						if (!answered) {
							window.clearTimeout(timer);
							reject(new Error('retrieval probe WebSocket closed before a response'));
						}
					};
				}),
			{ playerWebSocketURL, sessionCsrf, request }
		);
		const response = parseLatestRetrieveResponse(raw);
		if (retrievalsSent !== 1 || outboundFailures.length !== 0) {
			throw new Error('retrieval probe violated its action-free outbound contract');
		}
		requireEmptyLatestRetrieveResponse(response);
		emitSurfaceCheckpoint('retrieval_answered');

		for (const requestRecord of requests) {
			const url = new URL(requestRecord.url);
			if (
				url.origin !== browserOrigin ||
				'authorization' in requestRecord.headers ||
				requestRecord.url.includes(creatorCredential) ||
				JSON.stringify(requestRecord.headers).includes(creatorCredential)
			) {
				throw new Error('retrieval probe violated the browser credential boundary');
			}
		}
		if (
			websocketURLs.length !== 1 ||
			websocketURLs[0] !== playerWebSocketURL ||
			websocketURLs[0].includes(creatorCredential)
		) {
			throw new Error('retrieval probe violated the WebSocket authority boundary');
		}
		const storage = await context.storageState();
		const browserState = await page.evaluate(() => ({
			local: { ...localStorage },
			session: { ...sessionStorage },
			urls: performance.getEntriesByType('resource').map((entry) => entry.name),
			dom: document.documentElement.outerHTML,
			url: location.href
		}));
		if (
			JSON.stringify(storage).includes(creatorCredential) ||
			JSON.stringify(browserState).includes(creatorCredential)
		) {
			throw new Error('retrieval probe exposed the creator credential in browser state');
		}
		return response;
	} finally {
		await context.close();
	}
}
