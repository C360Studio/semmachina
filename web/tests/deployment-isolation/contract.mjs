import { createHash } from 'node:crypto';

export const NODE_IMAGE =
	'node:22.20.0-alpine3.22@sha256:dbcedd8aeab47fbc0f4dd4bffa55b7c3c729a707875968d467aaaea42d6225af';
export const RUN_LABEL = 'semmachina.surface-isolation.run';

export function canaryNonce(runId) {
	if (!/^[a-f0-9]{12}$/.test(runId)) throw new Error('isolation run identity is invalid');
	return createHash('sha256')
		.update(`semmachina.surface-isolation.canary.v1:${runId}`, 'utf8')
		.digest('hex')
		.slice(0, 24);
}

export function resourceNames(runId) {
	if (!/^[a-f0-9]{12}$/.test(runId)) throw new Error('isolation run identity is invalid');
	const prefix = `smiso-${runId}`;
	return Object.freeze({
		runtime: `${prefix}-runtime`,
		edgeProbe: `${prefix}-edge-probe`,
		siblingProbe: `${prefix}-sibling-probe`,
		edgeNetwork: `${prefix}-edge`,
		siblingNetwork: `${prefix}-sibling`
	});
}

export function requireTopology(runtime, edge, sibling, names) {
	const runtimeNetworks = Object.keys(runtime.NetworkSettings?.Networks ?? {}).sort();
	const noPorts =
		Object.keys(runtime.HostConfig?.PortBindings ?? {}).length === 0 &&
		Object.keys(runtime.NetworkSettings?.Ports ?? {}).length === 0;
	const separateModes =
		edge.HostConfig?.NetworkMode === names.edgeNetwork &&
		sibling.HostConfig?.NetworkMode === names.siblingNetwork &&
		edge.HostConfig?.NetworkMode !== sibling.HostConfig?.NetworkMode &&
		!String(edge.HostConfig?.NetworkMode).startsWith('container:') &&
		!String(sibling.HostConfig?.NetworkMode).startsWith('container:');
	const privatePidModes =
		runtime.HostConfig?.PidMode === '' &&
		edge.HostConfig?.PidMode === '' &&
		sibling.HostConfig?.PidMode === '';
	const probesHaveNoMounts = edge.Mounts?.length === 0 && sibling.Mounts?.length === 0;
	if (
		JSON.stringify(runtimeNetworks) !==
			JSON.stringify([names.edgeNetwork, names.siblingNetwork].sort()) ||
		!noPorts ||
		!separateModes ||
		!privatePidModes ||
		!probesHaveNoMounts
	) {
		throw new Error('surface isolation topology inspection failed');
	}
}

export function requireFixedProbeOutput(output, role) {
	if (!['edge', 'sibling'].includes(role) || output.trim() !== `OK: ${role} isolation`) {
		throw new Error('surface isolation probe failed');
	}
}

export function requireNoRemnants(ids) {
	if (ids.trim() !== '') throw new Error('surface isolation cleanup left resources');
}
