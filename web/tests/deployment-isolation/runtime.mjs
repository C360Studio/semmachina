import http from 'node:http';
import https from 'node:https';
import { spawn } from 'node:child_process';

import { cert, key } from '../tls-fixture.mjs';

const CANARY_ID = 'c360.semmachina.isolation.isolation-world.location.canary';
const MAX_REQUEST_BYTES = 16_384;
const nonce = process.env.SEMMACHINA_ISOLATION_NONCE;
if (!/^[a-f0-9]{24}$/.test(nonce ?? '')) throw new Error('invalid isolation nonce');
const canaryLabel = `Isolation Canary ${nonce}`;

function fail() {
	process.stderr.write('surface isolation runtime failed\n');
	process.exitCode = 1;
}

function boundedBody(request) {
	return new Promise((resolve, reject) => {
		let bytes = 0;
		let text = '';
		request.setEncoding('utf8');
		request.on('data', (part) => {
			bytes += Buffer.byteLength(part);
			if (bytes > MAX_REQUEST_BYTES) {
				reject(new Error('request too large'));
				request.destroy();
				return;
			}
			text += part;
		});
		request.on('end', () => resolve(text));
		request.on('error', reject);
	});
}

const graph = http.createServer(async (request, response) => {
	try {
		if (request.method !== 'POST' || request.url !== '/graphql') {
			response.writeHead(404, { 'content-length': '0' });
			response.end();
			return;
		}
		const parsed = JSON.parse(await boundedBody(request));
		const query = typeof parsed?.query === 'string' ? parsed.query : '';
		let data;
		if (query.includes('entitiesByPrefix')) {
			data = {
				entitiesByPrefix: [
					{
						id: CANARY_ID,
						triples: [
							{
								subject: CANARY_ID,
								predicate: 'world.entity.kind',
								object: 'location',
								datatype: 'xsd:string'
							},
							{
								subject: CANARY_ID,
								predicate: 'world.entity.name',
								object: canaryLabel,
								datatype: 'xsd:string'
							}
						]
					}
				]
			};
		} else if (query.includes('relationships')) {
			data = { relationships: [] };
		} else {
			response.writeHead(400, { 'content-length': '0' });
			response.end();
			return;
		}
		const body = JSON.stringify({ data });
		response.writeHead(200, {
			'content-type': 'application/json',
			'content-length': Buffer.byteLength(body),
			'cache-control': 'no-store'
		});
		response.end(body);
	} catch {
		if (!response.headersSent) response.writeHead(400, { 'content-length': '0' });
		response.end();
	}
});

const surface = spawn(process.execPath, ['/workspace/.server-build/server.js'], {
	cwd: '/workspace',
	env: process.env,
	stdio: 'ignore'
});

const proxy = https.createServer({ key, cert }, (incoming, outgoing) => {
	const upstream = http.request(
		{
			host: '127.0.0.1',
			port: 4173,
			method: incoming.method,
			path: incoming.url,
			headers: {
				...incoming.headers,
				host: process.env.SEMMACHINA_PROXY_HOST,
				'x-forwarded-proto': 'https'
			}
		},
		(response) => {
			outgoing.writeHead(response.statusCode ?? 502, response.headers);
			response.pipe(outgoing);
		}
	);
	upstream.on('error', () => {
		if (!outgoing.headersSent) outgoing.writeHead(502, { 'content-length': '0' });
		outgoing.end();
	});
	incoming.pipe(upstream);
});

let stopping = false;
function shutdown(status = 0) {
	if (stopping) return;
	stopping = true;
	graph.close();
	proxy.close();
	if (!surface.killed) surface.kill('SIGTERM');
	setTimeout(() => process.exit(status), 250).unref();
}

surface.once('error', () => {
	fail();
	shutdown(1);
});
surface.once('exit', () => {
	if (!stopping) {
		fail();
		shutdown(1);
	}
});
graph.once('error', () => {
	fail();
	shutdown(1);
});
proxy.once('error', () => {
	fail();
	shutdown(1);
});
process.once('SIGINT', () => shutdown(130));
process.once('SIGTERM', () => shutdown(143));

graph.listen(43102, '127.0.0.1');
proxy.listen(4181, '0.0.0.0');
