import { execFile } from 'node:child_process';
import { spawn } from 'node:child_process';
import net from 'node:net';
import path from 'node:path';
import readline from 'node:readline';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { promisify } from 'node:util';

import {
	DEMO_BOOTSTRAP_STAGES,
	DEMO_NAVIGATION_FAILURE_CATEGORIES,
	DEMO_PRE_NAVIGATION_FAILURE_CATEGORIES
} from './bellweather-surface-contract.mjs';

const execute = promisify(execFile);
const webRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const READY = '[surface-demo] presenter_ready';
const STOPPED = '[surface-demo] stopped';
const SYNTHETIC_KEY = 'demo-signal-proof-no-provider-key';
const PROOF_TIMEOUT_MS = 190_000;
const EXIT_TIMEOUT_MS = 30_000;
const PORTS = Object.freeze([4173, 4181, 43101, 43102, 43103]);
const OWNED_COMMAND_SIGNATURES = Object.freeze([
	'cmd/bellweather-surface-stack',
	'.server-build/server.js',
	'loopback-https-proxy.mjs',
	'playwright.demo.config.ts'
]);
const RUNNER_DIAGNOSTIC_STAGES = new Set(['unstarted', ...DEMO_BOOTSTRAP_STAGES]);
const RUNNER_NAVIGATION_CATEGORIES = new Set(DEMO_NAVIGATION_FAILURE_CATEGORIES);
const RUNNER_PRE_NAVIGATION_CATEGORIES = new Set(DEMO_PRE_NAVIGATION_FAILURE_CATEGORIES);

/**
 * @typedef {{kind: 'browser_bootstrap_failed', stage: string} |
 * {kind: 'browser_navigation_failed', category: string} |
 * {kind: 'browser_pre_navigation_failed', category: string}} RunnerDiagnostic
 */

/** @param {string} line @returns {RunnerDiagnostic | undefined} */
export function parseDemoRunnerDiagnostic(line) {
	const normalized = line.trim();
	const preNavigation = normalized.match(
		/^\[surface-demo-runner\] browser_pre_navigation_failed:([a-z_]+)$/
	);
	if (preNavigation !== null && RUNNER_PRE_NAVIGATION_CATEGORIES.has(preNavigation[1])) {
		return Object.freeze({ kind: 'browser_pre_navigation_failed', category: preNavigation[1] });
	}
	const navigation = normalized.match(
		/^\[surface-demo-runner\] browser_navigation_failed:([a-z_]+)$/
	);
	if (navigation !== null && RUNNER_NAVIGATION_CATEGORIES.has(navigation[1])) {
		return Object.freeze({ kind: 'browser_navigation_failed', category: navigation[1] });
	}
	const match = normalized.match(/^\[surface-demo-runner\] browser_bootstrap_failed:([a-z_]+)$/);
	if (match === null || !RUNNER_DIAGNOSTIC_STAGES.has(match[1])) return undefined;
	return Object.freeze({ kind: 'browser_bootstrap_failed', stage: match[1] });
}

/** @param {readonly string[]} arguments_ */
export function parseDemoSignalProofArguments(arguments_) {
	if (arguments_.length === 0) {
		return Object.freeze({ signal: 'SIGINT', label: 'interrupt', autoClose: false });
	}
	if (arguments_.length === 1 && arguments_[0] === '--auto-close') {
		return Object.freeze({ signal: null, label: 'auto-close', autoClose: true });
	}
	throw new Error('unsupported demo signal proof arguments');
}

/** @param {{kill: (signal: NodeJS.Signals) => boolean}} runner @param {NodeJS.Signals | null} signal */
export function signalDemoProofRunner(runner, signal) {
	if (signal === null) return false;
	if (!runner.kill(signal)) throw new Error('surface demo signal proof failed');
	return true;
}

/**
 * @param {{output: string, code: number | null, signal: NodeJS.Signals | null,
 * runnerGroupExists: boolean, listeners: readonly number[], processList: string}} evidence
 */
export function requireDemoSignalProof(evidence) {
	const lines = evidence.output.split(/\r?\n/);
	const exactReady = lines.filter((line) => line === READY).length === 1;
	const exactStopped = lines.filter((line) => line === STOPPED).length === 1;
	const actionFree = !/first_action|second_action|browser_tests_started/.test(evidence.output);
	const noProviderSecret = !evidence.output.includes(SYNTHETIC_KEY);
	const noOwnedProcess = OWNED_COMMAND_SIGNATURES.every(
		(signature) => !evidence.processList.includes(signature)
	);
	if (
		!exactReady ||
		!exactStopped ||
		evidence.code !== 0 ||
		evidence.signal !== null ||
		evidence.runnerGroupExists ||
		evidence.listeners.length !== 0 ||
		!noOwnedProcess ||
		!actionFree ||
		!noProviderSecret
	) {
		throw new Error('surface demo signal proof failed');
	}
}

