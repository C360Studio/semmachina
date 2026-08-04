const MANIFEST_KEYS = [
	'campaign_id',
	'diagnostics_url',
	'graphql_url',
	'player_websocket_url',
	'status',
	'world_prefix'
];
const POLL_MS = 30_000;
const ABSOLUTE_MS = 180_000;
const ATTEMPT_MS = 3_000;
const RETRY_MS = 250;
const UNAVAILABLE_MS = 10_000;
const PHASE_BUDGET_MS = new Map([
	['accepted', 60_000],
	['resolving', 60_000],
	['applying', 60_000],
	['interpreting', 120_000],
	['adjudicating', 120_000],
	['companion', 120_000],
	['narrating', 150_000]
]);
const TURN_PHASES = new Set([
	'accepted',
	'interpreting',
	'adjudicating',
	'resolving',
	'applying',
	'companion',
	'narrating',
	'complete',
	'failed'
]);
const CASE_PHASES = new Set([
	'cold_open',
	'discovery',
	'investigation',
	'accusation',
	'denouement'
]);
const FAILURE_REASONS = new Set([
	'effect-invalid',
	'effect-entity-missing',
	'effect-entity-kind',
	'effect-commit-incomplete',
	'persona-cap-exhausted',
	'persona-loop-failed',
	'turn-stranded',
	'knowledge-unauthorized',
	'accusation-invalid',
	'case-progress-invalid'
]);
const FAILURE_CLASSES = new Set([
	'provider-model',
	'model-output-limit',
	'agent-runtime',
	'agent-limit',
	'deterministic',
	'unknown'
]);
const AUTHORIZATION_REASONS = new Set([
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
]);
const DETERMINISTIC_FAILURE_CLASSES = new Set(['deterministic', 'unknown']);
const FAILURE_CLASSES_BY_REASON = new Map([
	['effect-invalid', DETERMINISTIC_FAILURE_CLASSES],
	['effect-entity-missing', DETERMINISTIC_FAILURE_CLASSES],
	['effect-entity-kind', DETERMINISTIC_FAILURE_CLASSES],
	['effect-commit-incomplete', DETERMINISTIC_FAILURE_CLASSES],
	['persona-cap-exhausted', new Set(['agent-limit', 'unknown'])],
	[
		'persona-loop-failed',
		new Set(['provider-model', 'model-output-limit', 'agent-runtime', 'unknown'])
	],
	['turn-stranded', DETERMINISTIC_FAILURE_CLASSES],
	['knowledge-unauthorized', DETERMINISTIC_FAILURE_CLASSES],
	['accusation-invalid', DETERMINISTIC_FAILURE_CLASSES],
	['case-progress-invalid', DETERMINISTIC_FAILURE_CLASSES]
]);
const SURFACE_CHECKPOINTS = new Set([
	'runner_started',
	'build_complete',
	'stack_ready',
	'diagnostic_ready',
	'surface_ready',
	'proxy_ready',
	'browser_tests_started',
	'unauthorized_world_verified',
	'surface_document_loaded',
	'login_submitted',
	'login_http_verified',
	'action_controls_visible',
	'schematic_world_visible',
	'clock_visible',
	'pre_action_ready',
	'interrupt_cleanup_ready',
	'retrieval_sent',
	'retrieval_answered',
	'first_action_submit_started',
	'first_action_accepted',
	'first_turn_complete',
	'second_action_submit_started',
	'second_action_accepted',
	'second_turn_complete',
	'browser_tests_complete'
]);

export const DEMO_STARTUP_LIMIT_MS = 180_000;
export const DEMO_PRESENTER_MARKER = '[surface-demo-bootstrap] presenter_ready';
export const DEMO_BOOTSTRAP_STAGES = Object.freeze([
	'started',
	'document_loaded',
	'login_complete',
	'world_ready',
	'audit_detached',
	'marker_written',
	'close_ack_received'
]);
const DEMO_BOOTSTRAP_STAGE_SET = new Set(DEMO_BOOTSTRAP_STAGES);
export const DEMO_PRE_NAVIGATION_FAILURE_CATEGORIES = Object.freeze([
	'credential_invalid',
	'world_scope_invalid',
	'close_signal_invalid'
]);
const DEMO_PRE_NAVIGATION_FAILURE_CATEGORY_SET = new Set(DEMO_PRE_NAVIGATION_FAILURE_CATEGORIES);
export const DEMO_NAVIGATION_FAILURE_CATEGORIES = Object.freeze([
	'navigation_timeout',
	'connection_refused',
	'tls_failure',
	'page_closed',
	'http_status',
	'other'
]);
const DEMO_NAVIGATION_FAILURE_CATEGORY_SET = new Set(DEMO_NAVIGATION_FAILURE_CATEGORIES);
const DEMO_READY_LINES = Object.freeze([
	'[surface-demo] presenter_ready',
	'URL: https://127.0.0.1:4181',
	'Login: the browser opens and authenticates automatically; see README.md.',
	'Each submitted action may incur Gemini API charges.',
	'Close the browser or press Ctrl-C to stop.'
]);

/** @param {string} line */
export function parseDemoBootstrapDiagnostic(line) {
	const normalized = line.trim();
	if (normalized === DEMO_PRESENTER_MARKER) return Object.freeze({ kind: 'ready' });
	const preNavigation = normalized.match(
		/^\[surface-demo-bootstrap\] pre_navigation_failed:([a-z_]+)$/
	);
	if (preNavigation !== null && DEMO_PRE_NAVIGATION_FAILURE_CATEGORY_SET.has(preNavigation[1])) {
		return Object.freeze({ kind: 'pre_navigation_failed', category: preNavigation[1] });
	}
	const navigation = normalized.match(/^\[surface-demo-bootstrap\] navigation_failed:([a-z_]+)$/);
	if (navigation !== null && DEMO_NAVIGATION_FAILURE_CATEGORY_SET.has(navigation[1])) {
		return Object.freeze({ kind: 'navigation_failed', category: navigation[1] });
	}
	const match = normalized.match(/^\[surface-demo-bootstrap\] (progress|failed):([a-z_]+)$/);
	if (match === null || !DEMO_BOOTSTRAP_STAGE_SET.has(match[2])) return undefined;
	return Object.freeze({ kind: match[1], stage: match[2] });
}

/**
 * @param {unknown} error
 * @returns {'navigation_timeout' | 'connection_refused' | 'tls_failure' | 'page_closed' | 'other'}
 */
export function classifyDemoNavigationFailure(error) {
	const name =
		error !== null && typeof error === 'object' && 'name' in error && typeof error.name === 'string'
			? error.name
			: '';
	const message =
		error !== null &&
		typeof error === 'object' &&
		'message' in error &&
		typeof error.message === 'string'
			? error.message
			: '';
	const signature = `${name}\n${message}`;
	if (name === 'TimeoutError' || /timeout/i.test(signature)) return 'navigation_timeout';
	if (/ERR_CONNECTION_REFUSED|ECONNREFUSED/i.test(signature)) return 'connection_refused';
	if (/ERR_CERT|ERR_SSL|\bTLS\b|certificate/i.test(signature)) return 'tls_failure';
	if (/Target page.*closed|page.*closed|browser.*closed|context.*closed/i.test(signature)) {
		return 'page_closed';
	}
	return 'other';
}

