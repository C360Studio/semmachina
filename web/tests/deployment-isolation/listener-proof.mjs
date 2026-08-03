import http from 'node:http';
import net from 'node:net';
import os from 'node:os';

const nonce = process.env.SEMMACHINA_ISOLATION_NONCE;
const CANARY = `Isolation Canary ${nonce}`;
const QUERY =
	'query SemMachinaLocations($prefix: String!, $limit: Int!) { entitiesByPrefix(prefix: $prefix, limit: $limit) { id triples { subject predicate object datatype } } }';

function connect(host) {
	return new Promise((resolve, reject) => {
		const socket = net.createConnection({ host, port: 43102 });
		const timer = setTimeout(() => {
			socket.destroy();
			reject(new Error('timeout'));
		}, 1_000);
		socket.once('connect', () => {
			clearTimeout(timer);
			socket.destroy();
			resolve();
		});
		socket.once('error', (error) => {
			clearTimeout(timer);
			reject(error);
		});
	});
}

function queryCanary() {
	return new Promise((resolve, reject) => {
		const body = JSON.stringify({
			query: QUERY,
			variables: { prefix: 'c360.semmachina.isolation.isolation-world.location', limit: 1000 }
		});
		const request = http.request(
			{
				host: '127.0.0.1',
				port: 43102,
				path: '/graphql',
				method: 'POST',
				headers: {
					'content-type': 'application/json',
					'content-length': Buffer.byteLength(body)
				}
			},
			(response) => {
				let text = '';
				response.setEncoding('utf8');
				response.on('data', (part) => {
					if (text.length < 16_384) text += part;
				});
				response.on('end', () => {
					try {
						const document = JSON.parse(text);
						const triples = document?.data?.entitiesByPrefix?.[0]?.triples;
						if (
							response.statusCode !== 200 ||
							!Array.isArray(triples) ||
							!triples.some((triple) => triple?.object === CANARY)
						) {
							reject(new Error('invalid canary'));
							return;
						}
						resolve();
					} catch {
						reject(new Error('invalid canary'));
					}
				});
			}
		);
		request.setTimeout(1_000, () => request.destroy(new Error('timeout')));
		request.once('error', reject);
		request.end(body);
	});
}

try {
	if (!/^[a-f0-9]{24}$/.test(nonce ?? '')) throw new Error('invalid isolation nonce');
	await connect('127.0.0.1');
	await queryCanary();
	const addresses = Object.values(os.networkInterfaces())
		.flatMap((entries) => entries ?? [])
		.filter((entry) => entry.family === 'IPv4' && !entry.internal)
		.map((entry) => entry.address);
	if (addresses.length === 0) throw new Error('no container address');
	for (const address of addresses) {
		try {
			await connect(address);
			throw new Error('published listener');
		} catch (error) {
			if (error instanceof Error && error.message === 'published listener') throw error;
		}
	}
	process.stdout.write('OK: loopback listener\n');
} catch {
	process.stderr.write('surface isolation listener proof failed\n');
	process.exitCode = 1;
}
