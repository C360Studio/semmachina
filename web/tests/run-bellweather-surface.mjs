import { spawn } from 'node:child_process';
import http from 'node:http';
import https from 'node:https';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import readline from 'node:readline';

import {
	buildBrowserTestEnvironment,
	buildDemoBrowserEnvironment,
	buildDemoWebServerEnvironment,
	buildGoEnvironment,
	buildWebServerEnvironment,
	cleanupOwnedProcessGroups,
	cleanupDemoCloseSignal,
	controlledTerminationSignals,
	coordinateControlledRun,
	createDemoCreatorCredential,
	createDemoCloseSignal,
	createDemoStartupDeadline,
	emitSurfaceCheckpoint,
	holdDemoPresentation,
	parseStackManifest,
	parseDemoBootstrapDiagnostic,
	parseSurfaceRunnerArguments,
	publishDemoCloseSignal,
	requireDemoSecretAbsent,
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
	waitForReadiness,
	withinDemoStartupDeadline
} from './bellweather-surface-contract.mjs';

const webRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const repositoryRoot = path.dirname(webRoot);

class DemoBootstrapFailure extends Error {
	constructor(stage) {
		super(`demo browser bootstrap failed at ${stage}`);
		this.stage = stage;
	}
}

class DemoNavigationFailure extends Error {
	constructor(category) {
		super(`demo browser navigation failed: ${category}`);
		this.category = category;
	}
}

class DemoPreNavigationFailure extends Error {
	constructor(category) {
		super(`demo browser pre-navigation failed: ${category}`);
		this.category = category;
	}
}

function findDemoFailure(error) {
	if (
		error instanceof DemoBootstrapFailure ||
		error instanceof DemoNavigationFailure ||
		error instanceof DemoPreNavigationFailure
	) {
		return error;
	}
	if (error instanceof AggregateError) {
		for (const nested of error.errors) {
			const failure = findDemoFailure(nested);
			if (failure !== undefined) return failure;
		}
	}
	return undefined;
}

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