export class DemoWorkerAuthorityError extends Error {
	/** @param {'credential_invalid' | 'close_signal_invalid'} category */
	constructor(category) {
		super(`demo worker authority ${category}`);
		this.category = category;
	}
}

/**
 * @template T
 * @param {Record<string, string | undefined>} environment
 * @param {(authority: {credential: string, closeSignalPath: string | undefined}) => Promise<T>} use
 */
export async function runDemoWorkerAuthorityFixture(environment, use) {
	const credential = environment.REAL_SURFACE_DEMO_CREDENTIAL;
	const closeSignalPath = environment.REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH;
	delete environment.REAL_SURFACE_DEMO_CREDENTIAL;
	delete environment.REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH;
	if (credential === undefined || !/^[A-Za-z0-9_-]{43}$/.test(credential)) {
		throw new DemoWorkerAuthorityError('credential_invalid');
	}
	if (
		closeSignalPath !== undefined &&
		(!nodePath.isAbsolute(closeSignalPath) || nodePath.basename(closeSignalPath) !== 'close.signal')
	) {
		throw new DemoWorkerAuthorityError('close_signal_invalid');
	}
	const authority = { credential, closeSignalPath };
	try {
		return await use(authority);
	} finally {
		authority.credential = '';
		authority.closeSignalPath = undefined;
	}
}

/** @param {() => Promise<boolean>} consume */
export function createSerializedDemoCloseSignalCheck(consume) {
	let requested = false;
	/** @type {Promise<boolean> | undefined} */
	let active;
	return () => {
		requested = true;
		if (active !== undefined) return active;
		active = (async () => {
			let consumed = false;
			while (requested && !consumed) {
				requested = false;
				consumed = await consume();
			}
			return consumed;
		})().finally(() => {
			active = undefined;
		});
		return active;
	};
}

/** @param {readonly string[]} arguments_ */
export function parseSurfaceRunnerArguments(arguments_) {
	if (arguments_.length === 0) return Object.freeze({ mode: 'paid' });
	if (arguments_.length === 1 && arguments_[0] === '--preflight') {
		return Object.freeze({ mode: 'preflight', interruptProof: false });
	}
	if (
		arguments_.length === 2 &&
		arguments_[0] === '--preflight' &&
		arguments_[1] === '--interrupt-proof'
	) {
		return Object.freeze({ mode: 'preflight', interruptProof: true });
	}
	if (arguments_.length === 1 && arguments_[0] === '--demo') {
		return Object.freeze({ mode: 'demo' });
	}
	throw new Error('unsupported runner arguments');
}

/** @param {(size: number) => Buffer} [generate] */
export function createDemoCreatorCredential(generate = randomBytes) {
	const bytes = generate(32);
	if (!Buffer.isBuffer(bytes) || bytes.byteLength !== 32) {
		throw new Error('demo credential generation failed');
	}
	return bytes.toString('base64url');
}

/**
 * @param {{temporaryRoot?: string, mkdtemp?: (prefix: string) => Promise<string>,
 * chmod?: (path: string, mode: number) => Promise<void>,
 * remove?: (path: string, options: {force: boolean, recursive: boolean}) => Promise<void>}} [dependencies]
 */
export async function createDemoCloseSignal(dependencies = {}) {
	const root = dependencies.temporaryRoot ?? tmpdir();
	const makeDirectory = dependencies.mkdtemp ?? mkdtemp;
	const setMode = dependencies.chmod ?? chmod;
	const remove = dependencies.remove ?? rm;
	let directory;
	try {
		directory = await makeDirectory(nodePath.join(root, 'semmachina-demo-close-'));
		await setMode(directory, 0o700);
		return Object.freeze({ directory, path: nodePath.join(directory, 'close.signal') });
	} catch (error) {
		if (directory !== undefined) {
			await remove(directory, { force: true, recursive: true });
		}
		throw error;
	}
}

/**
 * @param {{directory: string, path: string}} signal
 * @param {{writeFile?: (path: string, content: string, options: {encoding: 'utf8', flag: 'wx',
 * mode: number}) => Promise<void>, rename?: (from: string, to: string) => Promise<void>}} [dependencies]
 */
export async function publishDemoCloseSignal(signal, dependencies = {}) {
	const write = dependencies.writeFile ?? writeFile;
	const move = dependencies.rename ?? rename;
	const pending = `${signal.path}.pending`;
	await write(pending, 'close\n', { encoding: 'utf8', flag: 'wx', mode: 0o600 });
	await move(pending, signal.path);
}

/**
 * @param {{directory: string, path: string} | undefined} signal
 * @param {{remove?: (path: string, options: {force: boolean, recursive: boolean}) => Promise<void>}} [dependencies]
 */
export async function cleanupDemoCloseSignal(signal, dependencies = {}) {
	if (signal === undefined) return;
	await (dependencies.remove ?? rm)(signal.directory, { force: true, recursive: true });
}

/** @param {string} secret @param {readonly string[]} candidates */
export function requireDemoSecretAbsent(secret, candidates) {
	if (candidates.some((candidate) => candidate.includes(secret))) {
		throw new Error('surface demo secret boundary failed');
	}
}

/** @param {(line: string) => void} [write] */
export function emitDemoReady(write = console.log) {
	for (const line of DEMO_READY_LINES) write(line);
}

/** @param {() => number} [now] */
export function createDemoStartupDeadline(now = Date.now) {
	return now() + DEMO_STARTUP_LIMIT_MS;
}

/**
 * @template T
 * @param {Promise<T>} stage
 * @param {number} deadline
 * @param {{now?: () => number, setTimer?: typeof setTimeout, clearTimer?: typeof clearTimeout}} [dependencies]
 */
export async function withinDemoStartupDeadline(stage, deadline, dependencies = {}) {
	const now = dependencies.now ?? Date.now;
	const setTimer = dependencies.setTimer ?? setTimeout;
	const clearTimer = dependencies.clearTimer ?? clearTimeout;
	const remaining = deadline - now();
	if (!Number.isFinite(deadline) || remaining <= 0 || remaining > DEMO_STARTUP_LIMIT_MS) {
		throw new Error('surface demo startup timed out');
	}
	let timer;
	const timeout = new Promise((_, reject) => {
		timer = setTimer(() => reject(new Error('surface demo startup timed out')), remaining);
		timer.unref?.();
	});
	try {
		return await Promise.race([stage, timeout]);
	} finally {
		clearTimer(timer);
	}
}

/**
 * @param {Promise<void>} presenterReady
 * @param {Promise<{code: number | null, signal: NodeJS.Signals | null}>} browserExit
 * @param {(line: string) => void} [write]
 * @param {() => Promise<void>} [afterReady]
 */
