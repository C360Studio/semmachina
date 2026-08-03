import type { DeploymentConfig, DeploymentInstanceIdentity } from './deployment-config';

const issuedPrincipals = new WeakSet<object>();

export interface AuthenticatedProjectionPrincipal {
	readonly deploymentInstance: DeploymentInstanceIdentity;
}

export function issueProjectionPrincipal(
	config: DeploymentConfig
): AuthenticatedProjectionPrincipal {
	const principal = Object.freeze({ deploymentInstance: config.deploymentInstance });
	issuedPrincipals.add(principal);
	return principal;
}

export function isAuthorizedProjectionPrincipal(
	principal: unknown,
	config: DeploymentConfig
): principal is AuthenticatedProjectionPrincipal {
	return (
		typeof principal === 'object' &&
		principal !== null &&
		issuedPrincipals.has(principal) &&
		(principal as AuthenticatedProjectionPrincipal).deploymentInstance === config.deploymentInstance
	);
}
