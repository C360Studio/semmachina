import http from 'node:http';
import https from 'node:https';
import net from 'node:net';

import { cert, key } from './tls-fixture.mjs';

const upstreamHost = '127.0.0.1';
const upstreamPort = 4173;
const publicHost = '127.0.0.1:4181';

function forwardedHeaders(headers) {
	return { ...headers, host: publicHost, 'x-forwarded-proto': 'https' };
}

const server = https.createServer({ cert, key }, (request, response) => {
	const upstream = http.request(
		{
			host: upstreamHost,
			port: upstreamPort,
			method: request.method,
			path: request.url,
			headers: forwardedHeaders(request.headers)
		},
		(upstreamResponse) => {
			response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
			upstreamResponse.pipe(response);
		}
	);
	upstream.once('error', () => {
		if (!response.headersSent) response.writeHead(502);
		response.end();
	});
	request.pipe(upstream);
});

server.on('upgrade', (request, socket, head) => {
	const upstream = net.connect(upstreamPort, upstreamHost);
	upstream.once('connect', () => {
		const headers = forwardedHeaders(request.headers);
		upstream.write(
			`${request.method ?? 'GET'} ${request.url ?? '/'} HTTP/${request.httpVersion}\r\n${Object.entries(
				headers
			)
				.flatMap(([name, value]) =>
					(Array.isArray(value) ? value : [value]).map((item) => `${name}: ${item ?? ''}\r\n`)
				)
				.join('')}\r\n`
		);
		if (head.length !== 0) upstream.write(head);
		socket.pipe(upstream).pipe(socket);
	});
	upstream.once('error', () => socket.destroy());
});

server.listen(4181, '127.0.0.1');

function stop() {
	server.close(() => process.exit(0));
}
process.once('SIGINT', stop);
process.once('SIGTERM', stop);