export async function holdDemoPresentation(
	presenterReady,
	browserExit,
	write = console.log,
	afterReady = async () => undefined
) {
	const first = await Promise.race([
		presenterReady.then(() => ({ kind: 'ready' })),
		browserExit.then((exit) => ({ kind: 'exit', exit }))
	]);
	if (first.kind === 'exit') throw new Error('demo browser exited before presenter readiness');
	emitDemoReady(write);
	await afterReady();
	const exit = await browserExit;
	if (exit.code !== 0 || exit.signal !== null) throw new Error('demo browser exited unexpectedly');
}

export const DEFAULT_BROWSER_TEST_MATCH = '**/*.e2e.{ts,js}';
export const REAL_BROWSER_TEST_MATCH = '**/*.real.e2e.ts';
export const REAL_BROWSER_TEST_IGNORE = '**/*.real.e2e.ts';

const BROWSER_AUDIT_ERRORS = Object.freeze({
	request_authority: 'paid browser request authority audit failed',
	websocket_authority: 'paid browser WebSocket authority audit failed',
	protocol: 'paid browser protocol audit failed',
	browser_state: 'paid browser state audit failed'
});

/** @param {boolean} condition @param {keyof typeof BROWSER_AUDIT_ERRORS} kind */
export function requireSafeBrowserAudit(condition, kind) {
	if (!condition) throw new Error(BROWSER_AUDIT_ERRORS[kind]);
}

/**
 * @param {string} secret
 * @param {readonly string[]} candidates
 * @param {keyof typeof BROWSER_AUDIT_ERRORS} kind
 */
export function requireSecretAbsent(secret, candidates, kind) {
	requireSafeBrowserAudit(
		candidates.every((candidate) => !candidate.includes(secret)),
		kind
	);
}

/** @param {readonly unknown[]} protocolFailures @param {number} acceptedCount */
export function requireSafeProtocolPoll(protocolFailures, acceptedCount) {
	requireSafeBrowserAudit(protocolFailures.length === 0, 'protocol');
	return acceptedCount;
}

/** @param {string} label @param {(line: string) => void} [write] */
export function emitSurfaceCheckpoint(label, write = console.log) {
	if (!SURFACE_CHECKPOINTS.has(label)) throw new Error('unknown surface checkpoint');
	write(`[surface-checkpoint] ${label}`);
}

/** @param {string} filename */
export function isDefaultBrowserTest(filename) {
	return /\.e2e\.(?:ts|js)$/.test(filename) && !/\.real\.e2e\.ts$/.test(filename);
}

/** @param {string} filename */
export function isRealBrowserTest(filename) {
	return /\.real\.e2e\.ts$/.test(filename);
}

/**
 * @param {{status: () => number} | null | undefined} response
 * @param {number} expected
 * @param {string} label
 */
export function requireExactHTTPStatus(response, expected, label) {
	if (response === null || response === undefined || response.status() !== expected) {
		throw new Error(`${label} returned unexpected HTTP status`);
	}
}

/**
 * @typedef {{status: 'ready', player_websocket_url: string, graphql_url: string,
 * diagnostics_url: string, world_prefix: string, campaign_id: string}} StackManifest
 * @typedef {'effect-invalid' | 'effect-entity-missing' | 'effect-entity-kind' |
 * 'effect-commit-incomplete' | 'persona-cap-exhausted' | 'persona-loop-failed' |
 * 'turn-stranded' | 'knowledge-unauthorized' | 'accusation-invalid' |
 * 'case-progress-invalid'} DiagnosticFailureReason
 * @typedef {'provider-model' | 'model-output-limit' | 'agent-runtime' | 'agent-limit' |
 * 'deterministic' | 'unknown'} DiagnosticFailureClass
 * @typedef {'wrong-turn' | 'wrong-case' | 'wrong-actor' | 'invalid-target' |
 * 'ineligible-reveal' | 'ineligible-phase' | 'solution-locked' | 'question-target-mismatch' |
 * 'share-source-unknown' | 'share-target-unauthorized' | 'witness-unauthorized' |
 * 'unsupported-kind'} DiagnosticAuthorizationReason
 * @typedef {{reason: DiagnosticFailureReason, class: DiagnosticFailureClass,
 * authorization_reason: DiagnosticAuthorizationReason | null}} DiagnosticFailure
 * @typedef {{turn_id: string, phase: string, phase_recorded_at: string, case_phase: string,
 * kit_hint_proof: {proved: boolean, case_decision_kind?: string, trigger_kind?: string,
 * trigger_source?: string}, failure: DiagnosticFailure | null}} DiagnosticSnapshot
 * @typedef {{label: 'observer_retry' | 'authoritative_progress', elapsed_ms: number,
 * failure_count: number}} MonitorTelemetry
 * @typedef {{read?: (url: string, timeoutMs: number) => Promise<unknown>,
 * sleep?: (milliseconds: number) => Promise<void>, now?: () => number,
 * emit?: (entry: MonitorTelemetry) => void}} MonitorDependencies
 */

/** @param {unknown} value @param {string} expected @returns {string} */
function exactURL(value, expected) {
	if (typeof value !== 'string' || value !== expected)
		throw new Error(`unsafe stack URL: ${value}`);
	return value;
}

/** @param {string} line @returns {Readonly<StackManifest>} */
export function parseStackManifest(line) {
	/** @type {unknown} */
	let value;
	try {
		value = JSON.parse(line);
	} catch {
		throw new Error('stack manifest is not JSON');
	}
	if (value === null || typeof value !== 'object' || Array.isArray(value)) {
		throw new Error('stack manifest must be an object');
	}
	const record = /** @type {Record<string, unknown>} */ (value);
	const keys = Object.keys(record).sort();
	if (JSON.stringify(keys) !== JSON.stringify(MANIFEST_KEYS)) {
		throw new Error('stack manifest has an unexpected shape');
	}
	if (record.status !== 'ready') throw new Error('stack manifest is not ready');
	exactURL(record.player_websocket_url, 'ws://127.0.0.1:43101/play');
	exactURL(record.graphql_url, 'http://127.0.0.1:43102/graphql');
	exactURL(record.diagnostics_url, 'http://127.0.0.1:43103');
	if (typeof record.campaign_id !== 'string' || !/^[A-Za-z0-9._:-]+$/.test(record.campaign_id)) {
		throw new Error('invalid campaign identity');
	}
	if (
		typeof record.world_prefix !== 'string' ||
		!/^c360\.semmachina\.[a-z0-9][a-z0-9-]*\.bellweather-maze$/.test(record.world_prefix)
	) {
		throw new Error('invalid Bellweather world prefix');
	}
	return Object.freeze(/** @type {StackManifest} */ ({ ...record }));
}

