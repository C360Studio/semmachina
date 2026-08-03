import { request as nodeRequest } from 'node:http';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import WebSocket from 'ws';

const baseURL = 'http://127.0.0.1:4173';
const browserURL = 'https://127.0.0.1:4181';
const publicOrigin = browserURL;
const proxyHeaders = {
	host: '127.0.0.1:4181',
	'x-forwarded-proto': 'https'
};

const fixtureURL = 'http://127.0.0.1:4180';
const credential = 'creator-secret-that-is-long';
const standardAction = 'Ask Wren to inspect the brass gate';
const disconnectAction = 'Disconnect before acceptance';
const actionID = 'act-fixture-7';
const turnID = `turn-${actionID}`;
const playerID = 'c360.semmachina.bellweather.bellweather-maze.player.detective';
const companionID = 'c360.semmachina.bellweather.bellweather-maze.character.wren';
const greenhouseID = 'c360.semmachina.bellweather.bellweather-maze.location.green';
const mazeID = 'c360.semmachina.bellweather.bellweather-maze.location.maze';

interface FixtureState {
	readonly epoch: number;
	readonly pendingTimers: number;
	readonly submissions: ReadonlyArray<Record<string, unknown>>;
	readonly latestRequests: number;
	readonly exactRequests: ReadonlyArray<Record<string, unknown>>;
	readonly acceptedResponses: number;
	readonly intendedDeliveries: number;
	readonly unrelatedDeliveries: number;
	readonly duplicateDeliveries: number;
	readonly disconnectedBeforeAcceptance: number;
	readonly graphqlRequests: ReadonlyArray<{
		readonly query: string;
		readonly variables: Record<string, unknown>;
		readonly authorization: string | null;
	}>;
}

async function resetFixture(request: APIRequestContext): Promise<void> {
	const response = await request.post(`${fixtureURL}/__fixture/reset`);
	expect(response.status()).toBe(204);
}

async function fixtureState(request: APIRequestContext): Promise<FixtureState> {
	const response = await request.get(`${fixtureURL}/__fixture/state`);
	expect(response.status()).toBe(200);
	return (await response.json()) as FixtureState;
}

async function enterWorld(page: Page): Promise<void> {
	await page.goto(browserURL);
	await page.getByLabel('Creator credential').fill(credential);
	const preauth = page.waitForResponse((response) => response.url().endsWith('/api/auth/preauth'));
	const login = page.waitForResponse((response) => response.url().endsWith('/api/auth/login'));
	await page.getByRole('button', { name: 'Enter world' }).click();
	const preauthResponse = await preauth;
	expect(
		preauthResponse.status(),
		JSON.stringify(await preauthResponse.request().allHeaders())
	).toBe(200);
	expect((await login).status()).toBe(200);
	await expect(page.getByRole('textbox', { name: 'What do you do?' })).toBeVisible();
}

function auditBrowserBoundary(page: Page): () => void {
	const requests: Array<{ url: string; authorization?: string }> = [];
	const sockets: string[] = [];
	page.on('request', (request) => {
		requests.push({ url: request.url(), authorization: request.headers()['authorization'] });
	});
	page.on('websocket', (socket) => sockets.push(socket.url()));

	return () => {
		const browserOrigin = new URL(browserURL).origin;
		for (const request of requests) {
			const url = new URL(request.url);
			expect(url.origin).toBe(browserOrigin);
			expect(request.authorization).toBeUndefined();
			expect(Object.values(request).join(' ')).not.toMatch(/Bearer|graphql|nats/i);
			if (url.pathname.startsWith('/api/')) {
				expect([
					'/api/auth/preauth',
					'/api/auth/login',
					'/api/auth/logout',
					'/api/player',
					'/api/world'
				]).toContain(url.pathname);
			}
		}
		expect(sockets.length).toBeGreaterThan(0);
		expect(new Set(sockets)).toEqual(
			new Set([`${browserURL.replace('https:', 'wss:')}/api/player`])
		);
	};
}

test('custom entry shares its installed default-deny runtime with the lazy world route', async ({
	request
}) => {
	const response = await request.get(`${baseURL}/api/world`, { headers: proxyHeaders });
	expect(response.status()).toBe(401);
	expect(await response.json()).toEqual({ error: { code: 'unauthorized' } });
});

test('custom entry explicitly refuses WebSocket upgrades', async () => {
	const status = await new Promise<number>((resolve, reject) => {
		const request = nodeRequest(`${baseURL}/api/player`, {
			headers: {
				...proxyHeaders,
				origin: publicOrigin,
				connection: 'Upgrade',
				upgrade: 'websocket'
			}
		});
		request.once('response', (response) => resolve(response.statusCode ?? 0));
		request.once('upgrade', () => reject(new Error('unexpected upgrade acceptance')));
		request.once('error', reject);
		request.end();
	});
	expect(status).toBe(426);
});

