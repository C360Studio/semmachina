import { loadDeploymentConfig, type DeploymentEnvironment } from './deployment-config';
import { loadSurfaceConfig } from './surface-config';
import { createSurfaceSessionAuthority } from './surface-session';
import { createTrustedProxyBoundary } from './transport-boundary';
import { createWorldProjectionRoute } from './world-projection-route';
import { createWorldProjectionAdapter, type ProjectionAdapterOptions } from './world-projection';
import type { InstalledWorldRuntime } from './world-runtime-registry';

interface SurfaceRuntimeDependencies {
	readonly environment: DeploymentEnvironment;
	readonly fetcher: typeof fetch;
	readonly adapterOptions?: ProjectionAdapterOptions;
}

export function assembleSurfaceRuntime(
	dependencies: SurfaceRuntimeDependencies
): InstalledWorldRuntime {
	const deployment = loadDeploymentConfig(dependencies.environment);
	const surface = loadSurfaceConfig(dependencies.environment, deployment);
	const transport = createTrustedProxyBoundary(surface);
	const sessions = createSurfaceSessionAuthority(surface, deployment, {
		isTransportAttested: transport.isAttestedRequest
	});
	const adapter = createWorldProjectionAdapter(
		deployment,
		dependencies.fetcher,
		dependencies.adapterOptions
	);
	const handle = createWorldProjectionRoute({
		projectWorld: (principal) => adapter.projectWorld(principal),
		authorize: (request) => sessions.authorizeProjection(request)
	});
	return Object.freeze({
		deploymentInstance: deployment.deploymentInstance,
		attestRawTransport: transport.attestRawRequest,
		handle,
		handlePreauth: sessions.handlePreauth,
		handleLogin: sessions.handleLogin,
		handleLogout: sessions.handleLogout,
		authorizeUpgrade: sessions.authorizeUpgrade
	});
}