/** @param {number} port @returns {Promise<boolean>} */
function connectable(port) {
	return new Promise((resolve) => {
		const socket = net.createConnection({ host: '127.0.0.1', port });
		const timer = setTimeout(() => {
			socket.destroy();
			resolve(false);
		}, 250);
		socket.once('connect', () => {
			clearTimeout(timer);
			socket.destroy();
			resolve(true);
		});
		socket.once('error', () => {
			clearTimeout(timer);
			resolve(false);
		});
	});
}

/** @returns {Promise<number[]>} */
async function remainingListeners() {
	const states = await Promise.all(
		PORTS.map(async (port) => /** @type {[number, boolean]} */ ([port, await connectable(port)]))
	);
	return states.filter(([, listening]) => listening).map(([port]) => port);
}

/** @param {number} pid */
function processGroupExists(pid) {
	try {
		process.kill(-pid, 0);
		return true;
	} catch (error) {
		if (error !== null && typeof error === 'object' && 'code' in error && error.code === 'ESRCH') {
			return false;
		}
		return true;
	}
}

/** @param {number} runnerPid */
async function waitForCleanState(runnerPid) {
	let quiet = 0;
	/** @type {number[]} */
	let listeners = [];
	let runnerGroup = true;
	for (let attempt = 0; attempt < 100; attempt += 1) {
		listeners = await remainingListeners();
		runnerGroup = processGroupExists(runnerPid);
		quiet = listeners.length === 0 && !runnerGroup ? quiet + 1 : 0;
		if (quiet === 2) return { listeners, runnerGroup };
		await new Promise((resolve) => setTimeout(resolve, 100));
	}
	return { listeners, runnerGroup };
}

async function ownedProcessGroups() {
	const table = await execute('ps', ['-axo', 'pid=,pgid=,command='], {
		timeout: 5_000,
		maxBuffer: 1_048_576
	});
	const groups = new Set();
	for (const line of table.stdout.split(/\r?\n/)) {
		const match = line.match(/^\s*\d+\s+(\d+)\s+(.*)$/);
		if (
			match !== null &&
			OWNED_COMMAND_SIGNATURES.some((signature) => match[2].includes(signature))
		) {
			groups.add(Number(match[1]));
		}
	}
	return groups;
}

/** @param {number} group @param {NodeJS.Signals} signal */
function signalGroup(group, signal) {
	try {
		process.kill(-group, signal);
	} catch (error) {
		if (
			error === null ||
			typeof error !== 'object' ||
			!('code' in error) ||
			error.code !== 'ESRCH'
		) {
			throw error;
		}
	}
}

/** @param {Promise<unknown>} promise @param {number} milliseconds */
async function settledWithin(promise, milliseconds) {
	return Promise.race([
		promise.then(() => true),
		new Promise((resolve) => {
			const timer = setTimeout(() => resolve(false), milliseconds);
			timer.unref();
		})
	]);
}

/**
 * @param {{pid?: number, kill: (signal: NodeJS.Signals) => boolean}} runner
 * @param {Promise<unknown>} exit
 * @param {ReadonlySet<number>} baselineGroups
 * @param {{settle?: (exit: Promise<unknown>, milliseconds: number) => Promise<boolean>,
 * ownedGroups?: () => Promise<Set<number>>, signalGroup?: (group: number, signal: NodeJS.Signals) => void,
 * sleep?: (milliseconds: number) => Promise<void>, quiet?: (pid: number) => Promise<{
 * listeners: number[], runnerGroup: boolean}>}} [dependencies]
 */
export async function cleanupFailedDemoProof(runner, exit, baselineGroups, dependencies = {}) {
	if (runner.pid === undefined) throw new Error('surface demo proof cleanup failed');
	const settle = dependencies.settle ?? settledWithin;
	const groups = dependencies.ownedGroups ?? ownedProcessGroups;
	const signal = dependencies.signalGroup ?? signalGroup;
	const sleep =
		dependencies.sleep ??
		((milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds)));
	const quiet = dependencies.quiet ?? waitForCleanState;
	runner.kill('SIGTERM');
	const runnerSettled = await settle(exit, EXIT_TIMEOUT_MS);
	const termTargets = new Set([...(await groups())].filter((group) => !baselineGroups.has(group)));
	if (!runnerSettled) termTargets.add(runner.pid);
	if (termTargets.size !== 0) {
		for (const group of termTargets) signal(group, 'SIGTERM');
		await sleep(500);
		const killTargets = new Set(
			[...(await groups())].filter((group) => !baselineGroups.has(group))
		);
		if (!runnerSettled && processGroupExists(runner.pid)) killTargets.add(runner.pid);
		for (const group of killTargets) signal(group, 'SIGKILL');
		if (!runnerSettled) await settle(exit, 5_000);
	}
	const clean = await quiet(runner.pid);
	const remainingGroups = [...(await groups())].filter((group) => !baselineGroups.has(group));
	if (clean.listeners.length !== 0 || clean.runnerGroup || remainingGroups.length !== 0) {
		throw new Error('surface demo proof cleanup failed');
	}
}