/** @param {StackManifest} manifest */
export function buildSurfaceEnvironment(manifest) {
	const parts = manifest.world_prefix.split('.');
	return Object.freeze({
		REAL_SURFACE_CAMPAIGN_ID: manifest.campaign_id,
		REAL_SURFACE_DIAGNOSTICS_URL: manifest.diagnostics_url,
		REAL_SURFACE_GRAPHQL_URL: manifest.graphql_url,
		REAL_SURFACE_PLAYER_ID: `${manifest.world_prefix}.player.rowan`,
		REAL_SURFACE_PLAYER_WS_URL: manifest.player_websocket_url,
		REAL_SURFACE_WORLD_NAMESPACE: parts[2],
		REAL_SURFACE_WORLD_PREFIX: manifest.world_prefix,
		REAL_SURFACE_WORLD_TEMPLATE: parts[3]
	});
}

/** @param {StackManifest} manifest */
export function buildWebServerEnvironment(manifest) {
	const surface = buildSurfaceEnvironment(manifest);
	return Object.freeze({
		HOST: '127.0.0.1',
		PORT: '4173',
		ORIGIN: 'https://127.0.0.1:4181',
		SEMMACHINA_GRAPHQL_URL: surface.REAL_SURFACE_GRAPHQL_URL,
		SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
		SEMMACHINA_WORLD_ORG: 'c360',
		SEMMACHINA_WORLD_NAMESPACE: surface.REAL_SURFACE_WORLD_NAMESPACE,
		SEMMACHINA_WORLD_TEMPLATE: surface.REAL_SURFACE_WORLD_TEMPLATE,
		SEMMACHINA_PUBLIC_ORIGIN: 'https://127.0.0.1:4181',
		SEMMACHINA_TLS_POSTURE: 'trusted_loopback_proxy',
		SEMMACHINA_CREATOR_CREDENTIAL: 'bellweather-surface-creator-secret',
		SEMMACHINA_PLAYER_BEARER: 'CHANGE-ME-bellweather-local-only-bearer',
		SEMMACHINA_PLAYER_WS_URL: surface.REAL_SURFACE_PLAYER_WS_URL,
		SEMMACHINA_PLAYER_ID: surface.REAL_SURFACE_PLAYER_ID
	});
}

/** @param {StackManifest} manifest @param {string} creatorCredential */
export function buildDemoWebServerEnvironment(manifest, creatorCredential) {
	if (!/^[A-Za-z0-9_-]{43}$/.test(creatorCredential)) {
		throw new Error('invalid demo creator credential');
	}
	return Object.freeze({
		...buildWebServerEnvironment(manifest),
		SEMMACHINA_CREATOR_CREDENTIAL: creatorCredential
	});
}

/** @param {StackManifest} manifest */
export function buildBrowserTestEnvironment(manifest) {
	const surface = buildSurfaceEnvironment(manifest);
	return Object.freeze({
		REAL_SURFACE_CAMPAIGN_ID: surface.REAL_SURFACE_CAMPAIGN_ID,
		REAL_SURFACE_DIAGNOSTICS_URL: surface.REAL_SURFACE_DIAGNOSTICS_URL,
		REAL_SURFACE_WORLD_PREFIX: surface.REAL_SURFACE_WORLD_PREFIX
	});
}

/** @param {StackManifest} manifest @param {string} creatorCredential */
export function buildDemoBrowserEnvironment(manifest, creatorCredential) {
	if (!/^[A-Za-z0-9_-]{43}$/.test(creatorCredential)) {
		throw new Error('invalid demo creator credential');
	}
	return Object.freeze({
		REAL_SURFACE_WORLD_PREFIX: manifest.world_prefix,
		REAL_SURFACE_DEMO_CREDENTIAL: creatorCredential
	});
}

/** @param {Record<string, string | undefined>} environment */
export function sanitizeBrowserEnvironment(environment) {
	const clean = { ...environment };
	for (const key of Object.keys(clean)) {
		if (
			key === 'GEMINI_API_KEY' ||
			key === 'SEMMACHINA_PAID_SMOKE' ||
			key === 'SEMMACHINA_DEMO_AUTO_CLOSE_PROOF' ||
			key.startsWith('REAL_SURFACE_') ||
			key.startsWith('SEMMACHINA_GRAPHQL_') ||
			key.startsWith('SEMMACHINA_PLAYER_') ||
			key.startsWith('SEMMACHINA_WORLD_') ||
			key === 'SEMMACHINA_PUBLIC_ORIGIN' ||
			key === 'SEMMACHINA_TLS_POSTURE' ||
			key === 'SEMMACHINA_CREATOR_CREDENTIAL'
		) {
			delete clean[key];
		}
	}
	return clean;
}

/** @param {Record<string, string | undefined>} environment @param {string} geminiKey */
export function buildGoEnvironment(environment, geminiKey) {
	if (typeof geminiKey !== 'string' || geminiKey.trim() === '') {
		throw new Error('GEMINI_API_KEY is empty');
	}
	return {
		...sanitizeBrowserEnvironment(environment),
		GEMINI_API_KEY: geminiKey,
		SEMMACHINA_PAID_SMOKE: '1'
	};
}

/** @param {Record<string, string | undefined>} environment @returns {string} */
export function requirePaidRunnerEnvironment(environment) {
	if (environment.SEMMACHINA_PAID_SMOKE !== '1') {
		throw new Error('paid smoke is disabled; set SEMMACHINA_PAID_SMOKE=1 explicitly');
	}
	const key = environment.GEMINI_API_KEY;
	if (key === undefined || key.trim() === '') throw new Error('GEMINI_API_KEY is empty');
	return key;
}

/** @param {string} [platform] */
export function useDetachedProcessGroup(platform = process.platform) {
	return platform !== 'win32';
}

/** @param {string} [platform] */
export function requireProcessGroupSupport(platform = process.platform) {
	if (platform === 'win32') {
		throw new Error('paid surface acceptance requires POSIX process-group teardown');
	}
}

/**
 * @param {{pid?: number, kill: (signal?: NodeJS.Signals) => boolean}} child
 * @param {NodeJS.Signals} signal
 * @param {{platform?: string, kill?: (pid: number, signal: NodeJS.Signals) => void}} [dependencies]
 */
export function signalProcessGroup(child, signal, dependencies = {}) {
	const platform = dependencies.platform ?? process.platform;
	requireProcessGroupSupport(platform);
	if (child.pid === undefined || child.pid <= 0)
		throw new Error('child has no process-group leader');
	(dependencies.kill ?? process.kill)(-child.pid, signal);
}

/** @param {number} pid @param {(pid: number, signal: 0) => void} [kill] */
export function processGroupExists(pid, kill = process.kill) {
	try {
		kill(-pid, 0);
		return true;
	} catch (error) {
		if (error !== null && typeof error === 'object' && 'code' in error && error.code === 'ESRCH') {
			return false;
		}
		throw error;
	}
}

/**
 * @param {{pid?: number, exitCode: number | null, signalCode: NodeJS.Signals | null,
 * kill: (signal?: NodeJS.Signals) => boolean, once: Function, off: Function}} child
 * @param {number} timeoutMs
 */
