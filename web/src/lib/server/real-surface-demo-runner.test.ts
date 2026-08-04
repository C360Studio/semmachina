import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';

import {
	DEMO_BOOTSTRAP_STAGES,
	DEMO_PRESENTER_MARKER,
	DEMO_STARTUP_LIMIT_MS,
	buildDemoBrowserEnvironment,
	buildDemoWebServerEnvironment,
	classifyDemoNavigationFailure,
	cleanupDemoCloseSignal,
	createDemoCreatorCredential,
	createDemoCloseSignal,
	createSerializedDemoCloseSignalCheck,
	createDemoStartupDeadline,
	emitDemoReady,
	holdDemoPresentation,
	parseStackManifest,
	parseDemoBootstrapDiagnostic,
	parseSurfaceRunnerArguments,
	publishDemoCloseSignal,
	requireDemoSecretAbsent,
	sanitizeBrowserEnvironment,
	runDemoWorkerAuthorityFixture,
	withinDemoStartupDeadline
} from '../../../tests/bellweather-surface-contract.mjs';
import {
	cleanupFailedDemoProof,
	parseDemoRunnerDiagnostic,
	parseDemoSignalProofArguments,
	requireDemoSignalProof,
	signalDemoProofRunner
} from '../../../tests/prove-bellweather-surface-demo-interrupt.mjs';

const manifest = parseStackManifest(
	JSON.stringify({
		status: 'ready',
		player_websocket_url: 'ws://127.0.0.1:43101/play',
		graphql_url: 'http://127.0.0.1:43102/graphql',
		diagnostics_url: 'http://127.0.0.1:43103',
		world_prefix: 'c360.semmachina.demo-run.bellweather-maze',
		campaign_id: 'demo-campaign'
	})
);

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((accept) => {
		resolve = accept;
	});
	return { promise, resolve };
}

