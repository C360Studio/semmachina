import { describe, expect, it, vi } from 'vitest';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';

import { rolledDelivery } from '../player-v1/fixtures';

import {
	assertTerminalAfterAuthoritativeCompletion,
	buildBrowserTestEnvironment,
	buildWebServerEnvironment,
	buildGoEnvironment,
	buildSurfaceEnvironment,
	cleanupOwnedProcessGroups,
	controlledTerminationSignals,
	coordinateControlledRun,
	diagnosticObserverFailure,
	emitSurfaceCheckpoint,
	isDefaultBrowserTest,
	isRealBrowserTest,
	monitorTurn,
	parseDiagnosticSnapshot,
	parseStackManifest,
	classifyDiagnosticHTTPStatus,
	requireExactHTTPStatus,
	requirePaidRunnerEnvironment,
	requireProcessGroupSupport,
	requireSafeBrowserAudit,
	requireSafeProtocolPoll,
	requireSecretAbsent,
	sanitizeBrowserEnvironment,
	signalProcessGroup,
	stackCommandArguments,
	startAfterDiagnosticReadiness,
	superviseOwnedStage,
	surfaceServerCommandArguments,
	terminateProcessGroup,
	tlsProxyCommandArguments,
	watchOwnedProcess,
	waitForDiagnosticReadiness,
	waitForReadiness,
	useDetachedProcessGroup
} from '../../../tests/bellweather-surface-contract.mjs';
import {
	canonicalLatestRetrieveRequest,
	parseExactCsrfEnvelope,
	parseLatestRetrieveResponse,
	requireEmptyLatestRetrieveResponse,
	validateRetrievalProbeOutbound
} from '../../../tests/bellweather-retrieval-probe';

const manifestLine = JSON.stringify({
	status: 'ready',
	player_websocket_url: 'ws://127.0.0.1:43101/play',
	graphql_url: 'http://127.0.0.1:43102/graphql',
	diagnostics_url: 'http://127.0.0.1:43103',
	world_prefix: 'c360.semmachina.bellweather-run-1.bellweather-maze',
	campaign_id: 'campaign-1'
});

const diagnostic = (turnID: string, overrides: Record<string, unknown> = {}) => ({
	turn_id: turnID,
	phase: 'resolving',
	phase_recorded_at: '2026-08-03T14:15:29.000000Z',
	case_phase: 'cold_open',
	kit_hint_proof: { proved: false },
	failure: null,
	...overrides
});

const diagnosticResponse = (status: number, body: unknown, headers: Record<string, string> = {}) =>
	new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json', ...headers }
	});

const transitionResponse = () =>
	diagnosticResponse(
		404,
		{ error: 'turn_not_materialized' },
		{
			'retry-after': '1',
			'cache-control': 'no-store'
		}
	);

