import { expect, test, type Page, type Request, type WebSocket } from '@playwright/test';

import {
	parsePlayerFrame,
	parseRetrieveRequest,
	parseSubmitAction,
	type SubmitAction
} from '../src/lib/player-v1/parser';
import {
	assertTerminalAfterAuthoritativeCompletion,
	emitSurfaceCheckpoint,
	monitorTurn,
	requireSafeBrowserAudit,
	requireSafeProtocolPoll,
	requireSecretAbsent
} from './bellweather-surface-contract.mjs';
import { browserOrigin, completeActionFreeJourney } from './bellweather-action-free-journey';

const playerBearer = 'CHANGE-ME-bellweather-local-only-bearer';
const diagnosticsURL = process.env.REAL_SURFACE_DIAGNOSTICS_URL as string;
const worldPrefix = process.env.REAL_SURFACE_WORLD_PREFIX as string;
const observationAction =
	"I observe Harold Wren's body carefully, looking at the body itself before investigating further.";
const hintAction = 'I explicitly ask Kit Finch for a hint about what we observed.';

interface AcceptedTurn {
	readonly action_id: string;
	readonly turn_id: string;
	readonly idempotency_key: string;
}

type ProtocolFailureCategory =
	| 'outbound-binary'
	| 'outbound-malformed'
	| 'outbound-noncanonical'
	| 'inbound-binary'
	| 'inbound-invalid'
	| 'submission-refused'
	| 'missing-pending'
	| 'idempotency-mismatch'
	| 'identity-mismatch';

function browserAudit(page: Page) {
	const requests: Request[] = [];
	const sockets: WebSocket[] = [];
	const accepted: AcceptedTurn[] = [];
	const sentActions: SubmitAction[] = [];
	const protocolFailures: ProtocolFailureCategory[] = [];
	page.on('request', (request) => requests.push(request));
	page.on('websocket', (socket) => {
		sockets.push(socket);
		socket.on('framesent', ({ payload }) => {
			if (typeof payload !== 'string') {
				protocolFailures.push('outbound-binary');
				return;
			}
			let document: unknown;
			try {
				document = JSON.parse(payload) as unknown;
			} catch {
				protocolFailures.push('outbound-malformed');
				return;
			}
			const parsed = parseSubmitAction(document);
			if (parsed.ok) {
				sentActions.push(parsed.value);
				return;
			}
			const retrieval = parseRetrieveRequest(document);
			if (!retrieval.ok) {
				protocolFailures.push('outbound-noncanonical');
			}
		});
		socket.on('framereceived', ({ payload }) => {
			if (typeof payload !== 'string') {
				protocolFailures.push('inbound-binary');
				return;
			}
			const parsed = parsePlayerFrame(payload);
			if (!parsed.ok) {
				protocolFailures.push('inbound-invalid');
				return;
			}
			if (parsed.value.type !== 'submit_response') return;
			const response = parsed.value.response;
			if (response.status !== 'accepted') {
				protocolFailures.push('submission-refused');
				return;
			}
			const pending = sentActions[accepted.length];
			if (pending === undefined) {
				protocolFailures.push('missing-pending');
				return;
			}
			if (response.idempotency_key !== pending.idempotency_key) {
				protocolFailures.push('idempotency-mismatch');
				return;
			}
			if (response.turn_id !== `turn-${response.action_id}`) {
				protocolFailures.push('identity-mismatch');
				return;
			}
			accepted.push({
				turn_id: response.turn_id,
				action_id: response.action_id,
				idempotency_key: response.idempotency_key
			});
		});
	});
	return {
		accepted,
		sentActions,
		protocolFailures,
		assert: async () => {
			for (const request of requests) {
				const url = new URL(request.url());
				requireSafeBrowserAudit(url.origin === browserOrigin, 'request_authority');
				requireSafeBrowserAudit(!/graphql|nats/i.test(url.pathname), 'request_authority');
				const headers = request.headers();
				requireSafeBrowserAudit(headers.authorization === undefined, 'request_authority');
				requireSafeBrowserAudit(
					!/Bearer|graphql|nats/i.test(JSON.stringify({ url: request.url(), headers })),
					'request_authority'
				);
			}
			requireSafeBrowserAudit(sockets.length > 0, 'websocket_authority');
			for (const socket of sockets) {
				const url = new URL(socket.url());
				requireSafeBrowserAudit(url.origin === 'wss://127.0.0.1:4181', 'websocket_authority');
				requireSafeBrowserAudit(url.pathname === '/api/player', 'websocket_authority');
				requireSafeBrowserAudit(url.search === '', 'websocket_authority');
			}
			requireSafeBrowserAudit(protocolFailures.length === 0, 'protocol');
			const storage = await page.context().storageState();
			const browserState = await page.evaluate(() => ({
				local: { ...localStorage },
				session: { ...sessionStorage },
				urls: performance.getEntriesByType('resource').map((entry) => entry.name)
			}));
			requireSecretAbsent(
				playerBearer,
				[JSON.stringify(storage), JSON.stringify(browserState), await page.content(), page.url()],
				'browser_state'
			);
		}
	};
}