/**
 * @param {import('node:child_process').ChildProcess} child
 * @returns {Promise<{code: number | null, signal: NodeJS.Signals | null}>}
 */
function waitForExit(child) {
	return new Promise((resolve) => {
		/** @param {number | null} code @param {NodeJS.Signals | null} signal */
		const exited = (code, signal) => resolve({ code, signal });
		child.once('close', exited);
	});
}

/** @param {number} milliseconds @param {string} message @returns {Promise<never>} */
function boundedTimeout(milliseconds, message) {
	return new Promise((_, reject) => {
		const timer = setTimeout(() => reject(new Error(message)), milliseconds);
		timer.unref();
	});
}

async function main() {
	const proof = parseDemoSignalProofArguments(process.argv.slice(2));
	const baselineGroups = await ownedProcessGroups();
	const runner = spawn(process.execPath, ['tests/run-bellweather-surface.mjs', '--demo'], {
		cwd: webRoot,
		env: {
			...process.env,
			SEMMACHINA_PAID_SMOKE: '1',
			GEMINI_API_KEY: SYNTHETIC_KEY,
			...(proof.autoClose ? { SEMMACHINA_DEMO_AUTO_CLOSE_PROOF: '1' } : {})
		},
		stdio: ['ignore', 'pipe', 'pipe'],
		detached: true
	});
	if (runner.pid === undefined) throw new Error('surface demo signal proof failed');
	let output = '';
	let outputBytes = 0;
	let stderrBytes = 0;
	/** @type {RunnerDiagnostic | undefined} */
	let runnerDiagnostic;
	runner.stdout.on('data', (part) => {
		outputBytes += part.byteLength;
		if (outputBytes <= 131_072) output += part.toString('utf8');
	});
	runner.stderr.on('data', (part) => {
		stderrBytes += part.byteLength;
	});
	const stderrLines = readline.createInterface({ input: runner.stderr });
	stderrLines.on('line', (line) => {
		const diagnostic = parseDemoRunnerDiagnostic(line);
		if (diagnostic !== undefined) runnerDiagnostic = diagnostic;
	});
	const exit = waitForExit(runner);
	const lines = readline.createInterface({ input: runner.stdout });
	const presenterReady = (async () => {
		for await (const line of lines) if (line.trim() === READY) return;
		throw new Error('surface demo signal proof failed');
	})();
	let proofFailure;
	let cleanupFailure;
	try {
		await Promise.race([presenterReady, exit, boundedTimeout(PROOF_TIMEOUT_MS, 'proof timed out')]);
		signalDemoProofRunner(runner, proof.signal);
		const result = await Promise.race([
			exit,
			boundedTimeout(EXIT_TIMEOUT_MS, 'proof cleanup timed out')
		]);
		const clean = await waitForCleanState(runner.pid);
		const processTable = await execute('ps', ['-axo', 'command='], {
			timeout: 5_000,
			maxBuffer: 1_048_576
		});
		if (outputBytes > 131_072 || stderrBytes > 131_072) {
			throw new Error('surface demo signal proof failed');
		}
		requireDemoSignalProof({
			output,
			code: result.code,
			signal: result.signal,
			runnerGroupExists: clean.runnerGroup,
			listeners: clean.listeners,
			processList: processTable.stdout
		});
		process.stdout.write(`[surface-demo-proof] ${proof.label} passed\n`);
	} catch (error) {
		proofFailure =
			runnerDiagnostic === undefined
				? error
				: runnerDiagnostic.kind === 'browser_navigation_failed'
					? new Error(`surface demo signal proof navigation failed: ${runnerDiagnostic.category}`)
					: runnerDiagnostic.kind === 'browser_pre_navigation_failed'
						? new Error(
								`surface demo signal proof pre-navigation failed: ${runnerDiagnostic.category}`
							)
						: new Error(`surface demo signal proof failed at ${runnerDiagnostic.stage}`);
	} finally {
		lines.close();
		stderrLines.close();
		if (proofFailure !== undefined) {
			try {
				await cleanupFailedDemoProof(runner, exit, baselineGroups);
			} catch (error) {
				cleanupFailure = error;
			}
		}
	}
	if (proofFailure !== undefined && cleanupFailure !== undefined) {
		throw new AggregateError(
			[proofFailure, cleanupFailure],
			'surface demo proof and cleanup failed',
			{ cause: proofFailure }
		);
	}
	if (proofFailure !== undefined) throw proofFailure;
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
	await main();
}
