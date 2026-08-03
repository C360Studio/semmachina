import { spawn } from 'node:child_process';
import http from 'node:http';
import https from 'node:https';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import readline from 'node:readline';

import {
	buildBrowserTestEnvironment,
	buildGoEnvironment,
	buildWebServerEnvironment,
	cleanupOwnedProcessGroups,
	controlledTerminationSignals,
	coordinateControlledRun,
	emitSurfaceCheckpoint,
	parseStackManifest,
	requirePaidRunnerEnvironment,
	requireProcessGroupSupport,
	sanitizeBrowserEnvironment,
	stackCommandArguments,
	startAfterDiagnosticReadiness,
	superviseOwnedStage,
	surfaceServerCommandArguments,
	tlsProxyCommandArguments,
	useDetachedProcessGroup,
	watchOwnedProcess,
	waitForDiagnosticReadiness,
	waitForReadiness
} from './bellweather-surface-contract.mjs';

const webRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const repositoryRoot = path.dirname(webRoot);

async function readyManifest(stack) {
	const lines = readline.createInterface({ input: stack.stdout });
	const timeout = new Promise((_, reject) => {
		const timer = setTimeout(
			() => reject(new Error('Bellweather stack readiness timed out')),
			60_000
		);
		timer.unref();
	});
	const firstLine = (async () => {
		for await (const line of lines) {
			if (line.trim() !== '') return parseStackManifest(line);
		}
		throw new Error('Bellweather stack exited before its manifest');
	})();
	return Promise.race([firstLine, timeout]);
}

function probeStatus(url, expectedStatus) {
	return new Promise((resolve) => {
		const client = url.startsWith('https:') ? https : http;
		const request = client.get(url, { rejectUnauthorized: false, agent: false }, (response) => {
			const ready = response.statusCode === expectedStatus;
			response.destroy();
			resolve(ready);
		});
		request.setTimeout(1_000, () => request.destroy());
		request.once('error', () => resolve(false));
	});
}

async function main() {
	const arguments_ = process.argv.slice(2);
	const preflight = arguments_[0] === '--preflight';
	const interruptProof = preflight && arguments_[1] === '--interrupt-proof';
	if (
		(!preflight && arguments_.length !== 0) ||
		(preflight && arguments_.length !== (interruptProof ? 2 : 1))
	) {
		throw new Error('unsupported runner arguments');
	}
	const geminiKey = preflight
		? 'preflight-no-provider-key'
		: requirePaidRunnerEnvironment(process.env);
	requireProcessGroupSupport();
	const cleanEnvironment = sanitizeBrowserEnvironment(process.env);
	delete process.env.GEMINI_API_KEY;
	delete process.env.SEMMACHINA_PAID_SMOKE;
	const signals = controlledTerminationSignals();
	const owned = [];
	emitSurfaceCheckpoint('runner_started');
	try {
		await coordinateControlledRun(
			async () => {
				const detached = useDetachedProcessGroup();
				const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
				const build = watchOwnedProcess(
					'build',
					'surface build',
					spawn(npm, ['run', 'build'], {
						cwd: webRoot,
						env: cleanEnvironment,
						stdio: 'inherit',
						detached
					})
				);
				owned.push(build);
				const { code: buildCode, signal: buildSignal } = await superviseOwnedStage(build.exit, []);
				if (buildCode !== 0) throw new Error(`surface build failed (${buildCode ?? buildSignal})`);
				emitSurfaceCheckpoint('build_complete');
				if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');
				const stack = watchOwnedProcess(
					'stack',
					'Bellweather stack',
					spawn('go', stackCommandArguments(), {
						cwd: repositoryRoot,
						env: buildGoEnvironment(cleanEnvironment, geminiKey),
						stdio: ['ignore', 'pipe', 'inherit'],
						detached
					})
				);
				owned.push(stack);
				const manifest = await superviseOwnedStage(readyManifest(stack.child), [stack]);
				emitSurfaceCheckpoint('stack_ready');
				if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');

				const surface = await startAfterDiagnosticReadiness(
					superviseOwnedStage(waitForDiagnosticReadiness(manifest.diagnostics_url), [stack]),
					() => {
						emitSurfaceCheckpoint('diagnostic_ready');
						if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');
						const watched = watchOwnedProcess(
							'surface',
							'surface controller',
							spawn(process.execPath, surfaceServerCommandArguments(), {
								cwd: webRoot,
								env: { ...cleanEnvironment, ...buildWebServerEnvironment(manifest) },
								stdio: 'inherit',
								detached
							})
						);
						owned.push(watched);
						return watched;
					}
				);
				await superviseOwnedStage(
					waitForReadiness('surface_http', () =>
						probeStatus('http://127.0.0.1:4173/api/world', 401)
					),
					[stack, surface]
				);
				emitSurfaceCheckpoint('surface_ready');
				if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');

				const proxy = watchOwnedProcess(
					'tls_proxy',
					'TLS proxy',
					spawn(process.execPath, tlsProxyCommandArguments(), {
						cwd: webRoot,
						env: cleanEnvironment,
						stdio: 'inherit',
						detached
					})
				);
				owned.push(proxy);
				await superviseOwnedStage(
					waitForReadiness('tls_proxy', () => probeStatus('https://127.0.0.1:4181/api/world', 401)),
					[stack, surface, proxy]
				);
				emitSurfaceCheckpoint('proxy_ready');
				if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');

				const playwrightCLI = fileURLToPath(import.meta.resolve('@playwright/test/cli'));
				const playwright = watchOwnedProcess(
					'playwright',
					'Playwright',
					spawn(
						process.execPath,
						[playwrightCLI, 'test', '--config', 'playwright.real.config.ts'],
						{
							cwd: webRoot,
							env: {
								...cleanEnvironment,
								...buildBrowserTestEnvironment(manifest),
								...(preflight ? { REAL_SURFACE_PREFLIGHT: '1' } : {}),
								...(interruptProof ? { REAL_SURFACE_INTERRUPT_PROOF: '1' } : {})
							},
							stdio: 'inherit',
							detached
						}
					)
				);
				owned.push(playwright);
				emitSurfaceCheckpoint('browser_tests_started');
				const { code, signal } = await superviseOwnedStage(playwright.exit, [
					stack,
					surface,
					proxy
				]);
				if (code !== 0) throw new Error(`real Playwright acceptance failed (${code ?? signal})`);
				emitSurfaceCheckpoint('browser_tests_complete');
			},
			async () => cleanupOwnedProcessGroups([...owned].reverse()),
			signals.interruption
		);
	} finally {
		signals.dispose();
	}
}

await main();