async function waitForLeaderExit(child, timeoutMs) {
	if (child.exitCode !== null || child.signalCode !== null) return true;
	return new Promise((resolve) => {
		const exited = () => {
			clearTimeout(timer);
			resolve(true);
		};
		const timer = setTimeout(() => {
			child.off('exit', exited);
			resolve(false);
		}, timeoutMs);
		child.once('exit', exited);
	});
}

/** @param {number} pid @param {number} timeoutMs */
async function waitForProcessGroupExit(pid, timeoutMs) {
	const deadline = Date.now() + timeoutMs;
	while (processGroupExists(pid)) {
		if (Date.now() >= deadline) return false;
		await new Promise((resolve) => setTimeout(resolve, 25));
	}
	return true;
}

/**
 * @param {{pid?: number, exitCode: number | null, signalCode: NodeJS.Signals | null,
 * kill: (signal?: NodeJS.Signals) => boolean, once: Function, off: Function}} child
 * @param {{platform?: string, kill?: (pid: number, signal: NodeJS.Signals) => void,
 * waitForExit?: (child: object, timeoutMs: number) => Promise<boolean>,
 * groupExists?: (pid: number) => boolean,
 * waitForGroupExit?: (pid: number, timeoutMs: number) => Promise<boolean>,
 * leaderWasAlreadyReaped?: boolean}} [dependencies]
 */
export async function terminateProcessGroup(child, dependencies = {}) {
	requireProcessGroupSupport(dependencies.platform ?? process.platform);
	if (child.pid === undefined || child.pid <= 0)
		throw new Error('child has no process-group leader');
	const waitForExit = dependencies.waitForExit ?? waitForLeaderExit;
	const groupExists = dependencies.groupExists ?? processGroupExists;
	const waitForGroupExit = dependencies.waitForGroupExit ?? waitForProcessGroupExit;
	const leaderWasAlreadyReaped =
		dependencies.leaderWasAlreadyReaped ?? (child.exitCode !== null || child.signalCode !== null);
	let groupIsPresent;
	try {
		groupIsPresent = groupExists(child.pid);
	} catch (error) {
		if (
			leaderWasAlreadyReaped &&
			error !== null &&
			typeof error === 'object' &&
			'code' in error &&
			error.code === 'EPERM'
		) {
			// macOS can retain a non-signalable group identity briefly after its
			// direct leader is reaped; there is no live owned leader to signal.
			return;
		}
		throw error;
	}
	if (!groupIsPresent) {
		if (!(await waitForExit(child, 5_000)))
			throw new Error('child process-group leader was not reaped');
		return;
	}

	signalProcessGroup(child, 'SIGTERM', dependencies);
	const leaderExitedAfterTerm = await waitForExit(child, 5_000);
	const groupExitedAfterTerm = await waitForGroupExit(child.pid, 5_000);
	if (leaderExitedAfterTerm && groupExitedAfterTerm) return;

	if (groupExists(child.pid)) signalProcessGroup(child, 'SIGKILL', dependencies);
	const leaderExitedAfterKill = await waitForExit(child, 5_000);
	const groupExitedAfterKill = await waitForGroupExit(child.pid, 5_000);
	if (!leaderExitedAfterKill || !groupExitedAfterKill) {
		throw new Error('paid child process group survived SIGKILL or its leader was not reaped');
	}
}

/**
 * @param {string} name
 * @param {string} label
 * @param {{pid?: number, exitCode: number | null, signalCode: NodeJS.Signals | null,
 * kill: (signal?: NodeJS.Signals) => boolean, once: Function, off: Function}} child
 */
export function watchOwnedProcess(name, label, child) {
	/** @type {(exit: {code: number | null, signal: NodeJS.Signals | null}) => void} */
	let resolveExit;
	const exit = new Promise((resolve) => {
		resolveExit = resolve;
	});
	const state = { name, label, child, leaderWasAlreadyReaped: false, exit };
	/** @param {number | null} code @param {NodeJS.Signals | null} signal */
	const recordExit = (code, signal) => {
		state.leaderWasAlreadyReaped = true;
		resolveExit({ code, signal });
	};
	if (child.exitCode !== null || child.signalCode !== null) {
		recordExit(child.exitCode, child.signalCode);
	} else {
		child.once('exit', recordExit);
	}
	return state;
}

/**
 * @template T
 * @param {Promise<T>} stage
 * @param {readonly {label: string,
 * exit: Promise<{code: number | null, signal: NodeJS.Signals | null}>}[]} supervised
 * @returns {Promise<T>}
 */
export function superviseOwnedStage(stage, supervised) {
	return Promise.race([
		stage,
		...supervised.map(({ label, exit }) =>
			exit.then(({ code, signal }) => {
				throw new Error(`${label} exited early (${code ?? signal})`);
			})
		)
	]);
}

/**
 * @param {readonly {name: string, child: object, leaderWasAlreadyReaped: boolean}[]} children
 * @param {(child: object, dependencies: {leaderWasAlreadyReaped: boolean}, name: string) => Promise<void>} [terminate]
 */
export async function cleanupOwnedProcessGroups(children, terminate) {
	const terminateOwned =
		terminate ??
		((child, dependencies) =>
			terminateProcessGroup(
				/** @type {{pid?: number, exitCode: number | null, signalCode: NodeJS.Signals | null,
				kill: (signal?: NodeJS.Signals) => boolean, once: Function, off: Function}} */ (child),
				dependencies
			));
	const results = await Promise.allSettled(
		children.map(({ name, child, leaderWasAlreadyReaped }) =>
			terminateOwned(child, { leaderWasAlreadyReaped }, name)
		)
	);
	const failures = results
		.filter((result) => result.status === 'rejected')
		.map((result) => result.reason);
	if (failures.length !== 0) throw new AggregateError(failures, 'surface cleanup failed');
}