function cookie(header: string, name: string): string {
	const match = header.match(new RegExp(`${name}=[A-Za-z0-9_-]+`));
	if (match === null) throw new Error(`missing ${name}`);
	return match[0];
}

test('production bridge confines upstream headers and relays fragmented text bytes unchanged', async ({
	request
}) => {
	const authHeaders = { ...proxyHeaders, origin: publicOrigin };
	const preauth = await request.post(`${baseURL}/api/auth/preauth`, { headers: authHeaders });
	expect(preauth.status()).toBe(200);
	const preauthCsrf = ((await preauth.json()) as { csrf: string }).csrf;
	const preauthCookie = cookie(preauth.headers()['set-cookie'] ?? '', '__Host-semmachina_preauth');
	const login = await request.post(`${baseURL}/api/auth/login`, {
		headers: { ...authHeaders, cookie: preauthCookie },
		data: { credential: 'creator-secret-that-is-long', csrf: preauthCsrf }
	});
	expect(login.status()).toBe(200);
	const sessionCsrf = ((await login.json()) as { csrf: string }).csrf;
	const sessionCookie = cookie(login.headers()['set-cookie'] ?? '', '__Host-semmachina_session');

	const received = await new Promise<Buffer>((resolve, reject) => {
		const websocket = new WebSocket(
			`${baseURL.replace('http:', 'ws:')}/api/player`,
			['semmachina.player.v1', `csrf.${sessionCsrf}`],
			{
				headers: { ...authHeaders, cookie: sessionCookie },
				perMessageDeflate: false
			}
		);
		const expected = Buffer.from('Midsomer fragmented \u{1f50d} exact bytes', 'utf8');
		websocket.once('error', reject);
		websocket.once('unexpected-response', (_request, response) =>
			reject(new Error(`unexpected relay response ${response.statusCode ?? 0}`))
		);
		websocket.once('close', (code) => {
			if (code !== 1000) reject(new Error(`relay closed before echo with code ${code}`));
		});
		websocket.once('open', () => {
			try {
				expect(websocket.protocol).toBe('semmachina.player.v1');
				expect(websocket.extensions).toBe('');
				websocket.send(expected.subarray(0, 21), { binary: false, fin: false });
				websocket.send(expected.subarray(21), { binary: false, fin: true });
			} catch (error) {
				reject(error);
			}
		});
		websocket.once('message', (data, binary) => {
			try {
				expect(binary).toBe(false);
				const bytes = Buffer.isBuffer(data) ? data : Buffer.from(data as ArrayBuffer);
				expect(bytes).toEqual(expected);
				websocket.close(1000);
				resolve(bytes);
			} catch (error) {
				reject(error);
			}
		});
	});
	expect(received.byteLength).toBeGreaterThan(0);
});

test('fixture reset terminates clients and cancels delayed callbacks from the prior epoch', async ({
	request
}) => {
	await resetFixture(request);
	const initial = await fixtureState(request);
	const websocket = new WebSocket(`${fixtureURL.replace('http:', 'ws:')}/play`, {
		headers: { authorization: 'Bearer player-bearer-that-is-distinct' },
		perMessageDeflate: false
	});
	let closeCode: number | undefined;
	websocket.once('close', (code) => {
		closeCode = code;
	});
	try {
		await new Promise<void>((resolve, reject) => {
			websocket.once('open', resolve);
			websocket.once('error', reject);
		});
		websocket.send(
			JSON.stringify({
				protocol: 'player/v1',
				text: 'Schedule a response in the old fixture epoch',
				idempotency_key: 'fixture-reset-regression'
			})
		);
		await expect.poll(async () => (await fixtureState(request)).submissions.length).toBe(1);

		await resetFixture(request);
		await expect.poll(() => closeCode, { timeout: 1_000 }).toBe(1006);
		const reset = await fixtureState(request);
		expect(reset.epoch).toBe(initial.epoch + 1);
		expect(reset.pendingTimers).toBe(0);
		expect(reset.submissions).toEqual([]);
		expect(reset.acceptedResponses).toBe(0);
	} finally {
		websocket.terminate();
	}
});

