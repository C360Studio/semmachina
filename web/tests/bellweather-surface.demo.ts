import { expect, test as base, type Request, type WebSocket } from '@playwright/test';
import { watch } from 'node:fs';
import { access, readFile, unlink } from 'node:fs/promises';
import nodePath from 'node:path';

import { parseSubmitAction } from '../src/lib/player-v1/parser';
import {
	classifyDemoNavigationFailure,
	createSerializedDemoCloseSignalCheck,
	DEMO_PRESENTER_MARKER,
	DemoWorkerAuthorityError,
	requireDemoSecretAbsent,
	requireExactHTTPStatus,
	runDemoWorkerAuthorityFixture
} from './bellweather-surface-contract.mjs';

const origin = 'https://127.0.0.1:4181';
const worldPrefix = process.env.REAL_SURFACE_WORLD_PREFIX as string;

type BootstrapStage =
	| 'started'
	| 'document_loaded'
	| 'login_complete'
	| 'world_ready'
	| 'audit_detached'
	| 'marker_written'
	| 'close_ack_received';

function writeBootstrapDiagnostic(kind: 'progress' | 'failed', stage: BootstrapStage) {
	return new Promise<void>((resolve, reject) =>
		process.stdout.write(`[surface-demo-bootstrap] ${kind}:${stage}\n`, (error) => {
			if (error !== undefined && error !== null) reject(new Error('demo diagnostic write failed'));
			else resolve();
		})
	);
}

type NavigationFailureCategory =
	| 'navigation_timeout'
	| 'connection_refused'
	| 'tls_failure'
	| 'page_closed'
	| 'http_status'
	| 'other';

function writeNavigationFailure(category: NavigationFailureCategory) {
	return new Promise<void>((resolve, reject) =>
		process.stdout.write(`[surface-demo-bootstrap] navigation_failed:${category}\n`, (error) => {
			if (error !== undefined && error !== null) reject(new Error('demo diagnostic write failed'));
			else resolve();
		})
	);
}

function writePreNavigationFailure(
	category: 'credential_invalid' | 'world_scope_invalid' | 'close_signal_invalid'
) {
	return new Promise<void>((resolve, reject) =>
		process.stdout.write(
			`[surface-demo-bootstrap] pre_navigation_failed:${category}\n`,
			(error) => {
				if (error !== undefined && error !== null)
					reject(new Error('demo diagnostic write failed'));
				else resolve();
			}
		)
	);
}

type DemoWorkerAuthority = {
	credential: string;
	closeSignalPath: string | undefined;
};
type DemoTestFixtures = Record<never, never>;
type DemoWorkerFixtures = { demoAuthority: DemoWorkerAuthority };

const test = base.extend<DemoTestFixtures, DemoWorkerFixtures>({
	demoAuthority: [
		async ({ browserName }, use) => {
			void browserName;
			try {
				await runDemoWorkerAuthorityFixture(process.env, use);
			} catch (error) {
				if (error instanceof DemoWorkerAuthorityError) {
					await writePreNavigationFailure(error.category);
				}
				throw error;
			}
		},
		{ scope: 'worker', auto: true }
	]
});

async function consumeCloseSignal(signalPath: string) {
	try {
		await access(signalPath);
	} catch (error) {
		if (error !== null && typeof error === 'object' && 'code' in error && error.code === 'ENOENT') {
			return false;
		}
		throw new Error('demo close signal access failed', { cause: error });
	}
	const content = await readFile(signalPath, 'utf8');
	if (content !== 'close\n') throw new Error('demo close signal invalid');
	await unlink(signalPath);
	return true;
}

async function waitForCloseSignal(signalPath: string) {
	await new Promise<void>((resolve, reject) => {
		const directory = nodePath.dirname(signalPath);
		const filename = nodePath.basename(signalPath);
		let settled = false;
		const finish = (error?: Error) => {
			if (settled) return;
			settled = true;
			watcher.close();
			if (error === undefined) resolve();
			else reject(error);
		};
		const check = createSerializedDemoCloseSignalCheck(() => consumeCloseSignal(signalPath));
		const observe = () => {
			void check()
				.then((consumed) => {
					if (consumed) finish();
				})
				.catch(() => finish(new Error('demo close signal wait failed')));
		};
		const watcher = watch(directory, (event, changed) => {
			if (event !== 'rename' || changed?.toString() !== filename || settled) return;
			observe();
		});
		watcher.once('error', () => finish(new Error('demo close signal watch failed')));
		observe();
	});
}