export function stackCommandArguments() {
	return Object.freeze([
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
}

export function surfaceServerCommandArguments() {
	return Object.freeze(['.server-build/server.js']);
}

export function tlsProxyCommandArguments() {
	return Object.freeze(['tests/loopback-https-proxy.mjs']);
}

/**
 * @param {Record<string, unknown>} record
 * @param {readonly string[]} expected
 * @param {string} label
 */
function exactKeys(record, expected, label) {
	if (JSON.stringify(Object.keys(record).sort()) !== JSON.stringify([...expected].sort())) {
		throw new Error(`${label} has an unexpected shape`);
	}
}

/** @param {unknown} value @returns {value is string} */
function isExactRFC3339(value) {
	if (typeof value !== 'string') return false;
	const match = value.match(
		/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|([+-])(\d{2}):(\d{2}))$/
	);
	if (match === null) return false;
	const year = Number(match[1]);
	const month = Number(match[2]);
	const day = Number(match[3]);
	const hour = Number(match[4]);
	const minute = Number(match[5]);
	const second = Number(match[6]);
	const offsetHour = match[8] === undefined ? 0 : Number(match[8]);
	const offsetMinute = match[9] === undefined ? 0 : Number(match[9]);
	const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
	const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
	return (
		year > 0 &&
		month >= 1 &&
		month <= 12 &&
		day >= 1 &&
		day <= days[month - 1] &&
		hour <= 23 &&
		minute <= 59 &&
		second <= 59 &&
		offsetHour <= 23 &&
		offsetMinute <= 59
	);
}

/** @param {unknown} value @param {string} requestedTurnID @returns {DiagnosticSnapshot} */
export function parseDiagnosticSnapshot(value, requestedTurnID) {
	if (value === null || typeof value !== 'object' || Array.isArray(value)) {
		throw new Error('diagnostic snapshot is not an object');
	}
	const record = /** @type {Record<string, unknown>} */ (value);
	exactKeys(
		record,
		['turn_id', 'phase', 'phase_recorded_at', 'case_phase', 'kit_hint_proof', 'failure'],
		'diagnostic snapshot'
	);
	if (
		record.turn_id !== requestedTurnID ||
		typeof record.phase !== 'string' ||
		!TURN_PHASES.has(record.phase) ||
		!isExactRFC3339(record.phase_recorded_at) ||
		typeof record.case_phase !== 'string' ||
		!CASE_PHASES.has(record.case_phase) ||
		record.kit_hint_proof === null ||
		typeof record.kit_hint_proof !== 'object' ||
		Array.isArray(record.kit_hint_proof)
	) {
		throw new Error('diagnostic snapshot has an invalid shape');
	}
	const proof = /** @type {Record<string, unknown>} */ (record.kit_hint_proof);
	if (proof.proved === false) {
		exactKeys(proof, ['proved'], 'kit_hint_proof');
	} else if (proof.proved === true) {
		exactKeys(
			proof,
			['proved', 'case_decision_kind', 'trigger_kind', 'trigger_source'],
			'kit_hint_proof'
		);
		if (
			proof.case_decision_kind !== 'request_hint' ||
			proof.trigger_kind !== 'player-hint' ||
			proof.trigger_source !== 'case-decision'
		) {
			throw new Error('kit_hint_proof does not prove the exact persisted hint route');
		}
	} else {
		throw new Error('kit_hint_proof.proved must be boolean');
	}
	if (record.phase === 'failed') {
		if (
			record.failure === null ||
			typeof record.failure !== 'object' ||
			Array.isArray(record.failure)
		) {
			throw new Error('diagnostic failure has an invalid shape');
		}
		const failure = /** @type {Record<string, unknown>} */ (record.failure);
		exactKeys(failure, ['reason', 'class', 'authorization_reason'], 'diagnostic failure');
		if (
			typeof failure.reason !== 'string' ||
			!FAILURE_REASONS.has(failure.reason) ||
			typeof failure.class !== 'string' ||
			!FAILURE_CLASSES.has(failure.class) ||
			(failure.authorization_reason !== null &&
				(typeof failure.authorization_reason !== 'string' ||
					!AUTHORIZATION_REASONS.has(failure.authorization_reason)))
		) {
			throw new Error('diagnostic failure has an invalid shape');
		}
		if (!FAILURE_CLASSES_BY_REASON.get(failure.reason)?.has(failure.class)) {
			throw new Error('diagnostic failure has an invalid reason and class combination');
		}
		if (
			failure.authorization_reason !== null &&
			(failure.reason !== 'knowledge-unauthorized' || failure.class !== 'deterministic')
		) {
			throw new Error('diagnostic failure has an invalid authorization reason combination');
		}
	} else if (record.failure !== null) {
		throw new Error('diagnostic failure is inconsistent with the turn phase');
	}
	return /** @type {DiagnosticSnapshot} */ (record);
}

class DiagnosticObserverFailure extends Error {
	/** @param {string} message @param {boolean} retryable @param {number} retryDelayMs */
	constructor(message, retryable, retryDelayMs = RETRY_MS) {
		super(message);
		this.retryable = retryable;
		this.retryDelayMs = retryDelayMs;
	}
}

/** @param {number} status @returns {'ok' | 'retryable' | 'terminal'} */
export function classifyDiagnosticHTTPStatus(status) {
	if (status === 200) return 'ok';
	if ([429, 502, 503, 504].includes(status)) return 'retryable';
	return 'terminal';
}

/**
 * @param {'transport' | 'rate_limited' | 'upstream_unavailable' | 'turn_not_materialized' |
 * 'terminal_transition' | 'terminal_http' | 'invalid_json'} kind
 */
export function diagnosticObserverFailure(kind) {
	switch (kind) {
		case 'transport':
			return new DiagnosticObserverFailure('diagnostic observer transport unavailable', true);
		case 'rate_limited':
			return new DiagnosticObserverFailure('diagnostic observer rate limited', true);
		case 'upstream_unavailable':
			return new DiagnosticObserverFailure('diagnostic observer upstream unavailable', true);
		case 'turn_not_materialized':
			return new DiagnosticObserverFailure(
				'diagnostic observer turn is not materialized',
				true,
				1_000
			);
		case 'terminal_transition':
			return new DiagnosticObserverFailure(
				'diagnostic observer returned a terminal 404 transition response',
				false
			);
		case 'terminal_http':
			return new DiagnosticObserverFailure(
				'diagnostic observer returned a terminal HTTP response',
				false
			);
		case 'invalid_json':
			return new DiagnosticObserverFailure('diagnostic observer returned invalid JSON', false);
	}
}

/** @param {unknown} value */
function isExactTurnNotMaterialized(value) {
	return (
		value !== null &&
		typeof value === 'object' &&
		!Array.isArray(value) &&
		Object.keys(value).length === 1 &&
		Object.keys(value)[0] === 'error' &&
		/** @type {Record<string, unknown>} */ (value).error === 'turn_not_materialized'
	);
}

/** @param {unknown} error */
function isTransportFailure(error) {
	return (
		error instanceof TypeError ||
		(error !== null &&
			typeof error === 'object' &&
			'name' in error &&
			(error.name === 'AbortError' || error.name === 'TimeoutError'))
	);
}

/** @param {string} url @param {number} timeoutMs @returns {Promise<unknown>} */
async function readDiagnostic(url, timeoutMs) {
	let response;
	try {
		response = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) });
	} catch (error) {
		if (isTransportFailure(error)) throw diagnosticObserverFailure('transport');
		throw error;
	}
	if (response.status === 404) {
		let transition;
		try {
			transition = await response.json();
		} catch (error) {
			if (isTransportFailure(error)) throw diagnosticObserverFailure('transport');
			throw diagnosticObserverFailure('terminal_transition');
		}
		if (
			isExactTurnNotMaterialized(transition) &&
			response.headers.get('retry-after') === '1' &&
			response.headers.get('cache-control') === 'no-store'
		) {
			throw diagnosticObserverFailure('turn_not_materialized');
		}
		throw diagnosticObserverFailure('terminal_transition');
	}
	const classification = classifyDiagnosticHTTPStatus(response.status);
	if (classification === 'retryable') {
		throw diagnosticObserverFailure(
			response.status === 429 ? 'rate_limited' : 'upstream_unavailable'
		);
	}
	if (classification === 'terminal') throw diagnosticObserverFailure('terminal_http');
	try {
		return await response.json();
	} catch (error) {
		if (isTransportFailure(error)) throw diagnosticObserverFailure('transport');
		throw diagnosticObserverFailure('invalid_json');
	}
}

