import {
	loadDeploymentConfig,
	type DeploymentConfig,
	type DeploymentEnvironment
} from './deployment-config';
import type { AuthenticatedProjectionPrincipal } from './projection-principal';
import {
	createWorldProjectionAdapter,
	ProjectionError,
	type ProjectionAdapterOptions,
	type WorldProjection
} from './world-projection';
import type { InstalledWorldRuntime } from './world-runtime-registry';

interface RouteDependencies {
	readonly projectWorld: (principal: AuthenticatedProjectionPrincipal) => Promise<WorldProjection>;
	readonly authorize?: (
		request: Request
	) => Promise<AuthenticatedProjectionPrincipal | null> | AuthenticatedProjectionPrincipal | null;
}

function response(status: number, code: string, body?: WorldProjection): Response {
	return new Response(JSON.stringify(body ?? { error: { code } }), {
		status,
		headers: {
			'cache-control': 'no-store',
			'content-type': 'application/json; charset=utf-8'
		}
	});
}

export function createWorldProjectionRoute(dependencies: RouteDependencies) {
	return async (request: Request): Promise<Response> => {
		const url = new URL(request.url);
		if (request.method !== 'GET') return response(405, 'method_not_allowed');
		if ([...url.searchParams].length !== 0 || request.headers.has('authorization')) {
			return response(400, 'browser_authority_refused');
		}

		let principal: AuthenticatedProjectionPrincipal | null;
		try {
			principal = (await dependencies.authorize?.(request)) ?? null;
		} catch {
			return response(401, 'unauthorized');
		}
		if (principal === null) return response(401, 'unauthorized');

		try {
			return response(200, '', await dependencies.projectWorld(principal));
		} catch (error) {
			if (error instanceof ProjectionError) {
				const status = error.code === 'unauthorized' ? 401 : 502;
				return response(status, error.code);
			}
			return response(500, 'projection_failed');
		}
	};
}

export interface AssemblyDependencies {
	readonly environment: DeploymentEnvironment;
	readonly fetcher: typeof fetch;
	readonly authorize?: (
		request: Request,
		config: DeploymentConfig
	) => Promise<AuthenticatedProjectionPrincipal | null> | AuthenticatedProjectionPrincipal | null;
	readonly adapterOptions?: ProjectionAdapterOptions;
}

export function assembleWorldProjectionRuntime(
	dependencies: AssemblyDependencies
): InstalledWorldRuntime {
	const config = loadDeploymentConfig(dependencies.environment);
	const adapter = createWorldProjectionAdapter(
		config,
		dependencies.fetcher,
		dependencies.adapterOptions
	);
	const handle = createWorldProjectionRoute({
		projectWorld: (principal) => adapter.projectWorld(principal),
		authorize:
			dependencies.authorize === undefined
				? undefined
				: (request) => dependencies.authorize?.(request, config) ?? null
	});
	return Object.freeze({ deploymentInstance: config.deploymentInstance, handle });
}

export function assembleWorldProjectionRoute(dependencies: AssemblyDependencies) {
	return assembleWorldProjectionRuntime(dependencies).handle;
}
