import assert from 'node:assert/strict';
import test from 'node:test';

import { IsolationInterruptedError, createSerializedInterruption } from './interruption.mjs';

test('interrupt during creation settles the create before cleanup and leaves no late remnants', async () => {
	const interruption = createSerializedInterruption();
	const containers = new Set();
	const networks = new Set();
	let finishCreation;
	const creationGate = new Promise((resolve) => {
		finishCreation = resolve;
	});
	const creation = interruption.run(async (signal) => {
		if (!signal.aborted) {
			await new Promise((resolve) => signal.addEventListener('abort', resolve, { once: true }));
		}
		await creationGate;
		containers.add('late-labeled-container');
		networks.add('late-labeled-network');
		throw new Error('docker client aborted after daemon accepted creation');
	});

	interruption.interrupt('SIGTERM');
	const cleanup = (async () => {
		await interruption.settled();
		containers.clear();
		networks.clear();
	})();
	finishCreation();
	await assert.rejects(creation);
	await cleanup;

	assert.equal(interruption.exitCode(), 143);
	assert.deepEqual([...containers], []);
	assert.deepEqual([...networks], []);
	await assert.rejects(() => interruption.run(async () => {}), IsolationInterruptedError);
});
