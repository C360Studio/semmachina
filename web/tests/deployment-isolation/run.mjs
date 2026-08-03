import { execFile } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { access } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

import {
	NODE_IMAGE,
	RUN_LABEL,
	canaryNonce,
	requireFixedProbeOutput,
	requireNoRemnants,
	requireTopology,
	resourceNames
} from './contract.mjs';
import { createSerializedInterruption } from './interruption.mjs';

const execute = promisify(execFile);
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const runId = randomBytes(6).toString('hex');
const names = resourceNames(runId);
const nonce = canaryNonce(runId);
const label = `${RUN_LABEL}=${runId}`;
const probePath = path.join(webRoot, 'tests/deployment-isolation/probe.mjs');
let cleanupPromise;
let phase = 'prerequisites';
const interruption = createSerializedInterruption();

async function rawDocker(arguments_, timeout = 90_000, signal) {
	try {
		return await execute('docker', arguments_, {
			timeout,
			maxBuffer: 65_536,
			env: process.env,
			...(signal === undefined ? {} : { signal })
		});
	} catch {
		throw new Error('surface isolation Docker operation failed');
	}
}

async function docker(arguments_, timeout = 90_000) {
	return interruption.run((signal) => rawDocker(arguments_, timeout, signal));
}

async function cleanup() {
	if (cleanupPromise !== undefined) return cleanupPromise;
	cleanupPromise = (async () => {
		await interruption.settled();
		let remnants = '';
		let consecutiveEmptyChecks = 0;
		for (let attempt = 0; attempt < 3; attempt += 1) {
			try {
				await rawDocker(['rm', '-f', names.runtime, names.edgeProbe, names.siblingProbe], 15_000);
			} catch {
				// A partially-created run is expected to have missing explicit resources.
			}
			for (const network of [names.edgeNetwork, names.siblingNetwork]) {
				try {
					await rawDocker(['network', 'rm', network], 15_000);
				} catch {
					// The authoritative label query below decides whether retry is needed.
				}
			}
			const containers = await rawDocker(['ps', '-aq', '--filter', `label=${label}`], 15_000);
			const networks = await rawDocker(
				['network', 'ls', '-q', '--filter', `label=${label}`],
				15_000
			);
			remnants = `${containers.stdout}${networks.stdout}`;
			consecutiveEmptyChecks = remnants.trim() === '' ? consecutiveEmptyChecks + 1 : 0;
			if (consecutiveEmptyChecks === 2) return;
			await new Promise((resolve) => setTimeout(resolve, 200));
		}
		requireNoRemnants(remnants);
		throw new Error('surface isolation cleanup did not reach a quiet verification window');
	})();
	return cleanupPromise;
}

for (const signal of ['SIGINT', 'SIGTERM']) {
	process.once(signal, () => {
		interruption.interrupt(signal);
	});
}

