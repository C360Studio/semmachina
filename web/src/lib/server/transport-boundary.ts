import { randomBytes } from 'node:crypto';

import type { SurfaceConfig } from './surface-config';

export const INTERNAL_TRANSPORT_HEADER = 'x-semmachina-internal-transport';

export interface RawTransportRequest {
	readonly rawHeaders: readonly string[];
	readonly socket: { readonly remoteAddress?: string };
	readonly headers: Record<string, string | string[] | undefined>;
}

export interface TrustedProxyBoundary {
	readonly attestRawRequest: (request: RawTransportRequest) => boolean;
	readonly isAttestedRequest: (request: Request) => boolean;
}

function isLiteralLoopback(value: string | undefined): boolean {
	if (value === '::1') return true;
	const normalized = value?.startsWith('::ffff:') ? value.slice(7) : value;
	const octets = normalized?.split('.') ?? [];
	return (
		octets.length === 4 &&
		octets[0] === '127' &&
		octets.every((octet) => /^\d+$/.test(octet) && Number(octet) <= 255)
	);
}

function rawValues(request: RawTransportRequest, name: string): string[] {
	const values: string[] = [];
	for (let index = 0; index < request.rawHeaders.length; index += 2) {
		if (request.rawHeaders[index]?.toLowerCase() === name) {
			values.push(request.rawHeaders[index + 1] ?? '');
		}
	}
	return values;
}

export function createTrustedProxyBoundary(
	config: SurfaceConfig,
	token = randomBytes(32).toString('base64url')
): TrustedProxyBoundary {
	if (!/^[A-Za-z0-9_-]{43}$/.test(token)) throw new Error('invalid transport attestation');

	function attestRawRequest(request: RawTransportRequest): boolean {
		// Never preserve a browser-supplied value, including combined duplicate values.
		if (
			typeof request !== 'object' ||
			request === null ||
			typeof request.headers !== 'object' ||
			request.headers === null ||
			!Array.isArray(request.rawHeaders) ||
			typeof request.socket !== 'object' ||
			request.socket === null
		) {
			return false;
		}
		delete request.headers[INTERNAL_TRANSPORT_HEADER];
		const hosts = rawValues(request, 'host');
		const protocols = rawValues(request, 'x-forwarded-proto');
		if (
			!isLiteralLoopback(request.socket.remoteAddress) ||
			hosts.length !== 1 ||
			hosts[0] !== config.publicHost ||
			protocols.length !== 1 ||
			protocols[0] !== 'https'
		) {
			return false;
		}
		request.headers[INTERNAL_TRANSPORT_HEADER] = token;
		return true;
	}

	function isAttestedRequest(request: Request): boolean {
		return request.headers.get(INTERNAL_TRANSPORT_HEADER) === token;
	}

	return Object.freeze({ attestRawRequest, isAttestedRequest });
}
