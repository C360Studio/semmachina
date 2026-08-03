import { describe, expect, it, vi } from 'vitest';

import type { DeploymentInstanceIdentity } from './deployment-config';
import {
	getInstalledWorldRuntime,
	installWorldRuntime,
	type InstalledWorldRuntime
} from './world-runtime-registry';

function runtime(): InstalledWorldRuntime {
	return Object.freeze({
		deploymentInstance: Object.freeze({}) as DeploymentInstanceIdentity,
		handle: vi.fn(async () => new Response(null, { status: 401 }))
	});
}

describe('world runtime registry', () => {
	it('installs one frozen runtime under a non-writable process-stable key', () => {
		const registry = {};
		const installed = runtime();
		installWorldRuntime(installed, registry);
		expect(getInstalledWorldRuntime(registry)).toBe(installed);
		const symbols = Object.getOwnPropertySymbols(registry);
		expect(symbols).toContain(Symbol.for('c360.semmachina.world-projection-runtime.v1'));
		expect(Object.getOwnPropertyDescriptor(registry, symbols[0])).toMatchObject({
			writable: false,
			configurable: false,
			enumerable: false
		});
	});

	it('refuses a second installation', () => {
		const registry = {};
		installWorldRuntime(runtime(), registry);
		expect(() => installWorldRuntime(runtime(), registry)).toThrow();
	});
});