async function readyDemoPresenter(browser) {
	const lines = readline.createInterface({ input: browser.stdout });
	let lastStage = 'unstarted';
	for await (const line of lines) {
		const diagnostic = parseDemoBootstrapDiagnostic(line);
		if (diagnostic?.kind === 'ready') return;
		if (diagnostic?.kind === 'progress') lastStage = diagnostic.stage;
		if (diagnostic?.kind === 'pre_navigation_failed') {
			throw new DemoPreNavigationFailure(diagnostic.category);
		}
		if (diagnostic?.kind === 'navigation_failed') {
			throw new DemoNavigationFailure(diagnostic.category);
		}
		if (diagnostic?.kind === 'failed') {
			throw new DemoBootstrapFailure(diagnostic.stage);
		}
	}
	throw new DemoBootstrapFailure(lastStage);
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
	const selection = parseSurfaceRunnerArguments(process.argv.slice(2));
	const preflight = selection.mode === 'preflight';
	const demo = selection.mode === 'demo';
	const demoAutoCloseProof = demo && process.env.SEMMACHINA_DEMO_AUTO_CLOSE_PROOF === '1';
	const interruptProof = preflight && selection.interruptProof;
	const geminiKey = preflight
		? 'preflight-no-provider-key'
		: requirePaidRunnerEnvironment(process.env);
	const demoCredential = demo ? createDemoCreatorCredential() : undefined;
	const demoStartupDeadline = demo ? createDemoStartupDeadline() : undefined;
	requireProcessGroupSupport();
	const cleanEnvironment = sanitizeBrowserEnvironment(process.env);
	delete process.env.GEMINI_API_KEY;
	delete process.env.SEMMACHINA_PAID_SMOKE;
	delete process.env.SEMMACHINA_DEMO_AUTO_CLOSE_PROOF;
	delete process.env.REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH;
	const demoCloseSignal = demoAutoCloseProof ? await createDemoCloseSignal() : undefined;
	const demoStage = (stage, supervised) => {
		const watched = superviseOwnedStage(stage, supervised);
		return demo ? withinDemoStartupDeadline(watched, demoStartupDeadline) : watched;
	};
	const signals = controlledTerminationSignals();
	const owned = [];
	emitSurfaceCheckpoint('runner_started');
	try {
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
					const { code: buildCode, signal: buildSignal } = demo
						? await withinDemoStartupDeadline(
								superviseOwnedStage(build.exit, []),
								demoStartupDeadline
							)
						: await superviseOwnedStage(build.exit, []);
					if (buildCode !== 0)
						throw new Error(`surface build failed (${buildCode ?? buildSignal})`);
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
					const manifest = await demoStage(readyManifest(stack.child), [stack]);
					emitSurfaceCheckpoint('stack_ready');
					if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');

					const surface = await startAfterDiagnosticReadiness(
						demoStage(waitForDiagnosticReadiness(manifest.diagnostics_url), [stack]),
						() => {
							emitSurfaceCheckpoint('diagnostic_ready');
							if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');
							const watched = watchOwnedProcess(
								'surface',
								'surface controller',
								spawn(process.execPath, surfaceServerCommandArguments(), {
									cwd: webRoot,
									env: {
										...cleanEnvironment,
										...(demo
											? buildDemoWebServerEnvironment(manifest, demoCredential)
											: buildWebServerEnvironment(manifest))
									},
									stdio: 'inherit',
									detached
								})
							);
							owned.push(watched);
							return watched;
						}
					);
					await demoStage(
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
					await demoStage(
						waitForReadiness('tls_proxy', () =>
							probeStatus('https://127.0.0.1:4181/api/world', 401)
						),
						[stack, surface, proxy]
					);
					emitSurfaceCheckpoint('proxy_ready');
					if (signals.isInterrupted()) throw new Error('surface acceptance interrupted');

					const playwrightCLI = fileURLToPath(import.meta.resolve('@playwright/test/cli'));
					if (demo) {
						const demoArguments = [playwrightCLI, 'test', '--config', 'playwright.demo.config.ts'];
						requireDemoSecretAbsent(demoCredential, [
							...stackCommandArguments(),
							...surfaceServerCommandArguments(),
							...tlsProxyCommandArguments(),
							...demoArguments
						]);
						const browser = watchOwnedProcess(
							'demo_browser',
							'demo browser',
							spawn(process.execPath, demoArguments, {
								cwd: webRoot,
								env: {
									...cleanEnvironment,
									...buildDemoBrowserEnvironment(manifest, demoCredential),
									...(demoCloseSignal === undefined
										? {}
										: { REAL_SURFACE_DEMO_CLOSE_SIGNAL_PATH: demoCloseSignal.path })
								},
								stdio: ['ignore', 'pipe', 'ignore'],
								detached
							})
						);
						owned.push(browser);
						await demoStage(readyDemoPresenter(browser.child), [stack, surface, proxy, browser]);
						await holdDemoPresentation(
							Promise.resolve(),
							superviseOwnedStage(browser.exit, [stack, surface, proxy]),
							console.log,
							async () => {
								if (demoCloseSignal !== undefined) {
									await publishDemoCloseSignal(demoCloseSignal);
								}
							}
						);
						return;
					}
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
		} catch (error) {
			const demoFailure = demo ? findDemoFailure(error) : undefined;
			if (demoFailure instanceof DemoBootstrapFailure) {
				process.stderr.write(
					`[surface-demo-runner] browser_bootstrap_failed:${demoFailure.stage}\n`
				);
			}
			if (demoFailure instanceof DemoNavigationFailure) {
				process.stderr.write(
					`[surface-demo-runner] browser_navigation_failed:${demoFailure.category}\n`
				);
			}
			if (demoFailure instanceof DemoPreNavigationFailure) {
				process.stderr.write(
					`[surface-demo-runner] browser_pre_navigation_failed:${demoFailure.category}\n`
				);
			}
			if (!demo || !signals.isInterrupted() || error instanceof AggregateError) {
				throw error;
			}
		}
	} finally {
		signals.dispose();
		await cleanupDemoCloseSignal(demoCloseSignal);
	}
	if (demo) process.stdout.write('[surface-demo] stopped\n');
}

await main();
