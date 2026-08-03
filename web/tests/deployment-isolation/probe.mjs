import dns from 'node:dns/promises';
import http from 'node:http';
import https from 'node:https';
import net from 'node:net';

const role = process.argv[2];
const runtimeHost = process.env.SEMMACHINA_RUNTIME_HOST;
const nonce = process.env.SEMMACHINA_ISOLATION_NONCE;
const origin = `https://${runtimeHost}:4181`;
const agent = new https.Agent({ rejectUnauthorized: false });
const EXACT_GRAPHQL_BODY = JSON.stringify({
	query:
		'query SemMachinaIsolationProbe { entitiesByPrefix(prefix: "c360.semmachina.isolation.isolation-world.location", limit: 1) { id } }',
	variables: {}
});

function fail() {
	process.stderr.write('surface isolation probe failed\n');
	process.exitCode = 1;
}

function requestHttps(path, options = {}) {
	return new Promise((resolve, reject) => {
		const body = options.body === undefined ? undefined : JSON.stringify(options.body);
		const request = https.request(
			{
				host: runtimeHost,
				port: 4181,
				path,
				method: options.method ?? 'GET',
				agent,
				headers: {
					...(body === undefined
						? {}
						: {
								'content-type': 'application/json',
								'content-length': Buffer.byteLength(body)
							}),
					...(options.origin === true ? { origin } : {}),
					...(options.cookie === undefined ? {} : { cookie: options.cookie })
				}
			},
			(response) => {
				let text = '';
				let bytes = 0;
				response.on('data', (part) => {
					bytes += part.length;
					if (bytes <= 65_536) text += part;
				});
				response.on('end', () => {
					if (bytes > 65_536) {
						reject(new Error('response too large'));
						return;
					}
					resolve({ status: response.statusCode, headers: response.headers, text });
				});
			}
		);
		request.setTimeout(2_000, () => request.destroy(new Error('timeout')));
		request.once('error', reject);
		request.end(body);
	});
}

function cookie(response, name) {
	const values = response.headers['set-cookie'] ?? [];
	const match = values.join(';').match(new RegExp(`${name}=[A-Za-z0-9_-]{43}`));
	if (match === null) throw new Error('missing cookie');
	return match[0];
}

async function proveHttpsSurface() {
	if (role === 'sibling') {
		const unauthorized = await requestHttps('/api/world');
		if (unauthorized.status !== 401) throw new Error('unexpected surface response');
		return;
	}
	const preauth = await requestHttps('/api/auth/preauth', { method: 'POST', origin: true });
	if (preauth.status !== 200) throw new Error('preauth failed');
	const csrf = JSON.parse(preauth.text)?.csrf;
	if (typeof csrf !== 'string') throw new Error('preauth failed');
	const login = await requestHttps('/api/auth/login', {
		method: 'POST',
		origin: true,
		cookie: cookie(preauth, '__Host-semmachina_preauth'),
		body: { credential: 'isolation-creator-credential', csrf }
	});
	if (login.status !== 200) throw new Error('login failed');
	const world = await requestHttps('/api/world', {
		cookie: cookie(login, '__Host-semmachina_session')
	});
	if (world.status !== 200) throw new Error('world retrieval failed');
	const document = JSON.parse(world.text);
	if (!/^[a-f0-9]{24}$/.test(nonce ?? '')) throw new Error('invalid isolation nonce');
	if (
		!Array.isArray(document?.places) ||
		document.places.length !== 1 ||
		document.places[0]?.label !== `Isolation Canary ${nonce}`
	) {
		throw new Error('canary absent');
	}
}

async function proveRawTcpRefused() {
	await dns.lookup(runtimeHost);
	await new Promise((resolve, reject) => {
		const socket = net.createConnection({ host: runtimeHost, port: 43102 });
		const timer = setTimeout(() => {
			socket.destroy();
			resolve();
		}, 2_000);
		socket.once('connect', () => {
			clearTimeout(timer);
			socket.destroy();
			reject(new Error('tcp connected'));
		});
		socket.once('error', (error) => {
			clearTimeout(timer);
			if (['ENOTFOUND', 'EAI_AGAIN'].includes(error.code)) reject(new Error('dns failed'));
			else resolve();
		});
	});
}

async function proveExactPostRefused() {
	await dns.lookup(runtimeHost);
	await new Promise((resolve, reject) => {
		const request = http.request(
			{
				host: runtimeHost,
				port: 43102,
				path: '/graphql',
				method: 'POST',
				headers: {
					'content-type': 'application/json',
					'content-length': Buffer.byteLength(EXACT_GRAPHQL_BODY)
				}
			},
			(response) => {
				response.resume();
				reject(new Error('post connected'));
			}
		);
		request.setTimeout(2_000, () => {
			request.destroy();
			resolve();
		});
		request.once('error', (error) => {
			if (['ENOTFOUND', 'EAI_AGAIN'].includes(error.code)) reject(new Error('dns failed'));
			else resolve();
		});
		request.end(EXACT_GRAPHQL_BODY);
	});
}

try {
	if (!['edge', 'sibling'].includes(role) || typeof runtimeHost !== 'string') {
		throw new Error('invalid probe');
	}
	let surfaceReady = false;
	for (let attempt = 0; attempt < 20 && !surfaceReady; attempt += 1) {
		try {
			await proveHttpsSurface();
			surfaceReady = true;
		} catch {
			await new Promise((resolve) => setTimeout(resolve, 500));
		}
	}
	if (!surfaceReady) throw new Error('surface unavailable');
	await proveRawTcpRefused();
	await proveExactPostRefused();
	process.stdout.write(`OK: ${role} isolation\n`);
} catch {
	fail();
}