test.describe.serial('Group 7 deterministic creator acceptance', () => {
	test('7.1 completes one strict action through typed progress and a companion-aware terminal', async ({
		page,
		request
	}) => {
		await resetFixture(request);
		const assertBrowserBoundary = auditBrowserBoundary(page);
		await enterWorld(page);

		await expect(page.getByText('Schematic mode — some positions are inferred.')).toBeVisible();
		await expect(page.locator(`[data-node-id="${greenhouseID}"]`)).toHaveAttribute(
			'data-position-kind',
			'authored'
		);
		await expect(page.locator(`[data-node-id="${mazeID}"]`)).toHaveAttribute(
			'data-position-kind',
			'schematic'
		);
		await expect(page.getByText('Greenhouse to Bellweather Maze', { exact: true })).toBeVisible();
		await expect(page.getByText('Clock not configured.', { exact: true })).toBeVisible();

		const status = page.getByRole('region', { name: 'Action session' }).getByRole('status');
		await page.getByRole('textbox', { name: 'What do you do?' }).fill(standardAction);
		await page.getByRole('button', { name: 'Submit' }).click();
		await expect(status).toHaveText('Checking prior activity.');
		await expect(status).toHaveText('Submitting action.');
		await expect(status).toHaveText('Waiting for resolution.');
		await expect(status).toHaveText('Resolution received.');

		const terminal = page.getByRole('article', { name: `Resolution for turn ${turnID}` });
		await expect(terminal).toHaveCount(1);
		await expect(terminal).toContainText(
			'Wren points out fresh tool marks beneath the brass latch.'
		);
		await expect(terminal).toContainText('hint (hint level: connect)');
		await expect(terminal).toContainText(`Companion ${companionID}`);
		await expect(terminal).toContainText(actionID);
		await expect(terminal).toContainText(playerID);

		const state = await fixtureState(request);
		expect(state.submissions).toHaveLength(1);
		expect(state.submissions[0]).toEqual({
			protocol: 'player/v1',
			text: standardAction,
			idempotency_key: expect.stringMatching(/^[A-Za-z0-9_-]+$/)
		});
		expect(state.latestRequests).toBe(1);
		expect(state.acceptedResponses).toBe(1);
		expect(state.intendedDeliveries).toBe(1);
		expect(state.graphqlRequests).toHaveLength(3);
		expect(state.graphqlRequests.every(({ authorization }) => authorization === null)).toBe(true);
		assertBrowserBoundary();
	});

	test('7.2 requires an explicit byte-exact replay after disconnect-before-acceptance', async ({
		page,
		request
	}) => {
		await resetFixture(request);
		const assertBrowserBoundary = auditBrowserBoundary(page);
		await enterWorld(page);

		const status = page.getByRole('region', { name: 'Action session' }).getByRole('status');
		await page.getByRole('textbox', { name: 'What do you do?' }).fill(disconnectAction);
		await page.getByRole('button', { name: 'Submit' }).click();
		await expect(status).toHaveText('Checking prior activity.');
		await expect(status).toHaveText('Submitting action.');
		await expect(status).toHaveText('Connection interrupted.');

		const afterDisconnect = await fixtureState(request);
		expect(afterDisconnect.submissions).toHaveLength(1);
		expect(afterDisconnect.disconnectedBeforeAcceptance).toBe(1);

		await page.getByRole('button', { name: 'Reconnect' }).click();
		await expect(status).toHaveText('Checking player activity without assuming correlation.');
		const authorizeReplay = page.getByRole('button', { name: 'Authorize exact replay' });
		await expect(authorizeReplay).toBeVisible();
		await expect(
			page.getByText(
				'Authorize replay only if you want to continue: an accepted original converges idempotently to the same result; otherwise this may submit the action now.'
			)
		).toBeVisible();
		await expect(page.getByRole('article', { name: /Resolution for turn/ })).toHaveCount(0);

		const beforeAuthorization = await fixtureState(request);
		expect(beforeAuthorization.latestRequests).toBe(2);
		expect(beforeAuthorization.unrelatedDeliveries).toBe(1);
		expect(beforeAuthorization.submissions).toHaveLength(1);

		await authorizeReplay.click();
		await expect(status).toHaveText('Submitting action.');
		await expect(status).toHaveText('Resolution received.');
		const terminal = page.getByRole('article', { name: `Resolution for turn ${turnID}` });
		await expect(terminal).toHaveCount(1);
		await expect(terminal).toContainText('The replay resolves the intended brass-latch action.');
		await expect(page.getByRole('article', { name: /Resolution for turn/ })).toHaveCount(1);

		const recovered = await fixtureState(request);
		expect(recovered.submissions).toHaveLength(2);
		expect(recovered.submissions[1]).toEqual(recovered.submissions[0]);
		expect(recovered.submissions[1]).toEqual({
			protocol: 'player/v1',
			text: disconnectAction,
			idempotency_key: expect.stringMatching(/^[A-Za-z0-9_-]+$/)
		});
		expect(recovered.exactRequests).toEqual([
			{ protocol: 'player/v1', type: 'retrieve_result', by: 'turn', id: turnID }
		]);
		expect(recovered.acceptedResponses).toBe(1);
		expect(recovered.intendedDeliveries).toBe(1);
		expect(recovered.duplicateDeliveries).toBeGreaterThanOrEqual(1);
		assertBrowserBoundary();
	});
});