/** @param {number} milliseconds @returns {Promise<void>} */
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

/**
 * @param {'surface_http' | 'tls_proxy'} label
 * @param {() => Promise<boolean>} probe
 * @param {{sleep?: (milliseconds: number) => Promise<void>, now?: () => number,
 * timeoutMs?: number, pollMs?: number}} [dependencies]
 */
export async function waitForReadiness(label, probe, dependencies = {}) {
	if (label !== 'surface_http' && label !== 'tls_proxy') {
		throw new Error('unknown readiness service');
	}
	const sleep = dependencies.sleep ?? wait;
	const now = dependencies.now ?? Date.now;
	const timeoutMs = dependencies.timeoutMs ?? 30_000;
	const pollMs = dependencies.pollMs ?? 100;
	const started = now();
	for (;;) {
		if (await probe()) return;
		if (now() - started >= timeoutMs) throw new Error(`${label} readiness timed out`);
		await sleep(pollMs);
	}
}

class DiagnosticReadinessFailure extends Error {
	/** @param {string} message @param {boolean} retryable @param {number} retryDelayMs */
	constructor(message, retryable, retryDelayMs = RETRY_MS) {
		super(message);
		this.retryable = retryable;
		this.retryDelayMs = retryDelayMs;
	}
}

/** @param {string | null} value */
function boundedRetryAfter(value) {
	if (value === null || !/^\d+$/.test(value)) return RETRY_MS;
	const seconds = Number(value);
	if (!Number.isSafeInteger(seconds)) return RETRY_MS;
	return Math.max(RETRY_MS, Math.min(seconds * 1_000, ATTEMPT_MS));
}

/** @param {unknown} error */
function isDiagnosticReadinessTransportFailure(error) {
	return (
		error instanceof TypeError ||
		(error !== null &&
			typeof error === 'object' &&
			'name' in error &&
			(error.name === 'AbortError' || error.name === 'TimeoutError'))
	);
}

/** @param {string} url @param {number} timeoutMs */
async function readDiagnosticReadiness(url, timeoutMs) {
	let response;
	try {
		response = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) });
	} catch (error) {
		if (isDiagnosticReadinessTransportFailure(error)) {
			throw new DiagnosticReadinessFailure('diagnostic readiness transport unavailable', true);
		}
		throw error;
	}
	if (response.status === 503 || response.status === 504) {
		throw new DiagnosticReadinessFailure(
			'diagnostic readiness upstream unavailable',
			true,
			boundedRetryAfter(response.headers.get('retry-after'))
		);
	}
	if (response.status !== 200) {
		throw new DiagnosticReadinessFailure('diagnostic readiness returned a terminal status', false);
	}
	let body;
	try {
		body = await response.json();
	} catch (error) {
		if (isDiagnosticReadinessTransportFailure(error)) {
			throw new DiagnosticReadinessFailure('diagnostic readiness transport unavailable', true);
		}
		throw new DiagnosticReadinessFailure('diagnostic readiness returned malformed JSON', false);
	}
	if (
		body === null ||
		typeof body !== 'object' ||
		Array.isArray(body) ||
		Object.keys(body).length !== 1 ||
		Object.keys(body)[0] !== 'ready' ||
		/** @type {Record<string, unknown>} */ (body).ready !== true
	) {
		throw new DiagnosticReadinessFailure('diagnostic readiness returned an invalid shape', false);
	}
}

/**
 * @param {string} diagnosticsURL
 * @param {{read?: (url: string, timeoutMs: number) => Promise<void>,
 * sleep?: (milliseconds: number) => Promise<void>, now?: () => number}} [dependencies]
 */
export async function waitForDiagnosticReadiness(diagnosticsURL, dependencies = {}) {
	const read = dependencies.read ?? readDiagnosticReadiness;
	const sleep = dependencies.sleep ?? wait;
	const now = dependencies.now ?? Date.now;
	const started = now();
	for (;;) {
		const currentTime = now();
		if (currentTime - started >= 30_000) {
			throw new Error('diagnostic readiness exceeded the 30 second deadline');
		}
		const timeoutMs = Math.max(1, Math.min(ATTEMPT_MS, 30_000 - (currentTime - started)));
		try {
			await read(`${diagnosticsURL}/ready`, timeoutMs);
			if (now() - started >= 30_000) {
				throw new Error('diagnostic readiness exceeded the 30 second deadline');
			}
			return;
		} catch (error) {
			if (!(error instanceof DiagnosticReadinessFailure) || !error.retryable) throw error;
			const failedAt = now();
			if (failedAt - started >= 30_000) {
				throw new Error('diagnostic readiness exceeded the 30 second deadline', { cause: error });
			}
			await sleep(Math.min(error.retryDelayMs, 30_000 - (failedAt - started)));
		}
	}
}

/** @template T @param {Promise<unknown>} readiness @param {() => T | Promise<T>} startDownstream */
export async function startAfterDiagnosticReadiness(readiness, startDownstream) {
	await readiness;
	return startDownstream();
}

/** @param {DiagnosticSnapshot} snapshot @returns {number} */
function diagnosticPhaseBudget(snapshot) {
	const budget = PHASE_BUDGET_MS.get(snapshot.phase);
	if (budget === undefined) throw new Error('diagnostic phase has no observation budget');
	return budget;
}

/** @param {DiagnosticSnapshot} snapshot @returns {Error} */
function diagnosticPhaseBudgetError(snapshot) {
	return new Error(
		`agentic phase observation budget exceeded (phase=${snapshot.phase} case_phase=${snapshot.case_phase})`
	);
}

/** @returns {Error} */
function paidTurnAbsoluteBudgetError() {
	return new Error('paid turn absolute budget exceeded');
}

/**
 * @param {string} diagnosticsURL
 * @param {string} turnID
 * @param {MonitorDependencies} dependencies
 * @returns {Promise<DiagnosticSnapshot>}
 */