describe('real Bellweather surface runner contracts', () => {
	it('checks unauthorized HTTP status without consuming a response body', () => {
		const json = vi.fn(() => {
			throw new Error('body must not be consumed');
		});
		const text = vi.fn(() => {
			throw new Error('body must not be consumed');
		});
		const response = { status: () => 401, json, text };
		expect(() => requireExactHTTPStatus(response, 401, 'unauthorized world')).not.toThrow();
		expect(json).not.toHaveBeenCalled();
		expect(text).not.toHaveBeenCalled();
		expect(() => requireExactHTTPStatus(response, 200, 'authorized world')).toThrow(
			/authorized world returned unexpected HTTP status/
		);
	});

	it('reports paid browser audit failures without dumping secret-bearing evidence', () => {
		const canary = 'PAID_BROWSER_SECRET_CANARY_91f3';
		for (const audit of [
			() => requireSafeBrowserAudit(false, 'request_authority'),
			() => requireSecretAbsent(canary, [`headers=${canary}`], 'browser_state')
		]) {
			let failure: unknown;
			try {
				audit();
			} catch (error) {
				failure = error;
			}
			expect(failure).toBeInstanceOf(Error);
			const message = failure instanceof Error ? failure.message : '';
			expect(message).toMatch(/^paid browser (request authority|state) audit failed$/);
			expect(message).not.toContain(canary);
		}
	});

	it('bounds the early paid protocol poll failure without exposing stored evidence', () => {
		const canary = 'EARLY_PROTOCOL_POLL_SECRET_6b2e';
		let failure: unknown;
		try {
			requireSafeProtocolPoll([canary], 0);
		} catch (error) {
			failure = error;
		}
		expect(failure).toBeInstanceOf(Error);
		const message = failure instanceof Error ? failure.message : '';
		expect(message).toBe('paid browser protocol audit failed');
		expect(message).not.toContain(canary);
		expect(requireSafeProtocolPoll([], 2)).toBe(2);
	});

	it('closed-validates retrieval-probe authentication and canonical player frames', () => {
		const csrf = 'a'.repeat(43);
		expect(parseExactCsrfEnvelope({ csrf })).toBe(csrf);
		for (const invalid of [null, { csrf: 'short' }, { csrf, extra: true }, { csrf: `${csrf}!` }]) {
			expect(() => parseExactCsrfEnvelope(invalid)).toThrow(/authentication response/);
		}

		const request = canonicalLatestRetrieveRequest();
		expect(request).toEqual({ protocol: 'player/v1', type: 'retrieve_result', by: 'latest' });
		expect(validateRetrievalProbeOutbound(JSON.stringify(request))).toBe('latest_retrieval');
		expect(() =>
			validateRetrievalProbeOutbound(
				JSON.stringify({
					protocol: 'player/v1',
					text: 'forbidden action',
					idempotency_key: 'idem-probe-forbidden-0001'
				})
			)
		).toThrow(/forbidden submit frame/);

		const response = JSON.stringify({
			protocol: 'player/v1',
			type: 'retrieve_response',
			retrieval: {
				protocol: 'player/v1',
				status: 'refused',
				by: 'latest',
				refusal: { code: 'not_found', message: 'no prior result' }
			}
		});
		const emptyLatest = parseLatestRetrieveResponse(response);
		expect(emptyLatest).toMatchObject({
			status: 'refused',
			by: 'latest',
			refusal: { code: 'not_found' }
		});
		expect(() => requireEmptyLatestRetrieveResponse(emptyLatest)).not.toThrow();
		expect(() =>
			requireEmptyLatestRetrieveResponse({
				protocol: 'player/v1',
				status: 'refused',
				by: 'latest',
				refusal: { code: 'unavailable', message: 'temporarily unavailable' }
			})
		).toThrow(/^retrieval probe did not prove empty latest history$/);
		const foundLatest = parseLatestRetrieveResponse(
			JSON.stringify({
				protocol: 'player/v1',
				type: 'retrieve_response',
				retrieval: {
					protocol: 'player/v1',
					status: 'found',
					by: 'latest',
					delivery: rolledDelivery
				}
			})
		);
		expect(() => requireEmptyLatestRetrieveResponse(foundLatest)).toThrow(
			/^retrieval probe did not prove empty latest history$/
		);
		expect(() =>
			parseLatestRetrieveResponse(
				JSON.stringify({
					protocol: 'player/v1',
					type: 'operation_response',
					operation: {
						protocol: 'player/v1',
						status: 'refused',
						refusal: { code: 'malformed_operation', message: 'bad operation' }
					}
				})
			)
		).toThrow(/not a latest retrieval response/);
	});

	it('Taskfile preserves dotenv, paid gates, and direct ownership for paid and preflight runners', () => {
		const taskfile = readFileSync(new URL('../../../../Taskfile.yml', import.meta.url), 'utf8');
		const paidTask = taskfile.match(/ {2}smoke:gemini:surface:\n([\s\S]*?)(?=\n {2}[a-z]|$)/)?.[1];
		const preflightTask = taskfile.match(
			/ {2}smoke:surface:preflight:\n([\s\S]*?)(?=\n {2}[a-z]|$)/
		)?.[1];
		expect(/^dotenv: \['\.env'\]$/m.test(taskfile), 'Taskfile lost its top-level dotenv load').toBe(
			true
		);
		expect(paidTask !== undefined, 'paid surface task is missing').toBe(true);
		expect(preflightTask !== undefined, 'action-free preflight task is missing').toBe(true);
		expect(
			paidTask?.includes('- exec node web/tests/run-bellweather-surface.mjs') === true,
			'paid task does not directly exec the Node runner'
		).toBe(true);
		expect(
			preflightTask?.includes('- exec node web/tests/run-bellweather-surface.mjs --preflight') ===
				true,
			'preflight task does not directly exec the exact action-free runner command'
		).toBe(true);
		expect(
			paidTask?.includes('npm --prefix web') === false &&
				preflightTask?.includes('npm --prefix web') === false,
			'Taskfile runner commands reintroduced an npm intermediary'
		).toBe(true);
		expect(
			paidTask?.includes('test "$SEMMACHINA_PAID_SMOKE" = "1"') === true,
			'paid task lost its exact paid opt-in gate'
		).toBe(true);
		expect(
			paidTask?.includes('test -n "$GEMINI_API_KEY"') === true,
			'paid task lost its nonempty provider credential gate'
		).toBe(true);
	});

	it('accepts only the exact loopback stack manifest and derives the surface scope', () => {
		const manifest = parseStackManifest(manifestLine);
		expect(buildSurfaceEnvironment(manifest)).toEqual({
			REAL_SURFACE_CAMPAIGN_ID: 'campaign-1',
			REAL_SURFACE_DIAGNOSTICS_URL: 'http://127.0.0.1:43103',
			REAL_SURFACE_GRAPHQL_URL: 'http://127.0.0.1:43102/graphql',
			REAL_SURFACE_PLAYER_ID: 'c360.semmachina.bellweather-run-1.bellweather-maze.player.rowan',
			REAL_SURFACE_PLAYER_WS_URL: 'ws://127.0.0.1:43101/play',
			REAL_SURFACE_WORLD_NAMESPACE: 'bellweather-run-1',
			REAL_SURFACE_WORLD_PREFIX: 'c360.semmachina.bellweather-run-1.bellweather-maze',
			REAL_SURFACE_WORLD_TEMPLATE: 'bellweather-maze'
		});
	});

	it.each([
		['not JSON', 'not-json'],
		['wrong player port', manifestLine.replace('43101', '43111')],
		['wrong GraphQL path', manifestLine.replace('/graphql', '/query')],
		['remote diagnostic', manifestLine.replace('127.0.0.1:43103', 'example.test')],
		['malformed prefix', manifestLine.replace('c360.semmachina.', 'other.')],
		['empty campaign', manifestLine.replace('campaign-1', '')],
		['secret-shaped extra field', manifestLine.slice(0, -1) + ',"bearer":"secret"}']
	])('refuses a %s manifest', (_name, line) => {
		expect(() => parseStackManifest(line)).toThrow();
	});

	it('passes the paid credential only to Go and strips paid/provider state from Playwright', () => {
		const parent = {
			PATH: '/bin',
			GEMINI_API_KEY: 'gemini-secret',
			SEMMACHINA_PAID_SMOKE: '1',
			UNRELATED: 'kept'
		};
		expect(buildGoEnvironment(parent, 'gemini-secret')).toMatchObject({
			GEMINI_API_KEY: 'gemini-secret',
			SEMMACHINA_PAID_SMOKE: '1'
		});
		const browser = sanitizeBrowserEnvironment(parent);
		expect(browser).toMatchObject({ PATH: '/bin', UNRELATED: 'kept' });
		expect(browser).not.toHaveProperty('GEMINI_API_KEY');
		expect(browser).not.toHaveProperty('SEMMACHINA_PAID_SMOKE');
	});

	it('builds a fixed web-only environment without provider or paid state', () => {
		const manifest = parseStackManifest(manifestLine);
		expect(buildWebServerEnvironment(manifest)).toEqual({
			HOST: '127.0.0.1',
			PORT: '4173',
			ORIGIN: 'https://127.0.0.1:4181',
			SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:43102/graphql',
			SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
			SEMMACHINA_WORLD_ORG: 'c360',
			SEMMACHINA_WORLD_NAMESPACE: 'bellweather-run-1',
			SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze',
			SEMMACHINA_PUBLIC_ORIGIN: 'https://127.0.0.1:4181',
			SEMMACHINA_TLS_POSTURE: 'trusted_loopback_proxy',
			SEMMACHINA_CREATOR_CREDENTIAL: 'bellweather-surface-creator-secret',
			SEMMACHINA_PLAYER_BEARER: 'CHANGE-ME-bellweather-local-only-bearer',
			SEMMACHINA_PLAYER_WS_URL: 'ws://127.0.0.1:43101/play',
			SEMMACHINA_PLAYER_ID: 'c360.semmachina.bellweather-run-1.bellweather-maze.player.rowan'
		});
		expect(buildWebServerEnvironment(manifest)).not.toHaveProperty('GEMINI_API_KEY');
		expect(buildWebServerEnvironment(manifest)).not.toHaveProperty('SEMMACHINA_PAID_SMOKE');
		expect(buildBrowserTestEnvironment(manifest)).toEqual({
			REAL_SURFACE_CAMPAIGN_ID: 'campaign-1',
			REAL_SURFACE_DIAGNOSTICS_URL: 'http://127.0.0.1:43103',
			REAL_SURFACE_WORLD_PREFIX: 'c360.semmachina.bellweather-run-1.bellweather-maze'
		});
		expect(buildBrowserTestEnvironment(manifest)).not.toHaveProperty('REAL_SURFACE_GRAPHQL_URL');
	});

	it('requires the exact paid opt-in before runner children can start', () => {
		expect(
			requirePaidRunnerEnvironment({ SEMMACHINA_PAID_SMOKE: '1', GEMINI_API_KEY: 'key' })
		).toBe('key');
		for (const paid of [undefined, '', '0', 'true', '01']) {
			expect(() =>
				requirePaidRunnerEnvironment({ SEMMACHINA_PAID_SMOKE: paid, GEMINI_API_KEY: 'key' })
			).toThrow(/paid smoke is disabled/);
		}
		expect(() => requirePaidRunnerEnvironment({ SEMMACHINA_PAID_SMOKE: '1' })).toThrow(
			/GEMINI_API_KEY/
		);
	});

	it('keeps real discovery out of the default Playwright surface', () => {
		expect(isDefaultBrowserTest('tests/custom-server.e2e.ts')).toBe(true);
		expect(isDefaultBrowserTest('tests/bellweather-surface.real.e2e.ts')).toBe(false);
		expect(isRealBrowserTest('tests/custom-server.e2e.ts')).toBe(false);
		expect(isRealBrowserTest('tests/bellweather-surface.real.e2e.ts')).toBe(true);
	});

	it('uses detached POSIX process groups and escalates TERM to KILL only after waiting', async () => {
		expect(useDetachedProcessGroup('darwin')).toBe(true);
		expect(useDetachedProcessGroup('linux')).toBe(true);
		expect(useDetachedProcessGroup('win32')).toBe(false);
		expect(() => requireProcessGroupSupport('win32')).toThrow(/POSIX process-group/);
		const signals: Array<[number, NodeJS.Signals]> = [];
		const child = {
			pid: 42,
			exitCode: null,
			signalCode: null,
			kill: vi.fn(() => true),
			once: vi.fn(),
			off: vi.fn()
		};
		signalProcessGroup(child, 'SIGTERM', {
			platform: 'linux',
			kill: (pid: number, signal: NodeJS.Signals) => signals.push([pid, signal])
		});
		expect(signals).toEqual([[-42, 'SIGTERM']]);
		signals.length = 0;
		const waits = [false, true];
		const groupWaits = [false, true];
		await terminateProcessGroup(child, {
			platform: 'linux',
			kill: (pid: number, signal: NodeJS.Signals) => signals.push([pid, signal]),
			waitForExit: vi.fn(async () => waits.shift() ?? false),
			groupExists: () => true,
			waitForGroupExit: vi.fn(async () => groupWaits.shift() ?? false)
		});
		expect(signals).toEqual([
			[-42, 'SIGTERM'],
			[-42, 'SIGKILL']
		]);

		signals.length = 0;
		const reapedLeaderWithLiveDescendants = { ...child, exitCode: 0 };
		const descendantWaits = [false, true];
		await terminateProcessGroup(reapedLeaderWithLiveDescendants, {
			platform: 'linux',
			kill: (pid: number, signal: NodeJS.Signals) => signals.push([pid, signal]),
			waitForExit: async () => true,
			groupExists: () => true,
			waitForGroupExit: async () => descendantWaits.shift() ?? false
		});
		expect(signals).toEqual([
			[-42, 'SIGTERM'],
			[-42, 'SIGKILL']
		]);
	});

	it('accepts macOS EPERM only after the owned process-group leader is already reaped', async () => {
		const eperm = Object.assign(new Error('operation not permitted'), { code: 'EPERM' });
		const killed = vi.fn(() => true);
		const reaped = {
			pid: 73,
			exitCode: null,
			signalCode: null,
			kill: killed,
			once: vi.fn(),
			off: vi.fn()
		};
		await expect(
			terminateProcessGroup(reaped, {
				platform: 'darwin',
				leaderWasAlreadyReaped: true,
				groupExists: () => {
					throw eperm;
				}
			})
		).resolves.toBeUndefined();
		expect(killed).not.toHaveBeenCalled();

		const live = { ...reaped };
		await expect(
			terminateProcessGroup(live, {
				platform: 'darwin',
				leaderWasAlreadyReaped: false,
				groupExists: () => {
					throw eperm;
				}
			})
		).rejects.toMatchObject({ code: 'EPERM' });
		expect(killed).not.toHaveBeenCalled();
	});

	it('pins the real stack command to the dedicated Flash-Lite config and reserved ports', () => {
		const arguments_ = stackCommandArguments();
		expect(arguments_).toEqual([
			'run',
			'./cmd/bellweather-surface-stack',
			'-config',
			'configs/instance.gemini35-flash-lite.bellweather.example.json',
			'-world',
			'fixtures/worlds/bellweather-maze',
			'-player-addr',
			'127.0.0.1:43101',
			'-graphql-addr',
			'127.0.0.1:43102',
			'-diagnostic-addr',
			'127.0.0.1:43103'
		]);
		expect(arguments_).not.toContain('configs/instance.gemini36-flash.bellweather.example.json');
	});

	it('keeps Playwright test-only while the runner owns both web process groups', () => {
		const config = readFileSync(
			new URL('../../../playwright.real.config.ts', import.meta.url),
			'utf8'
		);
		expect(config).not.toMatch(/webServer\s*:/);
		expect(surfaceServerCommandArguments()).toEqual(['.server-build/server.js']);
		expect(tlsProxyCommandArguments()).toEqual(['tests/loopback-https-proxy.mjs']);
	});

	it('owns and supervises the detached initial build before declaring it complete', () => {
		const runner = readFileSync(
			new URL('../../../tests/run-bellweather-surface.mjs', import.meta.url),
			'utf8'
		);
		expect(runner).toMatch(/const build = watchOwnedProcess\(/);
		expect(runner).toMatch(/spawn\(npm, \['run', 'build'\], \{[\s\S]*?detached[\s\S]*?\}\)/);
		expect(runner).toMatch(/owned\.push\(build\)/);
		expect(runner).toMatch(/await superviseOwnedStage\(build\.exit, \[\]\)/);
		expect(runner.indexOf('owned.push(build)')).toBeLessThan(
			runner.indexOf("emitSurfaceCheckpoint('build_complete')")
		);
	});

	it('terminates and observes reaping when interruption arrives during the owned build', async () => {
		const child = Object.assign(new EventEmitter(), {
			pid: 73,
			exitCode: null as number | null,
			signalCode: null as NodeJS.Signals | null,
			kill: vi.fn(() => true)
		});
		const build = watchOwnedProcess('build', 'surface build', child);
		let interrupt!: () => void;
		const interruption = new Promise<never>((_resolve, reject) => {
			interrupt = () => reject(new Error('surface acceptance interrupted'));
		});
		const terminate = vi.fn(
			async (_child: object, dependencies: { leaderWasAlreadyReaped: boolean }, name: string) => {
				expect(name).toBe('build');
				expect(dependencies.leaderWasAlreadyReaped).toBe(false);
				child.signalCode = 'SIGTERM';
				child.emit('exit', null, 'SIGTERM');
			}
		);
		const running = coordinateControlledRun(
			async () => superviseOwnedStage(build.exit, []),
			async () => cleanupOwnedProcessGroups([build], terminate),
			interruption
		);
		interrupt();
		await expect(running).rejects.toThrow('surface acceptance interrupted');
		expect(terminate).toHaveBeenCalledOnce();
		expect(build.leaderWasAlreadyReaped).toBe(true);
	});

	it('orders diagnostic readiness between stack and downstream surface checkpoints', () => {
		const runner = readFileSync(
			new URL('../../../tests/run-bellweather-surface.mjs', import.meta.url),
			'utf8'
		);
		const checkpoints = [
			"emitSurfaceCheckpoint('stack_ready')",
			"emitSurfaceCheckpoint('diagnostic_ready')",
			"emitSurfaceCheckpoint('surface_ready')",
			"emitSurfaceCheckpoint('browser_tests_started')"
		];
		const positions = checkpoints.map((checkpoint) => runner.indexOf(checkpoint));
		expect(positions.every((position) => position >= 0)).toBe(true);
		expect(positions).toEqual([...positions].sort((left, right) => left - right));
	});

	it('retries readiness with bounded Retry-After before exact ready success', async () => {
		let now = 0;
		const sleeps: number[] = [];
		const responses = [
			new Response(null, { status: 503, headers: { 'retry-after': '2' } }),
			diagnosticResponse(200, { ready: true })
		];
		const fetch = vi.fn(async () => responses.shift() as Response);
		vi.stubGlobal('fetch', fetch);
		try {
			await expect(
				waitForDiagnosticReadiness('http://127.0.0.1:43103', {
					sleep: async (milliseconds) => {
						sleeps.push(milliseconds);
						now += milliseconds;
					},
					now: () => now
				})
			).resolves.toBeUndefined();
		} finally {
			vi.unstubAllGlobals();
		}
		expect(fetch).toHaveBeenCalledTimes(2);
		expect(sleeps).toEqual([2_000]);
	});

	it('counts readiness read time against the thirty-second total deadline', async () => {
		let now = 0;
		await expect(
			waitForDiagnosticReadiness('http://127.0.0.1:43103', {
				read: async (_url, timeoutMs) => {
					expect(timeoutMs).toBe(3_000);
					now += 30_000;
				},
				now: () => now
			})
		).rejects.toThrow(/30 second deadline/);
	});

	it('clamps Retry-After zero to positive backoff and reaches the readiness deadline', async () => {
		let now = 0;
		const sleeps: number[] = [];
		const fetch = vi.fn(
			async () => new Response(null, { status: 503, headers: { 'retry-after': '0' } })
		);
		vi.stubGlobal('fetch', fetch);
		try {
			await expect(
				waitForDiagnosticReadiness('http://127.0.0.1:43103', {
					sleep: async (milliseconds) => {
						sleeps.push(milliseconds);
						now += milliseconds;
					},
					now: () => now
				})
			).rejects.toThrow(/30 second deadline/);
		} finally {
			vi.unstubAllGlobals();
		}
		expect(now).toBe(30_000);
		expect(sleeps.length).toBeGreaterThan(0);
		expect(sleeps.every((milliseconds) => milliseconds === 250)).toBe(true);
		expect(fetch).toHaveBeenCalledTimes(120);
	});

	it.each([
		['case phase invariant', diagnosticResponse(500, { error: 'case_phase_invariant' })],
		['non-retryable upstream status', new Response(null, { status: 502 })],
		['unknown status', new Response(null, { status: 418 })],
		['malformed 200', new Response('{', { status: 200 })],
		['false ready', diagnosticResponse(200, { ready: false })],
		['extra ready key', diagnosticResponse(200, { ready: true, extra: true })]
	])('fails diagnostic readiness immediately for %s', async (_name, response) => {
		const fetch = vi.fn(async () => response);
		const sleep = vi.fn(async () => undefined);
		vi.stubGlobal('fetch', fetch);
		try {
			await expect(waitForDiagnosticReadiness('http://127.0.0.1:43103', { sleep })).rejects.toThrow(
				/diagnostic readiness/
			);
		} finally {
			vi.unstubAllGlobals();
		}
		expect(fetch).toHaveBeenCalledOnce();
		expect(sleep).not.toHaveBeenCalled();
	});

	it('lets an owned stack exit interrupt diagnostic readiness', async () => {
		const child = Object.assign(new EventEmitter(), {
			pid: 72,
			exitCode: null as number | null,
			signalCode: null as NodeJS.Signals | null,
			kill: vi.fn(() => true)
		});
		const stack = watchOwnedProcess('stack', 'Bellweather stack', child);
		const readiness = waitForDiagnosticReadiness('http://127.0.0.1:43103', {
			read: async () => new Promise<never>(() => undefined)
		});
		const supervised = superviseOwnedStage(readiness, [stack]);
		child.exitCode = 19;
		child.emit('exit', 19, null);
		await expect(supervised).rejects.toThrow(/Bellweather stack exited early \(19\)/);
	});

	it('does not start surface or browser work when diagnostic readiness fails', async () => {
		const startDownstream = vi.fn(() => 'started');
		await expect(
			startAfterDiagnosticReadiness(
				Promise.reject(new Error('diagnostic readiness terminal failure')),
				startDownstream
			)
		).rejects.toThrow(/diagnostic readiness terminal failure/);
		expect(startDownstream).not.toHaveBeenCalled();
	});

	it('emits only closed, non-secret checkpoint labels', () => {
		const lines: string[] = [];
		emitSurfaceCheckpoint('pre_action_ready', (line) => lines.push(line));
		expect(lines).toEqual(['[surface-checkpoint] pre_action_ready']);
		expect(() =>
			emitSurfaceCheckpoint('pre_action_ready turn-secret-1', (line) => lines.push(line))
		).toThrow(/unknown surface checkpoint/);
		expect(lines).toHaveLength(1);
	});

	it('polls readiness immediately and reports only the fixed service label on timeout', async () => {
		let attempts = 0;
		let now = 0;
		const sleep = vi.fn(async (milliseconds: number) => {
			now += milliseconds;
		});
		await expect(
			waitForReadiness('surface_http', async () => ++attempts === 3, {
				now: () => now,
				sleep,
				timeoutMs: 100,
				pollMs: 10
			})
		).resolves.toBeUndefined();
		expect(attempts).toBe(3);
		expect(sleep).toHaveBeenCalledTimes(2);

		await expect(
			waitForReadiness('tls_proxy', async () => false, {
				now: (() => {
					let tick = 0;
					return () => tick++ * 10;
				})(),
				sleep: async () => undefined,
				timeoutMs: 20,
				pollMs: 1
			})
		).rejects.toThrow(/^tls_proxy readiness timed out$/);
	});

	it('attempts cleanup for every owned process group even when one cleanup fails', async () => {
		const children = ['playwright', 'tls_proxy', 'surface', 'stack'].map((name, index) => ({
			name,
			child: { pid: index + 1 },
			leaderWasAlreadyReaped: index % 2 === 0
		}));
		const visited: string[] = [];
		const terminate = vi.fn(async (_child: object, dependencies: object, name: string) => {
			visited.push(name);
			if (name === 'tls_proxy') throw new Error('synthetic cleanup failure');
			expect(dependencies).toHaveProperty('leaderWasAlreadyReaped');
		});
		await expect(cleanupOwnedProcessGroups(children, terminate)).rejects.toThrow(
			/surface cleanup failed/
		);
		expect(visited).toEqual(['playwright', 'tls_proxy', 'surface', 'stack']);
		expect(terminate).toHaveBeenCalledTimes(4);
	});

	it('catches an earlier owned child exit immediately during a later readiness stage', async () => {
		const child = Object.assign(new EventEmitter(), {
			pid: 71,
			exitCode: null as number | null,
			signalCode: null as NodeJS.Signals | null,
			kill: vi.fn(() => true)
		});
		const stack = watchOwnedProcess('stack', 'Bellweather stack', child);
		child.exitCode = 17;
		child.emit('exit', 17, null);

		const laterReadiness = new Promise<never>(() => undefined);
		await expect(superviseOwnedStage(laterReadiness, [stack])).rejects.toThrow(
			/Bellweather stack exited early \(17\)/
		);
		expect(stack.leaderWasAlreadyReaped).toBe(true);
	});

	it('polls immediately and recognizes phase or authoritative timestamp movement as progress', async () => {
		let now = 0;
		const telemetry: unknown[] = [];
		const timeouts: number[] = [];
		const snapshots = [
			diagnostic('turn-1', { phase: 'accepted' }),
			diagnostic('turn-1', {
				phase: 'accepted',
				phase_recorded_at: '2026-08-03T14:15:30Z'
			}),
			diagnostic('turn-1', {
				phase: 'resolving',
				phase_recorded_at: '2026-08-03T14:15:30Z'
			}),
			diagnostic('turn-1', {
				phase: 'complete',
				phase_recorded_at: '2026-08-03T14:15:31.123456789Z',
				case_phase: 'discovery'
			})
		];
		const read = vi.fn(async (_url: string, timeoutMs: number) => {
			timeouts.push(timeoutMs);
			return snapshots.shift();
		});
		const sleep = vi.fn(async (milliseconds: number) => {
			now += Math.min(milliseconds, 29_500);
		});

		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-1', {
				read,
				sleep,
				now: () => now,
				emit: (entry) => telemetry.push(entry)
			})
		).resolves.toMatchObject({ phase: 'complete', case_phase: 'discovery' });
		expect(read).toHaveBeenCalledTimes(4);
		expect(sleep).toHaveBeenCalledTimes(3);
		expect(timeouts).toEqual([3_000, 3_000, 1_000, 3_000]);
		expect(telemetry).toEqual([
			{ label: 'authoritative_progress', elapsed_ms: 0, failure_count: 0 },
			{ label: 'authoritative_progress', elapsed_ms: 29_500, failure_count: 0 },
			{ label: 'authoritative_progress', elapsed_ms: 59_000, failure_count: 0 }
		]);
	});

	it('retries a timed-out first attempt and then accepts an authoritative success', async () => {
		let now = 0;
		let attempts = 0;
		const telemetry: unknown[] = [];
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-retry', {
				read: async (_url, timeoutMs) => {
					attempts += 1;
					if (attempts === 1) {
						now += timeoutMs;
						throw diagnosticObserverFailure('transport');
					}
					return diagnostic('turn-retry', {
						phase: 'complete',
						phase_recorded_at: '2026-08-03T14:15:30Z'
					});
				},
				sleep: async (milliseconds) => {
					now += milliseconds;
				},
				now: () => now,
				emit: (entry) => telemetry.push(entry)
			})
		).resolves.toMatchObject({ phase: 'complete' });
		expect(attempts).toBe(2);
		expect(now).toBe(3_250);
		expect(telemetry).toEqual([{ label: 'observer_retry', elapsed_ms: 3_000, failure_count: 1 }]);
	});

	it('honors the exact one-second transition 404 before a valid 200 success', async () => {
		let now = 0;
		const sleeps: number[] = [];
		const responses = [
			transitionResponse(),
			diagnosticResponse(
				200,
				diagnostic('turn-transition', {
					phase: 'complete',
					phase_recorded_at: '2026-08-03T14:15:30Z'
				})
			)
		];
		const fetch = vi.fn(async () => responses.shift() as Response);
		vi.stubGlobal('fetch', fetch);
		try {
			await expect(
				monitorTurn('http://127.0.0.1:43103', 'turn-transition', {
					sleep: async (milliseconds) => {
						sleeps.push(milliseconds);
						now += milliseconds;
					},
					now: () => now,
					emit: () => undefined
				})
			).resolves.toMatchObject({
				phase: 'complete',
				phase_recorded_at: '2026-08-03T14:15:30Z'
			});
		} finally {
			vi.unstubAllGlobals();
		}
		expect(fetch).toHaveBeenCalledTimes(2);
		expect(sleeps).toEqual([1_000]);
	});

	it.each([
		['accepted invariant', { error: 'accepted_turn_invariant' }, '1', 'no-store'],
		['unknown error', { error: 'unknown' }, '1', 'no-store'],
		['extra key', { error: 'turn_not_materialized', extra: true }, '1', 'no-store'],
		['missing retry header', { error: 'turn_not_materialized' }, undefined, 'no-store'],
		['invalid retry header', { error: 'turn_not_materialized' }, '2', 'no-store'],
		['missing cache header', { error: 'turn_not_materialized' }, '1', undefined],
		['invalid cache header', { error: 'turn_not_materialized' }, '1', 'no-cache']
	])('does not retry a %s 404 response', async (_name, body, retryAfter, cacheControl) => {
		const headers = {
			...(retryAfter === undefined ? {} : { 'retry-after': retryAfter }),
			...(cacheControl === undefined ? {} : { 'cache-control': cacheControl })
		};
		const fetch = vi.fn(async () => diagnosticResponse(404, body, headers));
		const sleep = vi.fn(async () => undefined);
		vi.stubGlobal('fetch', fetch);
		try {
			await expect(
				monitorTurn('http://127.0.0.1:43103', 'turn-terminal-404', {
					sleep,
					emit: () => undefined
				})
			).rejects.toThrow(/terminal 404 transition response/);
		} finally {
			vi.unstubAllGlobals();
		}
		expect(fetch).toHaveBeenCalledOnce();
		expect(sleep).not.toHaveBeenCalled();
	});

	it('does not retry malformed JSON in a 404 transition response', async () => {
		const fetch = vi.fn(
			async () =>
				new Response('{', {
					status: 404,
					headers: { 'retry-after': '1', 'cache-control': 'no-store' }
				})
		);
		const sleep = vi.fn(async () => undefined);
		vi.stubGlobal('fetch', fetch);
		try {
			await expect(
				monitorTurn('http://127.0.0.1:43103', 'turn-malformed-404', {
					sleep,
					emit: () => undefined
				})
			).rejects.toThrow(/terminal 404 transition response/);
		} finally {
			vi.unstubAllGlobals();
		}
		expect(fetch).toHaveBeenCalledOnce();
		expect(sleep).not.toHaveBeenCalled();
	});

	it('bounds continuous observer unavailability at ten seconds', async () => {
		let now = 0;
		const timeouts: number[] = [];
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-unavailable', {
				read: async (_url, timeoutMs) => {
					timeouts.push(timeoutMs);
					now += timeoutMs;
					throw diagnosticObserverFailure('upstream_unavailable');
				},
				sleep: async (milliseconds) => {
					now += milliseconds;
				},
				now: () => now,
				emit: () => undefined
			})
		).rejects.toThrow(/10 seconds of continuous observer unavailability/);
		expect(now).toBe(10_000);
		expect(timeouts.at(-1)).toBeLessThanOrEqual(3_000);
	});

	it('rejects at the loop-top unavailability deadline without starting a recovery read', async () => {
		let now = 0;
		let reads = 0;
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-loop-deadline', {
				read: async () => {
					reads += 1;
					if (now < 10_000) throw diagnosticObserverFailure('transport');
					return diagnostic('turn-loop-deadline', {
						phase: 'complete',
						phase_recorded_at: '2026-08-03T14:15:30Z'
					});
				},
				sleep: async (milliseconds) => {
					now += milliseconds;
				},
				now: () => now,
				emit: () => undefined
			})
		).rejects.toThrow(/10 seconds of continuous observer unavailability/);
		expect(now).toBe(10_000);
		expect(reads).toBe(40);
	});

	it('shares one exact ten-second budget across mixed transition and transport failures', async () => {
		let now = 0;
		let reads = 0;
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-mixed-unavailable', {
				read: async () => {
					reads += 1;
					if (now >= 10_000) {
						return diagnostic('turn-mixed-unavailable', { phase: 'complete' });
					}
					throw diagnosticObserverFailure(reads % 2 === 1 ? 'turn_not_materialized' : 'transport');
				},
				sleep: async (milliseconds) => {
					now += milliseconds;
				},
				now: () => now,
				emit: () => undefined
			})
		).rejects.toThrow(/10 seconds of continuous observer unavailability/);
		expect(now).toBe(10_000);
		expect(reads).toBe(16);
	});

	it('fails immediately when a transition 404 is followed by malformed 200 JSON', async () => {
		let now = 0;
		const sleeps: number[] = [];
		const responses = [transitionResponse(), new Response('{', { status: 200 })];
		const fetch = vi.fn(async () => responses.shift() as Response);
		vi.stubGlobal('fetch', fetch);
		try {
			await expect(
				monitorTurn('http://127.0.0.1:43103', 'turn-malformed-200', {
					sleep: async (milliseconds) => {
						sleeps.push(milliseconds);
						now += milliseconds;
					},
					now: () => now,
					emit: () => undefined
				})
			).rejects.toThrow(/invalid JSON/);
		} finally {
			vi.unstubAllGlobals();
		}
		expect(fetch).toHaveBeenCalledTimes(2);
		expect(sleeps).toEqual([1_000]);
	});

	it('reports the last closed stages at the loop-top stall deadline without leaking identity', async () => {
		let now = 0;
		let reads = 0;
		const canary = 'turn-STALL_SECRET_LOOP_TOP_39c5';
		const unchanged = diagnostic(canary, {
			phase: 'resolving',
			phase_recorded_at: '1970-01-01T00:00:00Z',
			case_phase: 'investigation'
		});
		const sleeps: number[] = [];
		const stalled = monitorTurn('http://127.0.0.1:43103', canary, {
			read: async () => {
				reads += 1;
				return unchanged;
			},
			sleep: async (milliseconds) => {
				sleeps.push(milliseconds);
				now += milliseconds;
			},
			now: () => now,
			emit: () => undefined
		});
		await expect(stalled).rejects.toMatchObject({
			message:
				'agentic phase observation budget exceeded (phase=resolving case_phase=investigation)'
		});
		await expect(stalled).rejects.not.toThrow(canary);
		expect(reads).toBe(2);
		expect(sleeps).toEqual([30_000, 30_000]);
	});

	it('reports the last closed stages when a retryable read reaches the stall deadline', async () => {
		let now = 0;
		let reads = 0;
		const canary = 'turn-STALL_SECRET_RETRY_4d27';
		const unchanged = diagnostic(canary, {
			phase: 'resolving',
			phase_recorded_at: '1970-01-01T00:00:00Z',
			case_phase: 'investigation'
		});
		const sleeps: number[] = [];
		const stalled = monitorTurn('http://127.0.0.1:43103', canary, {
			read: async () => {
				reads += 1;
				if (reads === 3) {
					now += 1_000;
					throw diagnosticObserverFailure('transport');
				}
				return unchanged;
			},
			sleep: async (milliseconds) => {
				sleeps.push(milliseconds);
				now += Math.min(milliseconds, 29_500);
			},
			now: () => now,
			emit: () => undefined
		});
		await expect(stalled).rejects.toMatchObject({
			message:
				'agentic phase observation budget exceeded (phase=resolving case_phase=investigation)'
		});
		await expect(stalled).rejects.not.toThrow(canary);
		expect(reads).toBe(3);
		expect(sleeps).toEqual([30_000, 30_000]);
	});

	it('reports the last closed stages when a validated read reaches the stall deadline', async () => {
		let now = 0;
		let reads = 0;
		const canary = 'turn-STALL_SECRET_POST_READ_87e1';
		const unchanged = diagnostic(canary, {
			phase: 'resolving',
			phase_recorded_at: '1970-01-01T00:00:00Z',
			case_phase: 'investigation'
		});
		const sleeps: number[] = [];
		const stalled = monitorTurn('http://127.0.0.1:43103', canary, {
			read: async () => {
				reads += 1;
				if (reads === 3) now += 1_000;
				return unchanged;
			},
			sleep: async (milliseconds) => {
				sleeps.push(milliseconds);
				now += Math.min(milliseconds, 29_500);
			},
			now: () => now,
			emit: () => undefined
		});
		await expect(stalled).rejects.toMatchObject({
			message:
				'agentic phase observation budget exceeded (phase=resolving case_phase=investigation)'
		});
		await expect(stalled).rejects.not.toThrow(canary);
		expect(reads).toBe(3);
		expect(sleeps).toEqual([30_000, 30_000]);
	});

	it('ages interpreting from its authoritative timestamp and expires at 120 seconds', async () => {
		let now = 60_000;
		const observedAt: number[] = [];
		const sleeps: number[] = [];
		const snapshot = diagnostic('turn-interpreting-budget', {
			phase: 'interpreting',
			phase_recorded_at: '1970-01-01T00:00:00Z',
			case_phase: 'investigation'
		});
		const monitored = monitorTurn('http://127.0.0.1:43103', 'turn-interpreting-budget', {
			read: async () => {
				observedAt.push(now);
				return snapshot;
			},
			sleep: async (milliseconds) => {
				sleeps.push(milliseconds);
				now += milliseconds;
			},
			now: () => now,
			emit: () => undefined
		});
		await expect(monitored).rejects.toMatchObject({
			message:
				'agentic phase observation budget exceeded (phase=interpreting case_phase=investigation)'
		});
		expect(observedAt).toEqual([60_000, 90_000]);
		expect(sleeps).toEqual([30_000, 30_000]);
	});

	it('ages narrating from its authoritative timestamp and expires at 150 seconds', async () => {
		let now = 120_000;
		const observedAt: number[] = [];
		const sleeps: number[] = [];
		const snapshot = diagnostic('turn-narrating-budget', {
			phase: 'narrating',
			phase_recorded_at: '1970-01-01T00:00:00Z',
			case_phase: 'denouement'
		});
		const monitored = monitorTurn('http://127.0.0.1:43103', 'turn-narrating-budget', {
			read: async () => {
				observedAt.push(now);
				return snapshot;
			},
			sleep: async (milliseconds) => {
				sleeps.push(milliseconds);
				now += milliseconds;
			},
			now: () => now,
			emit: () => undefined
		});
		await expect(monitored).rejects.toMatchObject({
			message: 'agentic phase observation budget exceeded (phase=narrating case_phase=denouement)'
		});
		expect(observedAt).toEqual([120_000]);
		expect(sleeps).toEqual([30_000]);
	});

	it('expires a static phase at the exact authoritative 60-second boundary', async () => {
		let now = 60_000;
		const sleep = vi.fn(async (milliseconds: number) => {
			now += milliseconds;
		});
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-static-budget', {
				read: async () =>
					diagnostic('turn-static-budget', {
						phase: 'applying',
						phase_recorded_at: '1970-01-01T00:00:00Z',
						case_phase: 'discovery'
					}),
				sleep,
				now: () => now,
				emit: () => undefined
			})
		).rejects.toMatchObject({
			message: 'agentic phase observation budget exceeded (phase=applying case_phase=discovery)'
		});
		expect(sleep).not.toHaveBeenCalled();
	});

	it('allows budget minus one millisecond and expires exactly at the boundary', async () => {
		let now = 59_999;
		let reads = 0;
		const sleeps: number[] = [];
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-budget-edge', {
				read: async () => {
					reads += 1;
					return diagnostic('turn-budget-edge', {
						phase: 'resolving',
						phase_recorded_at: '1970-01-01T00:00:00Z',
						case_phase: 'discovery'
					});
				},
				sleep: async (milliseconds) => {
					sleeps.push(milliseconds);
					now += milliseconds;
				},
				now: () => now,
				emit: () => undefined
			})
		).rejects.toMatchObject({
			message: 'agentic phase observation budget exceeded (phase=resolving case_phase=discovery)'
		});
		expect(reads).toBe(1);
		expect(sleeps).toEqual([1]);
	});

	it('clamps a future authoritative timestamp so it cannot extend a mapped budget', async () => {
		let now = 0;
		let reads = 0;
		const sleeps: number[] = [];
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-future-phase', {
				read: async () => {
					reads += 1;
					return diagnostic('turn-future-phase', {
						phase: 'applying',
						phase_recorded_at: '2099-01-01T00:00:00Z',
						case_phase: 'investigation'
					});
				},
				sleep: async (milliseconds) => {
					sleeps.push(milliseconds);
					now += milliseconds;
				},
				now: () => now,
				emit: () => undefined
			})
		).rejects.toMatchObject({
			message: 'agentic phase observation budget exceeded (phase=applying case_phase=investigation)'
		});
		expect(reads).toBe(2);
		expect(sleeps).toEqual([30_000, 30_000]);
	});

	it('resets the authoritative phase budget on a phase transition', async () => {
		let now = 30_000;
		let reads = 0;
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-phase-transition', {
				read: async () => {
					reads += 1;
					if (reads === 1) {
						return diagnostic('turn-phase-transition', {
							phase: 'accepted',
							phase_recorded_at: '1970-01-01T00:00:00Z'
						});
					}
					if (reads === 2) {
						return diagnostic('turn-phase-transition', {
							phase: 'interpreting',
							phase_recorded_at: '1970-01-01T00:00:59Z'
						});
					}
					return diagnostic('turn-phase-transition', {
						phase: 'complete',
						phase_recorded_at: '1970-01-01T00:01:28Z'
					});
				},
				sleep: async (milliseconds) => {
					now += Math.min(milliseconds, 29_000);
				},
				now: () => now,
				emit: () => undefined
			})
		).resolves.toMatchObject({ phase: 'complete' });
		expect(reads).toBe(3);
	});

	it('fails terminal HTTP classes, schema errors, and failed phase immediately', async () => {
		expect(classifyDiagnosticHTTPStatus(200)).toBe('ok');
		for (const status of [429, 502, 503, 504]) {
			expect(classifyDiagnosticHTTPStatus(status)).toBe('retryable');
		}
		for (const status of [400, 404, 418, 500]) {
			expect(classifyDiagnosticHTTPStatus(status)).toBe('terminal');
		}
		for (const turnID of ['turn-400', 'turn-404']) {
			const sleep = vi.fn(async () => undefined);
			await expect(
				monitorTurn('http://127.0.0.1:43103', turnID, {
					read: async () => {
						throw diagnosticObserverFailure('terminal_http');
					},
					sleep
				})
			).rejects.toThrow(/terminal HTTP response/);
			expect(sleep).not.toHaveBeenCalled();
		}
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-schema', {
				read: async () => ({ ...diagnostic('turn-schema'), phase_recorded_at: '' })
			})
		).rejects.toThrow(/invalid shape/);
		const emptyPhaseSleep = vi.fn(async () => undefined);
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-empty-phase', {
				read: async () => diagnostic('turn-empty-phase', { phase: '' }),
				sleep: emptyPhaseSleep
			})
		).rejects.toMatchObject({ message: 'diagnostic snapshot has an invalid shape' });
		expect(emptyPhaseSleep).not.toHaveBeenCalled();
		const failedSleep = vi.fn(async () => undefined);
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-failed', {
				read: async () =>
					diagnostic('turn-failed', {
						phase: 'failed',
						failure: {
							reason: 'persona-loop-failed',
							class: 'provider-model',
							authorization_reason: null
						}
					}),
				sleep: failedSleep
			})
		).rejects.toMatchObject({
			message:
				'diagnostic monitor reached failed phase (reason=persona-loop-failed class=provider-model authorization_reason=null)'
		});
		expect(failedSleep).not.toHaveBeenCalled();
		const authorizationSleep = vi.fn(async () => undefined);
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-authorization', {
				read: async () =>
					diagnostic('turn-authorization', {
						phase: 'failed',
						failure: {
							reason: 'knowledge-unauthorized',
							class: 'deterministic',
							authorization_reason: 'wrong-turn'
						}
					}),
				sleep: authorizationSleep
			})
		).rejects.toMatchObject({
			message:
				'diagnostic monitor reached failed phase (reason=knowledge-unauthorized class=deterministic authorization_reason=wrong-turn)'
		});
		expect(authorizationSleep).not.toHaveBeenCalled();

		const canary = 'DO_NOT_LEAK_FAILURE_DETAIL_6f7f0911';
		const canarySleep = vi.fn(async () => undefined);
		let canaryFailure: unknown;
		try {
			await monitorTurn('http://127.0.0.1:43103', 'turn-canary', {
				read: async () =>
					diagnostic('turn-canary', {
						phase: 'failed',
						failure: {
							reason: 'knowledge-unauthorized',
							class: 'deterministic',
							authorization_reason: canary
						}
					}),
				sleep: canarySleep
			});
		} catch (error) {
			canaryFailure = error;
		}
		expect(canaryFailure).toBeInstanceOf(Error);
		const canaryMessage = canaryFailure instanceof Error ? canaryFailure.message : '';
		expect(canaryMessage).toBe('diagnostic failure has an invalid shape');
		expect(canaryMessage).not.toContain(canary);
		expect(canarySleep).not.toHaveBeenCalled();
	});

	it('lets the paid absolute budget win across legitimate phase transitions', async () => {
		let now = 0;
		let sequence = 0;
		await expect(
			monitorTurn('http://127.0.0.1:43103', 'turn-too-long', {
				read: async (_url, timeoutMs) => {
					sequence += 1;
					if (sequence === 7) now += timeoutMs;
					const phase = sequence <= 2 ? 'accepted' : sequence <= 5 ? 'interpreting' : 'narrating';
					const phaseRecordedAt =
						sequence <= 2
							? '1970-01-01T00:00:00Z'
							: sequence <= 5
								? '1970-01-01T00:00:59Z'
								: '1970-01-01T00:02:27.500Z';
					return diagnostic('turn-too-long', {
						phase,
						phase_recorded_at: phaseRecordedAt
					});
				},
				sleep: async () => {
					now += 29_500;
				},
				now: () => now,
				emit: () => undefined
			})
		).rejects.toMatchObject({ message: 'paid turn absolute budget exceeded' });
		expect(now).toBe(180_000);
	});

	it('closed-validates diagnostics and the exact persisted Kit proof', () => {
		expect(parseDiagnosticSnapshot(diagnostic('turn-1'), 'turn-1')).toEqual(diagnostic('turn-1'));
		const proved = diagnostic('turn-1', {
			phase: 'complete',
			kit_hint_proof: {
				proved: true,
				case_decision_kind: 'request_hint',
				trigger_kind: 'player-hint',
				trigger_source: 'case-decision'
			}
		});
		expect(parseDiagnosticSnapshot(proved, 'turn-1')).toEqual(proved);
		for (const obsoleteProof of [
			{
				proved: true,
				case_decision_kind: 'request_hint',
				trigger_kind: 'player_hint',
				trigger_source: 'case-decision'
			},
			{
				proved: true,
				case_decision_kind: 'request_hint',
				trigger_kind: 'player-hint',
				trigger_source: 'case_decision'
			}
		]) {
			let proofFailure: unknown;
			try {
				parseDiagnosticSnapshot(diagnostic('turn-1', { kit_hint_proof: obsoleteProof }), 'turn-1');
			} catch (error) {
				proofFailure = error;
			}
			expect(proofFailure).toBeInstanceOf(Error);
			const proofMessage = proofFailure instanceof Error ? proofFailure.message : '';
			expect(proofMessage).toBe('kit_hint_proof does not prove the exact persisted hint route');
			expect(proofMessage).not.toContain('player_hint');
			expect(proofMessage).not.toContain('case_decision');
		}
		const failureClasses = [
			'provider-model',
			'model-output-limit',
			'agent-runtime',
			'agent-limit',
			'deterministic',
			'unknown'
		];
		const validFailurePairs = new Map([
			['effect-invalid', ['deterministic', 'unknown']],
			['effect-entity-missing', ['deterministic', 'unknown']],
			['effect-entity-kind', ['deterministic', 'unknown']],
			['effect-commit-incomplete', ['deterministic', 'unknown']],
			['persona-cap-exhausted', ['agent-limit', 'unknown']],
			['persona-loop-failed', ['provider-model', 'model-output-limit', 'agent-runtime', 'unknown']],
			['turn-stranded', ['deterministic', 'unknown']],
			['knowledge-unauthorized', ['deterministic', 'unknown']],
			['accusation-invalid', ['deterministic', 'unknown']],
			['case-progress-invalid', ['deterministic', 'unknown']]
		]);
		const authorizationReasons: Array<string | null> = [
			null,
			'wrong-turn',
			'wrong-case',
			'wrong-actor',
			'invalid-target',
			'ineligible-reveal',
			'ineligible-phase',
			'solution-locked',
			'question-target-mismatch',
			'share-source-unknown',
			'share-target-unauthorized',
			'witness-unauthorized',
			'unsupported-kind'
		];
		for (const [reason, validClasses] of validFailurePairs) {
			for (const failureClass of failureClasses) {
				for (const authorizationReason of authorizationReasons) {
					const failed = diagnostic('turn-1', {
						phase: 'failed',
						failure: {
							reason,
							class: failureClass,
							authorization_reason: authorizationReason
						}
					});
					const validPair = validClasses.includes(failureClass);
					const validAuthorization =
						authorizationReason === null ||
						(reason === 'knowledge-unauthorized' && failureClass === 'deterministic');
					if (validPair && validAuthorization) {
						expect(parseDiagnosticSnapshot(failed, 'turn-1')).toEqual(failed);
					} else {
						expect(() => parseDiagnosticSnapshot(failed, 'turn-1')).toThrow(
							validPair
								? /invalid authorization reason combination/
								: /invalid reason and class combination/
						);
					}
				}
			}
		}
		const missingFailure: Record<string, unknown> = { ...diagnostic('turn-1') };
		delete missingFailure.failure;

		const invalid = [
			missingFailure,
			{ ...diagnostic('turn-1'), extra: true },
			diagnostic('turn-other'),
			diagnostic('turn-1', { phase: 'resolvin' }),
			diagnostic('turn-1', {
				failure: { reason: 'persona-loop-failed', class: 'provider-model' }
			}),
			diagnostic('turn-1', { phase: 'failed', failure: null }),
			diagnostic('turn-1', { phase: 'failed', failure: 'persona-loop-failed' }),
			diagnostic('turn-1', { phase: 'failed', failure: [] }),
			diagnostic('turn-1', {
				phase: 'failed',
				failure: { reason: 'persona-loop-failed' }
			}),
			diagnostic('turn-1', {
				phase: 'failed',
				failure: {
					reason: 'persona-loop-failed',
					class: 'provider-model',
					authorization_reason: null,
					detail: 'unsafe'
				}
			}),
			diagnostic('turn-1', {
				phase: 'failed',
				failure: {
					reason: 'provider-refused',
					class: 'provider-model',
					authorization_reason: null
				}
			}),
			diagnostic('turn-1', {
				phase: 'failed',
				failure: { reason: 1, class: 'provider-model', authorization_reason: null }
			}),
			diagnostic('turn-1', {
				phase: 'failed',
				failure: {
					reason: 'persona-loop-failed',
					class: 'provider',
					authorization_reason: null
				}
			}),
			diagnostic('turn-1', {
				phase: 'failed',
				failure: {
					reason: 'knowledge-unauthorized',
					class: 'deterministic',
					authorization_reason: 'wrong-world'
				}
			}),
			diagnostic('turn-1', {
				phase: 'failed',
				failure: {
					reason: 'knowledge-unauthorized',
					class: 'deterministic',
					authorization_reason: 1
				}
			}),
			diagnostic('turn-1', { case_phase: 'discovering' }),
			diagnostic('turn-1', { phase_recorded_at: '0000-01-01T00:00:00Z' }),
			diagnostic('turn-1', { phase_recorded_at: '2026-02-30T14:15:29Z' }),
			diagnostic('turn-1', { phase_recorded_at: '2026-08-03 14:15:29Z' }),
			{ ...diagnostic('turn-1'), turn_version: 1 },
			{ ...diagnostic('turn-1'), turn_updated_at: '2026-08-03T14:15:29Z' },
			{ ...diagnostic('turn-1'), pending: 1 },
			diagnostic('turn-1', { kit_hint_proof: { proved: false, trigger_kind: 'player-hint' } }),
			diagnostic('turn-1', {
				kit_hint_proof: {
					proved: true,
					case_decision_kind: 'observe',
					trigger_kind: 'player-hint',
					trigger_source: 'case-decision'
				}
			})
		];
		for (const snapshot of invalid) {
			expect(() => parseDiagnosticSnapshot(snapshot, 'turn-1')).toThrow();
		}
	});

	it('short-circuits terminal assertions when authoritative monitoring rejects', async () => {
		const terminal = vi.fn(async () => undefined);
		await expect(
			assertTerminalAfterAuthoritativeCompletion(
				Promise.reject(new Error('authoritative diagnostics failed')),
				terminal
			)
		).rejects.toThrow(/authoritative diagnostics failed/);
		expect(terminal).not.toHaveBeenCalled();

		await expect(
			assertTerminalAfterAuthoritativeCompletion(Promise.resolve('complete'), terminal)
		).resolves.toBe('complete');
		expect(terminal).toHaveBeenCalledOnce();
	});

	it('converts termination signals into rejection and awaits cleanup before returning', async () => {
		const listeners = new Map<NodeJS.Signals, () => void>();
		const source = {
			once: (signal: NodeJS.Signals, listener: () => void) => listeners.set(signal, listener),
			off: (signal: NodeJS.Signals, listener: () => void) => {
				if (listeners.get(signal) === listener) listeners.delete(signal);
			}
		};
		const signals = controlledTerminationSignals(source);
		const order: string[] = [];
		const coordinated = coordinateControlledRun(
			async () => new Promise<never>(() => undefined),
			async () => {
				await Promise.resolve();
				order.push('cleanup');
			},
			signals.interruption
		).catch((error: unknown) => {
			order.push('rejected');
			throw error;
		});
		listeners.get('SIGINT')?.();
		await expect(coordinated).rejects.toThrow(/interrupted/);
		expect(order).toEqual(['cleanup', 'rejected']);
		signals.dispose();
		expect(listeners.size).toBe(0);
	});
});
