import assert from 'node:assert/strict';
import test from 'node:test';

import {
	NODE_IMAGE,
	canaryNonce,
	requireFixedProbeOutput,
	requireNoRemnants,
	requireTopology,
	resourceNames
} from './contract.mjs';

test('pins the multi-architecture Node runtime and creates one unique topology', () => {
	assert.match(NODE_IMAGE, /^node:22\.20\.0-alpine3\.22@sha256:[a-f0-9]{64}$/);
	const names = resourceNames('0123456789ab');
	assert.equal(new Set(Object.values(names)).size, 5);
	assert.throws(() => resourceNames('unsafe'), /identity is invalid/);
	assert.match(canaryNonce('0123456789ab'), /^[a-f0-9]{24}$/);
	assert.notEqual(canaryNonce('0123456789ab'), canaryNonce('1123456789ab'));
});

test('requires two distinct probe networks, no shared namespace, and no publication', () => {
	const names = resourceNames('0123456789ab');
	const runtime = {
		HostConfig: { PortBindings: {}, PidMode: '' },
		NetworkSettings: {
			Ports: {},
			Networks: { [names.edgeNetwork]: {}, [names.siblingNetwork]: {} }
		}
	};
	const edge = { HostConfig: { NetworkMode: names.edgeNetwork, PidMode: '' }, Mounts: [] };
	const sibling = { HostConfig: { NetworkMode: names.siblingNetwork, PidMode: '' }, Mounts: [] };
	assert.doesNotThrow(() => requireTopology(runtime, edge, sibling, names));
	assert.throws(
		() =>
			requireTopology(
				{ ...runtime, HostConfig: { PortBindings: { '43102/tcp': [{}] } } },
				edge,
				sibling,
				names
			),
		/topology inspection failed/
	);
	assert.throws(
		() =>
			requireTopology(
				runtime,
				edge,
				{ HostConfig: { NetworkMode: names.edgeNetwork }, Mounts: [] },
				names
			),
		/topology inspection failed/
	);
	assert.throws(
		() => requireTopology(runtime, { ...edge, Mounts: [{}] }, sibling, names),
		/topology inspection failed/
	);
	for (const mode of ['host', 'container:another']) {
		assert.throws(
			() =>
				requireTopology(
					{ ...runtime, HostConfig: { ...runtime.HostConfig, PidMode: mode } },
					edge,
					sibling,
					names
				),
			/topology inspection failed/
		);
		assert.throws(
			() =>
				requireTopology(
					runtime,
					{ ...edge, HostConfig: { ...edge.HostConfig, PidMode: mode } },
					sibling,
					names
				),
			/topology inspection failed/
		);
		assert.throws(
			() =>
				requireTopology(
					runtime,
					edge,
					{ ...sibling, HostConfig: { ...sibling.HostConfig, PidMode: mode } },
					names
				),
			/topology inspection failed/
		);
	}
});

test('probe and cleanup failures never echo supplied evidence', () => {
	const canary = 'ISOLATION_SECRET_CANARY_771a';
	for (const operation of [
		() => requireFixedProbeOutput(canary, 'edge'),
		() => requireNoRemnants(canary)
	]) {
		let message = '';
		try {
			operation();
		} catch (error) {
			message = error instanceof Error ? error.message : '';
		}
		assert.ok(message.length > 0);
		assert.equal(message.includes(canary), false);
	}
});