async function run() {
	phase = 'contract tests';
	await execute(
		process.execPath,
		[
			'--test',
			path.join(webRoot, 'tests/deployment-isolation/contract.test.mjs'),
			path.join(webRoot, 'tests/deployment-isolation/interruption.test.mjs')
		],
		{ timeout: 15_000, maxBuffer: 65_536, env: process.env }
	);
	await access(path.join(webRoot, '.server-build/server.js'));
	await access(path.join(webRoot, 'node_modules/ws/package.json'));

	phase = 'network creation';
	await docker(['network', 'create', '--label', label, names.edgeNetwork]);
	await docker(['network', 'create', '--label', label, names.siblingNetwork]);
	phase = 'runtime creation';
	await docker([
		'run',
		'-d',
		'--name',
		names.runtime,
		'--label',
		label,
		'--network',
		names.edgeNetwork,
		'--mount',
		`type=bind,src=${webRoot},dst=/workspace,readonly`,
		'-w',
		'/workspace',
		'-e',
		'HOST=127.0.0.1',
		'-e',
		'PORT=4173',
		'-e',
		`ORIGIN=https://${names.runtime}:4181`,
		'-e',
		`SEMMACHINA_PROXY_HOST=${names.runtime}:4181`,
		'-e',
		'SEMMACHINA_GRAPHQL_URL=http://127.0.0.1:43102/graphql',
		'-e',
		'SEMMACHINA_GRAPHQL_POSTURE=loopback',
		'-e',
		'SEMMACHINA_WORLD_ORG=c360',
		'-e',
		'SEMMACHINA_WORLD_NAMESPACE=isolation',
		'-e',
		'SEMMACHINA_WORLD_TEMPLATE=isolation-world',
		'-e',
		`SEMMACHINA_PUBLIC_ORIGIN=https://${names.runtime}:4181`,
		'-e',
		'SEMMACHINA_TLS_POSTURE=trusted_loopback_proxy',
		'-e',
		'SEMMACHINA_CREATOR_CREDENTIAL=isolation-creator-credential',
		'-e',
		'SEMMACHINA_PLAYER_BEARER=isolation-local-player-bearer',
		'-e',
		'SEMMACHINA_PLAYER_WS_URL=ws://127.0.0.1:43103/play',
		'-e',
		'SEMMACHINA_PLAYER_ID=c360.semmachina.isolation.isolation-world.player.isolation',
		'-e',
		`SEMMACHINA_ISOLATION_NONCE=${nonce}`,
		NODE_IMAGE,
		'node',
		'tests/deployment-isolation/runtime.mjs'
	]);
	await docker(['network', 'connect', names.siblingNetwork, names.runtime]);

	phase = 'probe creation';
	for (const [probeName, network, role] of [
		[names.edgeProbe, names.edgeNetwork, 'edge'],
		[names.siblingProbe, names.siblingNetwork, 'sibling']
	]) {
		await docker([
			'create',
			'--name',
			probeName,
			'--label',
			label,
			'--network',
			network,
			'-e',
			`SEMMACHINA_RUNTIME_HOST=${names.runtime}`,
			...(role === 'edge' ? ['-e', `SEMMACHINA_ISOLATION_NONCE=${nonce}`] : []),
			NODE_IMAGE,
			'node',
			'/probe.mjs',
			role
		]);
		await docker(['cp', probePath, `${probeName}:/probe.mjs`]);
	}

	phase = 'topology inspection';
	const inspected = await docker(['inspect', names.runtime, names.edgeProbe, names.siblingProbe]);
	const [runtime, edge, sibling] = JSON.parse(inspected.stdout);
	requireTopology(runtime, edge, sibling, names);

	phase = 'listener proof';
	let listenerReady = false;
	for (let attempt = 0; attempt < 20 && !listenerReady; attempt += 1) {
		try {
			const proof = await docker(
				['exec', names.runtime, 'node', 'tests/deployment-isolation/listener-proof.mjs'],
				5_000
			);
			listenerReady = proof.stdout.trim() === 'OK: loopback listener';
		} catch {
			// The runtime and actual Svelte server have a bounded startup window.
		}
		if (!listenerReady) await new Promise((resolve) => setTimeout(resolve, 500));
	}
	if (!listenerReady) throw new Error('surface isolation runtime did not become ready');
	process.stdout.write('OK: runtime loopback listener\n');

	phase = 'edge probe';
	const edgeResult = await docker(['start', '-a', names.edgeProbe], 30_000);
	requireFixedProbeOutput(edgeResult.stdout, 'edge');
	process.stdout.write(edgeResult.stdout);
	phase = 'sibling probe';
	const siblingResult = await docker(['start', '-a', names.siblingProbe], 30_000);
	requireFixedProbeOutput(siblingResult.stdout, 'sibling');
	process.stdout.write(siblingResult.stdout);
}

try {
	await run();
} catch {
	if (!interruption.isInterrupted()) {
		process.stderr.write(`FAIL: surface deployment isolation at ${phase}\n`);
		process.exitCode = 1;
	}
} finally {
	try {
		await cleanup();
		if (!interruption.isInterrupted() && process.exitCode !== 1) {
			process.stdout.write('OK: surface deployment isolation\n');
		}
	} catch {
		if (!interruption.isInterrupted()) {
			process.stderr.write('FAIL: surface deployment isolation cleanup\n');
			process.exitCode = 1;
		}
	}
	if (interruption.isInterrupted() && process.exitCode !== 1) {
		process.exitCode = interruption.exitCode();
	}
}
