import type { DeploymentInstanceIdentity } from './deployment-config';
import type { UpgradeAuthorization } from './surface-session';
import type { RawTransportRequest } from './transport-boundary';

const WORLD_RUNTIME_KEY = Symbol.for('c360.semmachina.world-projection-runtime.v1');

export interface InstalledWorldRuntime {
	readonly deploymentInstance: DeploymentInstanceIdentity;
	readonly attestRawTransport?: (request: RawTransportRequest) => boolean;
	readonly handle: (request: Request) => Promise<Response>;
	readonly handlePreauth?: (request: Request) => Promise<Response>;
	readonly handleLogin?: (request: Request) => Promise<Response>;
	readonly handleLogout?: (request: Request) => Promise<Response>;
	readonly authorizeUpgrade?: (request: Request) => UpgradeAuthorization | null;
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