async function submitAndMonitor(
	page: Page,
	audit: ReturnType<typeof browserAudit>,
	action: string,
	acceptedIndex: number,
	checkpointLabels: {
		readonly submitStarted: 'first_action_submit_started' | 'second_action_submit_started';
		readonly accepted: 'first_action_accepted' | 'second_action_accepted';
		readonly complete: 'first_turn_complete' | 'second_turn_complete';
	}
) {
	await page.getByRole('textbox', { name: 'What do you do?' }).fill(action);
	emitSurfaceCheckpoint(checkpointLabels.submitStarted);
	await page.getByRole('button', { name: 'Submit' }).click();
	await expect
		.poll(() => requireSafeProtocolPoll(audit.protocolFailures, audit.accepted.length))
		.toBe(acceptedIndex + 1);
	emitSurfaceCheckpoint(checkpointLabels.accepted);
	const accepted = audit.accepted[acceptedIndex];
	const sent = audit.sentActions[acceptedIndex];
	expect(sent.text).toBe(action);
	expect(accepted.idempotency_key).toBe(sent.idempotency_key);
	const terminal = page.getByRole('article', { name: `Resolution for turn ${accepted.turn_id}` });
	const diagnostic = await assertTerminalAfterAuthoritativeCompletion(
		monitorTurn(diagnosticsURL, accepted.turn_id),
		async () => {
			await expect(terminal).toBeVisible();
			await expect(terminal).toHaveCount(1);
		}
	);
	emitSurfaceCheckpoint(checkpointLabels.complete);
	return { accepted, terminal, diagnostic };
}

test.describe.serial('OpenSpec 7.3 real Bellweather surface acceptance', () => {
	test('proves the paid two-turn journey through only the browser surface', async ({ page }) => {
		const audit = browserAudit(page);
		await completeActionFreeJourney(page, worldPrefix);

		const first = await submitAndMonitor(page, audit, observationAction, 0, {
			submitStarted: 'first_action_submit_started',
			accepted: 'first_action_accepted',
			complete: 'first_turn_complete'
		});
		expect(first.diagnostic.case_phase).toBe('discovery');
		await expect(page.getByRole('article', { name: /Resolution for turn/ })).toHaveCount(1);
		await page.getByRole('button', { name: 'Continue' }).click();
		await expect(page.getByRole('article', { name: /Resolution for turn/ })).toHaveCount(0);

		const second = await submitAndMonitor(page, audit, hintAction, 1, {
			submitStarted: 'second_action_submit_started',
			accepted: 'second_action_accepted',
			complete: 'second_turn_complete'
		});
		await expect(second.terminal).toContainText(`${worldPrefix}.character.kit-finch`);
		await expect(second.terminal).toContainText(/Kit/i);
		expect(second.diagnostic.kit_hint_proof).toMatchObject({ proved: true });
		await expect(page.getByRole('article', { name: /Resolution for turn/ })).toHaveCount(1);

		expect(first.accepted.turn_id).not.toBe(second.accepted.turn_id);
		await audit.assert();
	});
});