test('authenticates the headed demo and yields control to the presenter', async ({
	page,
	demoAuthority
}) => {
	test.setTimeout(0);
	let stage: BootstrapStage = 'started';
	await writeBootstrapDiagnostic('progress', stage);
	try {
		if (!/^c360\.semmachina\.[a-z0-9-]+\.bellweather-maze$/.test(worldPrefix)) {
			await writePreNavigationFailure('world_scope_invalid');
			throw new Error('demo world scope is unavailable');
		}

		{
			const requests: Request[] = [];
			const frameListeners = new Map<WebSocket, (event: { payload: string | Buffer }) => void>();
			let submittedActions = 0;
			const requestListener = (request: Request) => requests.push(request);
			const websocketListener = (socket: WebSocket) => {
				const frameListener = ({ payload }: { payload: string | Buffer }) => {
					if (typeof payload !== 'string') return;
					try {
						if (parseSubmitAction(JSON.parse(payload) as unknown).ok) submittedActions += 1;
					} catch {
						// The bootstrap only needs to distinguish submitted actions before handoff.
					}
				};
				frameListeners.set(socket, frameListener);
				socket.on('framesent', frameListener);
			};
			page.on('request', requestListener);
			page.on('websocket', websocketListener);

			const documentResponse = await page.goto(origin).catch(async (error: unknown) => {
				await writeNavigationFailure(classifyDemoNavigationFailure(error));
				throw new Error('demo initial navigation failed');
			});
			if (documentResponse === null || documentResponse.status() !== 200) {
				await writeNavigationFailure('http_status');
				throw new Error('demo initial navigation failed');
			}
			requireExactHTTPStatus(documentResponse, 200, 'demo surface document');
			stage = 'document_loaded';
			await writeBootstrapDiagnostic('progress', stage);
			const preauth = page.waitForResponse((response) =>
				response.url().endsWith('/api/auth/preauth')
			);
			const login = page.waitForResponse((response) => response.url().endsWith('/api/auth/login'));
			const world = page.waitForResponse((response) => response.url().endsWith('/api/world'));
			const input = page.getByLabel('Creator credential');
			await input.fill(demoAuthority.credential);
			await page.getByRole('button', { name: 'Enter world' }).click();
			for (const [label, response] of [
				['preauth', await preauth],
				['login', await login],
				['world', await world]
			] as const) {
				requireExactHTTPStatus(response, 200, `demo ${label}`);
			}
			await expect(input).toHaveCount(0);
			stage = 'login_complete';
			await writeBootstrapDiagnostic('progress', stage);
			await expect(page.getByRole('textbox', { name: 'What do you do?' })).toBeVisible();
			await expect(
				page.locator(`[data-node-id^="${worldPrefix}.location."]`).first()
			).toBeVisible();
			expect(submittedActions, 'demo bootstrap must not submit a player action').toBe(0);
			stage = 'world_ready';
			await writeBootstrapDiagnostic('progress', stage);

			const storage = await page.context().storageState();
			const browserState = await page.evaluate(() => ({
				local: { ...localStorage },
				session: { ...sessionStorage },
				urls: performance.getEntriesByType('resource').map((entry) => entry.name)
			}));
			requireDemoSecretAbsent(demoAuthority.credential, [
				...requests.flatMap((request) => [request.url(), JSON.stringify(request.headers())]),
				JSON.stringify(storage),
				JSON.stringify(browserState),
				await page.content(),
				page.url()
			]);
			page.off('request', requestListener);
			page.off('websocket', websocketListener);
			for (const [socket, listener] of frameListeners) socket.off('framesent', listener);
			frameListeners.clear();
			requests.length = 0;
		}
		demoAuthority.credential = '';
		stage = 'audit_detached';
		await writeBootstrapDiagnostic('progress', stage);
		const autoClose =
			demoAuthority.closeSignalPath === undefined
				? undefined
				: waitForCloseSignal(demoAuthority.closeSignalPath);
		demoAuthority.closeSignalPath = undefined;

		await new Promise<void>((resolve, reject) =>
			process.stdout.write(`${DEMO_PRESENTER_MARKER}\n`, (error) => {
				if (error !== undefined && error !== null)
					reject(new Error('demo readiness signal failed'));
				else resolve();
			})
		);
		stage = 'marker_written';
		if (autoClose !== undefined) {
			await autoClose;
			stage = 'close_ack_received';
			await page.close();
			return;
		}
		await page.waitForEvent('close', { timeout: 0 });
	} catch (error) {
		await writeBootstrapDiagnostic('failed', stage);
		throw error;
	}
});
