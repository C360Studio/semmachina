import type { DeploymentInstanceIdentity } from './deployment-config';

const WORLD_RUNTIME_KEY = Symbol.for('c360.semmachina.world-projection-runtime.v1');

export interface InstalledWorldRuntime {
	readonly deploymentInstance: DeploymentInstanceIdentity;
	readonly handle: (request: Request) => Promise<Response>;
}

type RegistryTarget = object;

export function installWorldRuntime(
	runtime: InstalledWorldRuntime,
	registry: RegistryTarget = globalThis
): void {
	if (Object.prototype.hasOwnProperty.call(registry, WORLD_RUNTIME_KEY)) {
		throw new Error('world projection runtime already installed');
	}
	if (!Object.isFrozen(runtime)) throw new Error('world projection runtime must be immutable');
	Object.defineProperty(registry, WORLD_RUNTIME_KEY, {
		value: runtime,
		writable: false,
		configurable: false,
		enumerable: false
	});
}

export function getInstalledWorldRuntime(
	registry: RegistryTarget = globalThis
): InstalledWorldRuntime {
	const descriptor = Object.getOwnPropertyDescriptor(registry, WORLD_RUNTIME_KEY);
	if (descriptor === undefined) throw new Error('world projection runtime is not installed');
	return descriptor.value as InstalledWorldRuntime;
}
