import { expect, test, type Page, type Request } from '@playwright/test';

import { parseRetrieveRequest, parseSubmitAction } from '../src/lib/player-v1/parser';
import {
	browserOrigin,
	completeActionFreeJourney,
	creatorCredential
} from './bellweather-action-free-journey';
import { runLatestRetrievalProbe } from './bellweather-retrieval-probe';
import { emitSurfaceCheckpoint } from './bellweather-surface-contract.mjs';

const worldPrefix = process.env.REAL_SURFACE_WORLD_PREFIX as string;

async function assertBrowserAuthorityBoundary(
	page: Page,
	requests: readonly Request[],
	websocketURLs: readonly string[],
	requireCredentialInputGone: boolean
): Promise<void> {
	try {
		for (const request of requests) {
			const url = new URL(request.url());
			expect(url.origin === browserOrigin, 'browser request escaped the public surface').toBe(true);
			const headers = request.headers();
			expect('authorization' in headers, 'browser request carried Authorization').toBe(false);
			expect(
				request.url().includes(creatorCredential),
				'browser request URL exposed the creator credential'
			).toBe(false);
			expect(
				JSON.stringify(headers).includes(creatorCredential),
				'browser request headers exposed the creator credential'
			).toBe(false);
		}
		expect(websocketURLs.length > 0, 'browser opened no player WebSocket').toBe(true);
		for (const rawURL of websocketURLs) {
			const url = new URL(rawURL);
			expect(
				url.origin === 'wss://127.0.0.1:4181',
				'browser WebSocket escaped the public WSS surface'
			).toBe(true);
			expect(url.pathname === '/api/player', 'browser WebSocket used an unexpected path').toBe(
				true
			);
			expect(url.search === '', 'browser WebSocket carried a query string').toBe(true);
			expect(
				url.username === '' && url.password === '',
				'browser WebSocket carried URL credentials'
			).toBe(true);
			expect(
				rawURL.includes(creatorCredential),
				'browser WebSocket URL exposed the creator credential'
			).toBe(false);
		}
		const storage = await page.context().storageState();
		const browserState = await page.evaluate(() => ({
			local: { ...localStorage },
			session: { ...sessionStorage },
			urls: performance.getEntriesByType('resource').map((entry) => entry.name)
		}));
		expect(
			JSON.stringify(storage).includes(creatorCredential),
			'browser storage state exposed the creator credential'
		).toBe(false);
		expect(
			JSON.stringify(browserState.local).includes(creatorCredential),
			'localStorage exposed the creator credential'
		).toBe(false);
		expect(
			JSON.stringify(browserState.session).includes(creatorCredential),
			'sessionStorage exposed the creator credential'
		).toBe(false);
		expect(
			browserState.urls.some((url) => url.includes(creatorCredential)),
			'resource URLs exposed the creator credential'
		).toBe(false);
		expect(
			(await page.content()).includes(creatorCredential),
			'DOM exposed the creator credential'
		).toBe(false);
		expect(
			page.url().includes(creatorCredential),
			'current URL exposed the creator credential'
		).toBe(false);
		if (requireCredentialInputGone) {
			await expect(page.getByLabel('Creator credential')).toHaveCount(0);
		}
	} catch (error) {
		throw new Error('action-free preflight failed at browser authority boundary', { cause: error });
	}
}

test('action-free real stack reaches login, map, and unconfigured clock', async ({
	browser,
	page
}) => {
	let submittedActions = 0;
	const frameFailures: string[] = [];
	const requests: Request[] = [];
	const websocketURLs: string[] = [];
	page.on('request', (request) => requests.push(request));
	page.on('websocket', (socket) => {
		websocketURLs.push(socket.url());
		socket.on('framesent', ({ payload }) => {
			if (typeof payload !== 'string') {
				frameFailures.push('binary outbound player frame');
				return;
			}
			let document: unknown;
			try {
				document = JSON.parse(payload) as unknown;
			} catch {
				frameFailures.push('malformed outbound player JSON');
				return;
			}
			const action = parseSubmitAction(document);
			if (action.ok) {
				submittedActions += 1;
				return;
			}
			if (!parseRetrieveRequest(document).ok)
				frameFailures.push('unsupported outbound player frame');
		});
	});

	await completeActionFreeJourney(page, worldPrefix);
	await assertBrowserAuthorityBoundary(page, requests, websocketURLs, true);
	await runLatestRetrievalProbe(browser, creatorCredential);
	await assertBrowserAuthorityBoundary(page, requests, websocketURLs, false);

	expect(frameFailures).toEqual([]);
	expect(submittedActions, 'preflight must never submit a player action').toBe(0);
	if (process.env.REAL_SURFACE_INTERRUPT_PROOF === '1') {
		emitSurfaceCheckpoint('interrupt_cleanup_ready');
		await new Promise<never>(() => undefined);
	}
});