export async function monitorTurn(diagnosticsURL, turnID, dependencies = {}) {
	if (!/^turn-[A-Za-z0-9_-]+$/.test(turnID)) throw new Error('invalid accepted turn identity');
	const read = dependencies.read ?? readDiagnostic;
	const sleep = dependencies.sleep ?? wait;
	const now = dependencies.now ?? Date.now;
	const emit =
		dependencies.emit ??
		((entry) =>
			console.log(
				`[surface-monitor] ${entry.label} elapsed_ms=${entry.elapsed_ms} failure_count=${entry.failure_count}`
			));
	const started = now();
	let previous;
	/** @type {DiagnosticSnapshot | undefined} */
	let lastSnapshot;
	/** @type {number | undefined} */
	let phaseRecordedAt;
	let unavailableStarted;
	let failureCount = 0;

	for (;;) {
		const currentTime = now();
		if (currentTime - started >= ABSOLUTE_MS) {
			throw paidTurnAbsoluteBudgetError();
		}
		if (unavailableStarted !== undefined && currentTime - unavailableStarted >= UNAVAILABLE_MS) {
			throw new Error(
				'diagnostic monitor reached 10 seconds of continuous observer unavailability'
			);
		}
		if (
			lastSnapshot !== undefined &&
			phaseRecordedAt !== undefined &&
			currentTime - phaseRecordedAt >= diagnosticPhaseBudget(lastSnapshot)
		) {
			throw diagnosticPhaseBudgetError(lastSnapshot);
		}
		const endpoint = `${diagnosticsURL}/turns/${encodeURIComponent(turnID)}`;
		const attemptStarted = currentTime;
		const attemptTimeout = Math.max(
			1,
			Math.min(
				ATTEMPT_MS,
				ABSOLUTE_MS - (currentTime - started),
				unavailableStarted === undefined
					? ATTEMPT_MS
					: UNAVAILABLE_MS - (currentTime - unavailableStarted),
				lastSnapshot === undefined || phaseRecordedAt === undefined
					? ATTEMPT_MS
					: diagnosticPhaseBudget(lastSnapshot) - (currentTime - phaseRecordedAt)
			)
		);
		let value;
		try {
			value = await read(endpoint, attemptTimeout);
		} catch (error) {
			if (!(error instanceof DiagnosticObserverFailure) || !error.retryable) throw error;
			const failedAt = now();
			if (unavailableStarted === undefined) unavailableStarted = attemptStarted;
			failureCount += 1;
			emit({
				label: 'observer_retry',
				elapsed_ms: failedAt - unavailableStarted,
				failure_count: failureCount
			});
			if (failedAt - started >= ABSOLUTE_MS) {
				throw paidTurnAbsoluteBudgetError();
			}
			if (failedAt - unavailableStarted >= UNAVAILABLE_MS) {
				throw new Error(
					'diagnostic monitor reached 10 seconds of continuous observer unavailability',
					{ cause: error }
				);
			}
			if (
				lastSnapshot !== undefined &&
				phaseRecordedAt !== undefined &&
				failedAt - phaseRecordedAt >= diagnosticPhaseBudget(lastSnapshot)
			) {
				throw diagnosticPhaseBudgetError(lastSnapshot);
			}
			const retryDelay = Math.min(
				error.retryDelayMs,
				ABSOLUTE_MS - (failedAt - started),
				UNAVAILABLE_MS - (failedAt - unavailableStarted),
				lastSnapshot === undefined || phaseRecordedAt === undefined
					? error.retryDelayMs
					: diagnosticPhaseBudget(lastSnapshot) - (failedAt - phaseRecordedAt)
			);
			await sleep(Math.max(1, retryDelay));
			continue;
		}
		const readAt = now();
		if (readAt - started >= ABSOLUTE_MS) {
			throw paidTurnAbsoluteBudgetError();
		}
		const current = parseDiagnosticSnapshot(value, turnID);
		unavailableStarted = undefined;
		failureCount = 0;
		if (current.phase === 'failed') {
			const failure = /** @type {DiagnosticFailure} */ (current.failure);
			throw new Error(
				`diagnostic monitor reached failed phase (reason=${failure.reason} class=${failure.class} authorization_reason=${failure.authorization_reason ?? 'null'})`
			);
		}
		if (current.phase === 'complete') return current;
		if (lastSnapshot === undefined || lastSnapshot.phase !== current.phase) {
			phaseRecordedAt = Math.min(Date.parse(current.phase_recorded_at), readAt);
		}
		const currentPhaseRecordedAt = /** @type {number} */ (phaseRecordedAt);
		lastSnapshot = current;
		if (readAt - currentPhaseRecordedAt >= diagnosticPhaseBudget(current)) {
			throw diagnosticPhaseBudgetError(current);
		}

		const signature = `${current.phase}|${current.phase_recorded_at}`;
		if (previous === undefined || previous !== signature) {
			previous = signature;
			emit({
				label: 'authoritative_progress',
				elapsed_ms: readAt - started,
				failure_count: 0
			});
		}
		await sleep(
			Math.max(
				1,
				Math.min(
					POLL_MS,
					ABSOLUTE_MS - (readAt - started),
					diagnosticPhaseBudget(current) - (readAt - currentPhaseRecordedAt)
				)
			)
		);
	}
}

/**
 * @template T
 * @param {Promise<T>} authoritativeMonitor
 * @param {() => Promise<void>} assertTerminal
 * @returns {Promise<T>}
 */
export async function assertTerminalAfterAuthoritativeCompletion(
	authoritativeMonitor,
	assertTerminal
) {
	const diagnostic = await authoritativeMonitor;
	await assertTerminal();
	return diagnostic;
}

/**
 * @template T
 * @param {() => Promise<T>} operation
 * @param {() => Promise<void>} cleanup
 * @param {Promise<never>} interruption
 * @returns {Promise<T>}
 */
export async function coordinateControlledRun(operation, cleanup, interruption) {
	/** @type {T | undefined} */
	let result;
	let operationFailure;
	try {
		result = await Promise.race([operation(), interruption]);
	} catch (error) {
		operationFailure = error;
	}
	let cleanupFailure;
	try {
		await cleanup();
	} catch (error) {
		cleanupFailure = error;
	}
	if (operationFailure !== undefined && cleanupFailure !== undefined) {
		throw new AggregateError(
			[operationFailure, cleanupFailure],
			'operation failed and cleanup also failed',
			{ cause: operationFailure }
		);
	}
	if (operationFailure !== undefined) throw operationFailure;
	if (cleanupFailure !== undefined) throw cleanupFailure;
	return /** @type {T} */ (result);
}

/**
 * @param {{once: (signal: NodeJS.Signals, listener: () => void) => unknown,
 * off: (signal: NodeJS.Signals, listener: () => void) => unknown}} source
 */
export function controlledTerminationSignals(source = process) {
	/** @type {(reason: Error) => void} */
	let rejectInterruption;
	let interrupted = false;
	const interruption = /** @type {Promise<never>} */ (
		new Promise((_resolve, reject) => {
			rejectInterruption = reject;
		})
	);
	const interrupt = () => {
		interrupted = true;
		rejectInterruption(new Error('surface acceptance interrupted'));
	};
	source.once('SIGINT', interrupt);
	source.once('SIGTERM', interrupt);
	return {
		interruption,
		isInterrupted: () => interrupted,
		dispose() {
			source.off('SIGINT', interrupt);
			source.off('SIGTERM', interrupt);
		}
	};
}
import { randomBytes } from 'node:crypto';
import { chmod, mkdtemp, rename, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import nodePath from 'node:path';