describe('interactive Bellweather surface demo runner', () => {
	it('accepts only the exact demo argument without changing existing runner modes', () => {
		expect(parseSurfaceRunnerArguments([])).toEqual({ mode: 'paid' });
		expect(parseSurfaceRunnerArguments(['--preflight'])).toEqual({
			mode: 'preflight',
			interruptProof: false
		});
		expect(parseSurfaceRunnerArguments(['--preflight', '--interrupt-proof'])).toEqual({
			mode: 'preflight',
			interruptProof: true
		});
		expect(parseSurfaceRunnerArguments(['--demo'])).toEqual({ mode: 'demo' });
		for (const invalid of [
			['demo'],
			['--demo', '--auto-close-proof'],
			['--demo', '--interrupt-proof'],
			['--preflight', '--demo']
		]) {
			expect(() => parseSurfaceRunnerArguments(invalid)).toThrow('unsupported runner arguments');
		}
	});

	it('generates a fresh 32-byte credential and passes it only to the two trusted children', () => {
		const canary = createDemoCreatorCredential((size) => {
			expect(size).toBe(32);
			return Buffer.alloc(32, 0xa7);
		});
		expect(canary).toMatch(/^[A-Za-z0-9_-]{43}$/);
		const surface = buildDemoWebServerEnvironment(manifest, canary);
		const browser = buildDemoBrowserEnvironment(manifest, canary);
		expect(surface.SEMMACHINA_CREATOR_CREDENTIAL).toBe(canary);
		expect(browser).toEqual({
			REAL_SURFACE_WORLD_PREFIX: 'c360.semmachina.demo-run.bellweather-maze',
			REAL_SURFACE_DEMO_CREDENTIAL: canary
		});
		const clean = sanitizeBrowserEnvironment({
			GEMINI_API_KEY: canary,
			SEMMACHINA_PAID_SMOKE: '1',
			REAL_SURFACE_DEMO_CREDENTIAL: canary,
			SEMMACHINA_DEMO_AUTO_CLOSE_PROOF: '1',
			PATH: '/bin'
		});
		expect(clean).toEqual({ PATH: '/bin' });
		expect(JSON.stringify(surface)).toContain(canary);
		expect(JSON.stringify(browser)).toContain(canary);
		expect(JSON.stringify(clean)).not.toContain(canary);
	});

	it('reports secret-boundary failures without echoing the credential canary', () => {
		const canary = 'demo-secret-canary-that-must-never-be-echoed';
		expect(() => requireDemoSecretAbsent(canary, ['fixed output'])).not.toThrow();
		let error: unknown;
		try {
			requireDemoSecretAbsent(canary, [`argv=${canary}`]);
		} catch (caught) {
			error = caught;
		}
		expect(error).toBeInstanceOf(Error);
		expect((error as Error).message).toBe('surface demo secret boundary failed');
		expect((error as Error).message).not.toContain(canary);
	});

	it('consumes worker authority before a fake Chromium launch and clears worker memory', async () => {
		const credential = Buffer.alloc(32, 0xb3).toString('base64url');
		const signalPath = '/private/tmp/semmachina-demo-close-private/close.signal';
		const environment: Record<string, string | undefined> = {
			REAL_SURFACE_DEMO_CREDENTIAL: credential,
			REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH: signalPath,
			PATH: '/bin'
		};
		const sequence: string[] = [];
		let retained;
		await runDemoWorkerAuthorityFixture(environment, async (authority) => {
			sequence.push('automatic_worker_fixture');
			retained = authority;
			const chromiumEnvironment = { ...environment };
			sequence.push('fake_chromium_launch');
			expect(chromiumEnvironment).toEqual({ PATH: '/bin' });
			expect(authority).toEqual({ credential, closeSignalPath: signalPath });
		});
		expect(sequence).toEqual(['automatic_worker_fixture', 'fake_chromium_launch']);
		expect(retained).toEqual({ credential: '', closeSignalPath: undefined });

		const invalidEnvironment: Record<string, string | undefined> = {
			REAL_SURFACE_DEMO_CREDENTIAL: 'credential-canary-invalid',
			REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH: 'relative/close.signal'
		};
		await expect(
			runDemoWorkerAuthorityFixture(invalidEnvironment, async () => undefined)
		).rejects.toThrow('demo worker authority credential_invalid');
		expect(invalidEnvironment).toEqual({});
		const invalidPathEnvironment: Record<string, string | undefined> = {
			REAL_SURFACE_DEMO_CREDENTIAL: credential,
			REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH: 'relative/close.signal'
		};
		await expect(
			runDemoWorkerAuthorityFixture(invalidPathEnvironment, async () => undefined)
		).rejects.toThrow('demo worker authority close_signal_invalid');
		expect(invalidPathEnvironment).toEqual({});
	});

	it('keeps the closed bootstrap stage vocabulary unique', () => {
		expect(new Set(DEMO_BOOTSTRAP_STAGES).size).toBe(DEMO_BOOTSTRAP_STAGES.length);
	});

	it('creates, publishes, and removes an isolated proof-only close signal', async () => {
		const operations: unknown[] = [];
		const signal = await createDemoCloseSignal({
			temporaryRoot: '/private/tmp',
			mkdtemp: vi.fn(async (prefix: string) => {
				operations.push(['mkdtemp', prefix]);
				return '/private/tmp/semmachina-demo-close-private';
			}),
			chmod: vi.fn(async (target: string, mode: number) => {
				operations.push(['chmod', target, mode]);
			}),
			remove: vi.fn(async () => undefined)
		});
		expect(signal).toEqual({
			directory: '/private/tmp/semmachina-demo-close-private',
			path: '/private/tmp/semmachina-demo-close-private/close.signal'
		});
		expect(operations).toEqual([
			['mkdtemp', '/private/tmp/semmachina-demo-close-'],
			['chmod', '/private/tmp/semmachina-demo-close-private', 0o700]
		]);

		await publishDemoCloseSignal(signal, {
			writeFile: vi.fn(async (target: string, content: string, options: unknown) => {
				operations.push(['writeFile', target, content, options]);
			}),
			rename: vi.fn(async (from: string, to: string) => {
				operations.push(['rename', from, to]);
			})
		});
		expect(operations.slice(2)).toEqual([
			[
				'writeFile',
				'/private/tmp/semmachina-demo-close-private/close.signal.pending',
				'close\n',
				{ encoding: 'utf8', flag: 'wx', mode: 0o600 }
			],
			[
				'rename',
				'/private/tmp/semmachina-demo-close-private/close.signal.pending',
				'/private/tmp/semmachina-demo-close-private/close.signal'
			]
		]);

		const remove = vi.fn(async () => undefined);
		await cleanupDemoCloseSignal(signal, { remove });
		expect(remove).toHaveBeenCalledExactlyOnceWith(signal.directory, {
			force: true,
			recursive: true
		});
		await cleanupDemoCloseSignal(undefined, { remove });
		expect(remove).toHaveBeenCalledTimes(1);
		const presenterLines: string[] = [];
		emitDemoReady((line) => presenterLines.push(line));
		expect(presenterLines.join('\n')).not.toContain(signal.path);

		const failedRemove = vi.fn(async () => undefined);
		await expect(
			createDemoCloseSignal({
				temporaryRoot: '/private/tmp',
				mkdtemp: vi.fn(async () => '/private/tmp/semmachina-demo-close-failed'),
				chmod: vi.fn(async () => {
					throw new Error('permission setup failed');
				}),
				remove: failedRemove
			})
		).rejects.toThrow('permission setup failed');
		expect(failedRemove).toHaveBeenCalledExactlyOnceWith(
			'/private/tmp/semmachina-demo-close-failed',
			{ force: true, recursive: true }
		);
	});

	it('serializes overlapping close-signal checks and reruns a queued observation', async () => {
		const first = deferred<void>();
		let attempts = 0;
		const consume = vi.fn(async () => {
			attempts += 1;
			if (attempts === 1) {
				await first.promise;
				return false;
			}
			return true;
		});
		const check = createSerializedDemoCloseSignalCheck(consume);
		const immediate = check();
		const watcher = check();
		expect(watcher).toBe(immediate);
		first.resolve();
		await expect(immediate).resolves.toBe(true);
		expect(consume).toHaveBeenCalledTimes(2);
	});

	it('accepts only fixed bootstrap diagnostics and never reflects arbitrary text', () => {
		expect(parseDemoBootstrapDiagnostic('[surface-demo-bootstrap] progress:world_ready')).toEqual({
			kind: 'progress',
			stage: 'world_ready'
		});
		expect(parseDemoBootstrapDiagnostic('[surface-demo-bootstrap] failed:audit_detached')).toEqual({
			kind: 'failed',
			stage: 'audit_detached'
		});
		expect(parseDemoBootstrapDiagnostic(DEMO_PRESENTER_MARKER)).toEqual({ kind: 'ready' });
		const canary = 'credential-canary-must-not-enter-diagnostics';
		for (const arbitrary of [
			`[surface-demo-bootstrap] failed:${canary}`,
			`Error: ${canary}`,
			`[surface-demo-bootstrap] progress:world_ready:${canary}`
		]) {
			expect(parseDemoBootstrapDiagnostic(arbitrary)).toBeUndefined();
		}
	});

	it('accepts only a closed runner diagnostic sentinel from proof stderr', () => {
		expect(
			parseDemoRunnerDiagnostic('[surface-demo-runner] browser_bootstrap_failed:world_ready')
		).toEqual({ kind: 'browser_bootstrap_failed', stage: 'world_ready' });
		expect(
			parseDemoRunnerDiagnostic('[surface-demo-runner] browser_bootstrap_failed:unstarted')
		).toEqual({ kind: 'browser_bootstrap_failed', stage: 'unstarted' });
		const canary = 'credential-canary-must-not-enter-runner-diagnostics';
		for (const arbitrary of [
			`[surface-demo-runner] browser_bootstrap_failed:${canary}`,
			`Error: ${canary}`,
			`[surface-demo-runner] browser_bootstrap_failed:world_ready:${canary}`
		]) {
			expect(parseDemoRunnerDiagnostic(arbitrary)).toBeUndefined();
		}
	});

	it('classifies initial navigation without reflecting raw errors or credentials', () => {
		const canary = 'navigation-credential-canary-must-never-be-reflected';
		const categories = [
			classifyDemoNavigationFailure({ name: 'TimeoutError', message: `Timeout 30000ms ${canary}` }),
			classifyDemoNavigationFailure({ message: `net::ERR_CONNECTION_REFUSED ${canary}` }),
			classifyDemoNavigationFailure({ message: `net::ERR_CERT_AUTHORITY_INVALID ${canary}` }),
			classifyDemoNavigationFailure({ message: `Target page has been closed ${canary}` }),
			classifyDemoNavigationFailure({ message: `unrecognized ${canary}` })
		];
		expect(categories).toEqual([
			'navigation_timeout',
			'connection_refused',
			'tls_failure',
			'page_closed',
			'other'
		]);
		expect(JSON.stringify(categories)).not.toContain(canary);
		expect(
			parseDemoRunnerDiagnostic('[surface-demo-runner] browser_navigation_failed:http_status')
		).toEqual({ kind: 'browser_navigation_failed', category: 'http_status' });
		for (const raw of [
			`[surface-demo-runner] browser_navigation_failed:${canary}`,
			`[surface-demo-runner] browser_navigation_failed:other:${canary}`,
			`[surface-demo-runner] browser_pre_navigation_failed:${canary}`
		]) {
			expect(parseDemoRunnerDiagnostic(raw)).toBeUndefined();
		}
		expect(
			parseDemoRunnerDiagnostic(
				'[surface-demo-runner] browser_pre_navigation_failed:credential_invalid'
			)
		).toEqual({ kind: 'browser_pre_navigation_failed', category: 'credential_invalid' });
		expect(
			parseDemoRunnerDiagnostic(
				'[surface-demo-runner] browser_pre_navigation_failed:world_scope_invalid'
			)
		).toEqual({ kind: 'browser_pre_navigation_failed', category: 'world_scope_invalid' });
	});

	it('uses one absolute startup deadline capped at 180 seconds', async () => {
		expect(DEMO_STARTUP_LIMIT_MS).toBe(180_000);
		expect(createDemoStartupDeadline(() => 25)).toBe(180_025);
		let fire!: () => void;
		const clearTimer = vi.fn();
		const timed = withinDemoStartupDeadline(new Promise<never>(() => undefined), 180_000, {
			now: () => 10,
			setTimer: ((callback: () => void, milliseconds: number) => {
				expect(milliseconds).toBe(179_990);
				fire = callback;
				return { unref: vi.fn() };
			}) as unknown as typeof setTimeout,
			clearTimer: clearTimer as unknown as typeof clearTimeout
		});
		fire();
		await expect(timed).rejects.toThrow('surface demo startup timed out');
		expect(clearTimer).toHaveBeenCalledOnce();
	});

	it('prints bounded presenter text, holds until normal browser close, and rejects early exit', async () => {
		const ready = deferred<void>();
		const exit = deferred<{ code: number | null; signal: NodeJS.Signals | null }>();
		const lines: string[] = [];
		const holding = holdDemoPresentation(ready.promise, exit.promise, (line) => lines.push(line));
		ready.resolve();
		await vi.waitFor(() =>
			expect(lines).toEqual([
				'[surface-demo] presenter_ready',
				'URL: https://127.0.0.1:4181',
				'Login: the browser opens and authenticates automatically; see README.md.',
				'Each submitted action may incur Gemini API charges.',
				'Close the browser or press Ctrl-C to stop.'
			])
		);
		exit.resolve({ code: 0, signal: null });
		await expect(holding).resolves.toBeUndefined();

		const acknowledged: string[] = [];
		await expect(
			holdDemoPresentation(
				Promise.resolve(),
				Promise.resolve({ code: 0, signal: null }),
				vi.fn(),
				async () => {
					acknowledged.push('after-public-ready');
				}
			)
		).resolves.toBeUndefined();
		expect(acknowledged).toEqual(['after-public-ready']);

		await expect(
			holdDemoPresentation(
				new Promise<never>(() => undefined),
				Promise.resolve({ code: 0, signal: null }),
				vi.fn()
			)
		).rejects.toThrow('demo browser exited before presenter readiness');
	});

	it('wires the exact paid task and a headed artifact-free action-free bootstrap', () => {
		const taskfile = readFileSync(new URL('../../../../Taskfile.yml', import.meta.url), 'utf8');
		const task = taskfile.match(/ {2}demo:surface:\n([\s\S]*?)(?=\n {2}[a-z]|$)/)?.[1] ?? '';
		expect(task).toContain('test "$SEMMACHINA_PAID_SMOKE" = "1"');
		expect(task).toContain('test -n "$GEMINI_API_KEY"');
		expect(task).toContain('- exec node web/tests/run-bellweather-surface.mjs --demo');

		const config = readFileSync(
			new URL('../../../playwright.demo.config.ts', import.meta.url),
			'utf8'
		);
		expect(config).toMatch(/headless:\s*false/);
		expect(config).toMatch(/timeout:\s*0/);
		expect(config).toMatch(/trace:\s*'off'/);
		expect(config).toMatch(/screenshot:\s*'off'/);
		expect(config).toMatch(/video:\s*'off'/);

		const spec = readFileSync(
			new URL('../../../tests/bellweather-surface.demo.ts', import.meta.url),
			'utf8'
		);
		const testCallback = spec.indexOf(
			"test('authenticates the headed demo and yields control to the presenter'"
		);
		expect(testCallback).toBeGreaterThanOrEqual(0);
		expect(spec).toContain('base.extend<DemoTestFixtures, DemoWorkerFixtures>');
		expect(spec).toMatch(/demoAuthority:\s*\[\s*async \(\{ browserName \}, use\)/);
		expect(spec).toContain('void browserName;');
		expect(spec).toMatch(/scope:\s*'worker',\s*auto:\s*true/);
		expect(spec).toContain('runDemoWorkerAuthorityFixture(process.env, use)');
		expect(spec).not.toContain('delete process.env.REAL_SURFACE_DEMO_CREDENTIAL');
		expect(spec).not.toContain('delete process.env.REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH');
		expect(spec).toMatch(/async \(\{\s*page,\s*demoAuthority\s*\}\)/);
		expect(spec).toContain('DEMO_PRESENTER_MARKER');
		expect(DEMO_PRESENTER_MARKER).toBe('[surface-demo-bootstrap] presenter_ready');
		expect(spec).not.toMatch(/submit_action|first_action|second_action/);
		const handoffWrite = spec.indexOf('process.stdout.write(`${DEMO_PRESENTER_MARKER}\\n`');
		expect(handoffWrite).toBeGreaterThanOrEqual(0);
		for (const teardown of [
			"page.off('request', requestListener)",
			"page.off('websocket', websocketListener)",
			"socket.off('framesent', listener)",
			'frameListeners.clear()',
			'requests.length = 0'
		]) {
			expect(spec.indexOf(teardown)).toBeGreaterThanOrEqual(0);
			expect(spec.indexOf(teardown)).toBeLessThan(handoffWrite);
		}
		const afterHandoff = spec.slice(handoffWrite);
		const credentialBlank = spec.indexOf("demoAuthority.credential = '';");
		expect(credentialBlank).toBeGreaterThan(testCallback);
		expect(credentialBlank).toBeLessThan(handoffWrite);
		expect(afterHandoff).not.toMatch(/page\.(?:on|off|goto|click|fill|evaluate)/);
		expect(afterHandoff.match(/page\.close\(\)/g)).toHaveLength(1);
		expect(spec).not.toContain('process.stdin');
		expect(spec).toContain('watch(');
		expect(spec).toContain('access(');
		expect(afterHandoff).toContain("page.waitForEvent('close', { timeout: 0 })");

		const runnerSource = readFileSync(
			new URL('../../../tests/run-bellweather-surface.mjs', import.meta.url),
			'utf8'
		);
		expect(runnerSource).toContain('REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH: demoCloseSignal.path');
		expect(runnerSource).toContain(
			'demoAutoCloseProof ? await createDemoCloseSignal() : undefined'
		);
		expect(runnerSource).toContain('await cleanupDemoCloseSignal(demoCloseSignal)');
		const demoArgumentDeclaration = runnerSource.match(/const demoArguments = [^;]+;/)?.[0] ?? '';
		expect(demoArgumentDeclaration).not.toContain('REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH');
	});

	it('keeps presenter output fixed and credential-free', () => {
		const lines: string[] = [];
		emitDemoReady((line) => lines.push(line));
		expect(lines).toHaveLength(5);
		expect(lines.join('\n')).not.toContain('GEMINI_API_KEY');
		expect(lines.join('\n')).not.toContain('credential');
	});

	it('uses a bounded external signal proof against the normal demo mode', () => {
		expect(parseDemoSignalProofArguments([])).toEqual({
			signal: 'SIGINT',
			label: 'interrupt',
			autoClose: false
		});
		expect(parseDemoSignalProofArguments(['--auto-close'])).toEqual({
			signal: null,
			label: 'auto-close',
			autoClose: true
		});
		expect(() => parseDemoSignalProofArguments(['--interrupt-proof'])).toThrow();
		const source = readFileSync(
			new URL('../../../tests/prove-bellweather-surface-demo-interrupt.mjs', import.meta.url),
			'utf8'
		);
		expect(source).toContain("['tests/run-bellweather-surface.mjs', '--demo']");
		expect(source).toContain('signalDemoProofRunner(runner, proof.signal)');
		expect(source).toContain("SEMMACHINA_DEMO_AUTO_CLOSE_PROOF: '1'");
		expect(source).not.toContain("'--demo', '--interrupt-proof'");
		const runner = { kill: vi.fn(() => true) };
		expect(signalDemoProofRunner(runner, null)).toBe(false);
		expect(runner.kill).not.toHaveBeenCalled();
		expect(signalDemoProofRunner(runner, 'SIGINT')).toBe(true);
		expect(runner.kill).toHaveBeenCalledExactlyOnceWith('SIGINT');
	});

	it('cleans a wedged proof by terminating exact new owned groups before requiring quiet', async () => {
		const signals: Array<[number, NodeJS.Signals]> = [];
		const runnerSignals: NodeJS.Signals[] = [];
		let groupRead = 0;
		await expect(
			cleanupFailedDemoProof(
				{ pid: 700, kill: (signal) => (runnerSignals.push(signal), true) },
				new Promise<never>(() => undefined),
				new Set([11]),
				{
					settle: vi.fn(async () => false),
					ownedGroups: vi.fn(async () => {
						groupRead += 1;
						return groupRead === 1 ? new Set([11, 701, 702]) : new Set<number>();
					}),
					signalGroup: (group, signal) => signals.push([group, signal]),
					sleep: vi.fn(async () => undefined),
					quiet: vi.fn(async () => ({ listeners: [], runnerGroup: false }))
				}
			)
		).resolves.toBeUndefined();
		expect(runnerSignals).toEqual(['SIGTERM']);
		expect(signals).toEqual([
			[701, 'SIGTERM'],
			[702, 'SIGTERM'],
			[700, 'SIGTERM']
		]);
	});

	it('terminates newly discovered orphan groups even after the runner already settled', async () => {
		const signals: Array<[number, NodeJS.Signals]> = [];
		const runnerSignals: NodeJS.Signals[] = [];
		let groupRead = 0;
		await expect(
			cleanupFailedDemoProof(
				{ pid: 800, kill: (signal) => (runnerSignals.push(signal), true) },
				Promise.resolve({ code: 1, signal: null }),
				new Set([21]),
				{
					settle: vi.fn(async () => true),
					ownedGroups: vi.fn(async () => {
						groupRead += 1;
						return groupRead === 1 ? new Set([21, 801]) : new Set<number>();
					}),
					signalGroup: (group, signal) => signals.push([group, signal]),
					sleep: vi.fn(async () => undefined),
					quiet: vi.fn(async () => ({ listeners: [], runnerGroup: false }))
				}
			)
		).resolves.toBeUndefined();
		expect(runnerSignals).toEqual(['SIGTERM']);
		expect(signals).toEqual([[801, 'SIGTERM']]);
	});

	it('requires stopped, action-free, listener-free, process-free signal evidence without dumping it', () => {
		expect(() =>
			requireDemoSignalProof({
				output: '[surface-demo] presenter_ready\n[surface-demo] stopped\n',
				code: 0,
				signal: null,
				runnerGroupExists: false,
				listeners: [],
				processList: ''
			})
		).not.toThrow();
		const canary = 'PROOF_SECRET_CANARY_72f1';
		let failure: unknown;
		try {
			requireDemoSignalProof({
				output: `[surface-demo] presenter_ready\n${canary}`,
				code: 0,
				signal: null,
				runnerGroupExists: true,
				listeners: [43102],
				processList: 'playwright.demo.config.ts'
			});
		} catch (error) {
			failure = error;
		}
		expect(failure).toBeInstanceOf(Error);
		expect((failure as Error).message).toBe('surface demo signal proof failed');
		expect((failure as Error).message).not.toContain(canary);
	});
});
