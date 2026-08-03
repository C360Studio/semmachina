import { createServer, request as httpRequest } from 'node:http';
import { createServer as createHttpsServer } from 'node:https';
import { WebSocketServer } from 'ws';

import { cert, key } from './tls-fixture.mjs';

const ACTION_ID = 'act-fixture-7';
const TURN_ID = `turn-${ACTION_ID}`;
const PLAYER_ID = 'c360.semmachina.bellweather.bellweather-maze.player.detective';
const COMPANION_ID = 'c360.semmachina.bellweather.bellweather-maze.character.wren';
const GREENHOUSE_ID = 'c360.semmachina.bellweather.bellweather-maze.location.green';
const MAZE_ID = 'c360.semmachina.bellweather.bellweather-maze.location.maze';
const DISCONNECT_ACTION = 'Disconnect before acceptance';
const RESOLVED_AT = '2026-08-03T14:15:30.123456Z';
const RETRIEVAL_DELAY_MS = 260;
const SUBMIT_DELAY_MS = 260;
const DELIVERY_DELAY_MS = 260;
const SURFACE_PORT = 4173;
const TLS_PROXY_PORT = 4181;

function emptyState(epoch) {
	return {
		epoch,
		submissions: [],
		latestRequests: 0,
		exactRequests: [],
		acceptedResponses: 0,
		intendedDeliveries: 0,
		unrelatedDeliveries: 0,
		duplicateDeliveries: 0,
		disconnectedBeforeAcceptance: 0,
		graphqlRequests: [],
		connections: 0,
		recoveryPending: false,
		originalSubmission: undefined
	};
}

let fixture = emptyState(0);
const pendingTimers = new Set();

function automaticDelivery(actionId, prose) {
	const turnId = `turn-${actionId}`;
	return {
		protocol: 'player/v1',
		result: {
			protocol: 'player/v1',
			turn_id: turnId,
			action_id: actionId,
			player_id: PLAYER_ID,
			phase: 'complete',
			resolution: {
				verdict: {
					plausibility: 'certain',
					risk: 'none',
					consequence: 'none',
					requires_roll: false
				},
				band: 'auto'
			},
			narration_ref: `obj://ARTIFACTS/narration/${turnId}`,
			resolved_at: RESOLVED_AT
		},
		narration: { turn_id: turnId, band: 'auto', prose }
	};
}

function intendedDelivery(prose) {
	const delivery = automaticDelivery(ACTION_ID, prose);
	delivery.result.resolution = {
		verdict: {
			plausibility: 'plausible',
			risk: 'moderate',
			consequence: 'complication',
			requires_roll: true
		},
		band: 'partial',
		roll: {
			mechanic: '2d6-pbta/v1',
			dice: [4, 3],
			modifiers: [{ source: 'assistance', value: 1, note: 'Wren notices the latch' }],
			modifier_total: 1,
			total: 8
		}
	};
	delivery.result.companion_resolution = {
		companion_id: COMPANION_ID,
		kind: 'hint',
		hint_level: 'connect'
	};
	delivery.narration.band = 'partial';
	return delivery;
}

function frame(type, payload, value) {
	return { protocol: 'player/v1', type, [payload]: value };
}

function latestNotFound() {
	return frame('retrieve_response', 'retrieval', {
		protocol: 'player/v1',
		status: 'refused',
		by: 'latest',
		refusal: { code: 'not_found', message: 'no prior player result exists' }
	});
}

function latestUnrelated(delivery) {
	return frame('retrieve_response', 'retrieval', {
		protocol: 'player/v1',
		status: 'found',
		by: 'latest',
		delivery
	});
}

function accepted(submission) {
	return frame('submit_response', 'response', {
		protocol: 'player/v1',
		status: 'accepted',
		idempotency_key: submission.idempotency_key,
		action_id: ACTION_ID,
		turn_id: TURN_ID,
		arrived_at: '2026-08-03T14:15:29.000000Z'
	});
}

function exactFound(request, delivery) {
	return frame('retrieve_response', 'retrieval', {
		protocol: 'player/v1',
		status: 'found',
		by: request.by,
		id: request.id,
		delivery
	});
}

function strictSubmit(document) {
	return (
		document !== null &&
		typeof document === 'object' &&
		!Array.isArray(document) &&
		Object.keys(document).sort().join(',') === 'idempotency_key,protocol,text' &&
		document.protocol === 'player/v1' &&
		typeof document.text === 'string' &&
		typeof document.idempotency_key === 'string'
	);
}

function strictRetrieve(document, by) {
	if (document === null || typeof document !== 'object' || Array.isArray(document)) return false;
	const keys = by === 'latest' ? 'by,protocol,type' : 'by,id,protocol,type';
	return (
		Object.keys(document).sort().join(',') === keys &&
		document.protocol === 'player/v1' &&
		document.type === 'retrieve_result' &&
		document.by === by &&
		(by === 'latest' || typeof document.id === 'string')
	);
}

function sendJson(websocket, document) {
	if (websocket.readyState === 1) {
		websocket.send(JSON.stringify(document), { binary: false, compress: false });
	}
}

function schedule(websocket, delay, operation) {
	const scheduledFixture = fixture;
	const timer = setTimeout(() => {
		pendingTimers.delete(timer);
		if (fixture === scheduledFixture && websocket.readyState === 1) operation();
	}, delay);
	pendingTimers.add(timer);
}

function clearFixtureActivity() {
	for (const timer of pendingTimers) clearTimeout(timer);
	pendingTimers.clear();
	for (const socket of sockets.clients) socket.terminate();
}

function readBody(request) {
	return new Promise((resolve, reject) => {
		const chunks = [];
		request.on('data', (chunk) => chunks.push(chunk));
		request.once('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
		request.once('error', reject);
	});
}

function json(response, status, document) {
	const body = JSON.stringify(document);
	response.writeHead(status, {
		'content-type': 'application/json',
		'content-length': Buffer.byteLength(body),
		'cache-control': 'no-store'
	});
	response.end(body);
}

function location(id, name, extras = []) {
	return {
		id,
		triples: [
			{ subject: id, predicate: 'world.entity.kind', object: 'location', datatype: 'xsd:string' },
			{ subject: id, predicate: 'world.entity.name', object: name, datatype: 'xsd:string' },
			...extras
		]
	};
}

async function handleGraphQL(request, response) {
	let document;
	try {
		document = JSON.parse(await readBody(request));
	} catch {
		json(response, 400, { errors: [{ message: 'invalid fixture request' }] });
		return;
	}
	fixture.graphqlRequests.push({
		query: document.query,
		variables: document.variables,
		authorization: request.headers.authorization ?? null
	});

	if (typeof document.query === 'string' && document.query.includes('entitiesByPrefix')) {
		const green = location(GREENHOUSE_ID, 'Greenhouse', [
			{ subject: GREENHOUSE_ID, predicate: 'geo.location.latitude', object: 51.25 },
			{ subject: GREENHOUSE_ID, predicate: 'geo.location.longitude', object: -0.1 },
			{
				subject: GREENHOUSE_ID,
				predicate: 'location.relation.connects-to',
				object: MAZE_ID,
				datatype: '@id'
			}
		]);
		json(response, 200, {
			data: { entitiesByPrefix: [green, location(MAZE_ID, 'Bellweather Maze')] }
		});
		return;
	}
	if (typeof document.query === 'string' && document.query.includes('relationships')) {
		json(response, 200, {
			data: {
				relationships:
					document.variables?.entityId === GREENHOUSE_ID
						? [
								{
									from: GREENHOUSE_ID,
									to: MAZE_ID,
									predicate: 'location.relation.connects-to'
								}
							]
						: []
			}
		});
		return;
	}
	json(response, 400, { errors: [{ message: 'unexpected fixture query' }] });
}

const server = createServer((request, response) => {
	void (async () => {
		if (request.method === 'GET' && request.url === '/health') {
			response.writeHead(200, { 'content-length': '0' });
			response.end();
			return;
		}
		if (request.method === 'POST' && request.url === '/__fixture/reset') {
			const nextEpoch = fixture.epoch + 1;
			clearFixtureActivity();
			fixture = emptyState(nextEpoch);
			response.writeHead(204);
			response.end();
			return;
		}
		if (request.method === 'GET' && request.url === '/__fixture/state') {
			json(response, 200, { ...fixture, pendingTimers: pendingTimers.size });
			return;
		}
		if (request.method === 'POST' && request.url === '/graphql') {
			await handleGraphQL(request, response);
			return;
		}
		response.writeHead(404, { 'content-length': '0' });
		response.end();
	})().catch(() => {
		if (!response.headersSent) response.writeHead(500, { 'content-length': '0' });
		response.end();
	});
});

function proxyHeaders(headers) {
	const forwarded = { ...headers, 'x-forwarded-proto': 'https' };
	delete forwarded['x-semmachina-internal-transport'];
	return forwarded;
}

const tlsProxy = createHttpsServer({ cert, key }, (request, response) => {
	const upstream = httpRequest(
		{
			host: '127.0.0.1',
			port: SURFACE_PORT,
			method: request.method,
			path: request.url,
			headers: proxyHeaders(request.headers)
		},
		(upstreamResponse) => {
			response.writeHead(upstreamResponse.statusCode ?? 502, upstreamResponse.headers);
			upstreamResponse.pipe(response);
		}
	);
	upstream.once('error', () => {
		if (!response.headersSent) response.writeHead(502, { 'content-length': '0' });
		response.end();
	});
	request.pipe(upstream);
});

tlsProxy.on('upgrade', (request, socket, head) => {
	const upstream = httpRequest({
		host: '127.0.0.1',
		port: SURFACE_PORT,
		method: request.method,
		path: request.url,
		headers: proxyHeaders(request.headers)
	});
	upstream.once('upgrade', (upstreamResponse, upstreamSocket, upstreamHead) => {
		socket.write(
			`HTTP/1.1 ${upstreamResponse.statusCode ?? 101} ${upstreamResponse.statusMessage ?? 'Switching Protocols'}\r\n`
		);
		for (let index = 0; index < upstreamResponse.rawHeaders.length; index += 2) {
			socket.write(
				`${upstreamResponse.rawHeaders[index]}: ${upstreamResponse.rawHeaders[index + 1]}\r\n`
			);
		}
		socket.write('\r\n');
		if (upstreamHead.length > 0) socket.write(upstreamHead);
		if (head.length > 0) upstreamSocket.write(head);
		upstreamSocket.pipe(socket).pipe(upstreamSocket);
	});
	upstream.once('response', (upstreamResponse) => {
		socket.write(
			`HTTP/1.1 ${upstreamResponse.statusCode ?? 502} ${upstreamResponse.statusMessage ?? 'Bad Gateway'}\r\nConnection: close\r\nContent-Length: 0\r\n\r\n`
		);
		socket.destroy();
	});
	upstream.once('error', () => socket.destroy());
	upstream.end();
});

const sockets = new WebSocketServer({
	noServer: true,
	perMessageDeflate: false,
	maxPayload: 262_144
});

server.on('upgrade', (request, socket, head) => {
	const forbidden = [
		'cookie',
		'origin',
		'sec-websocket-protocol',
		'x-forwarded-proto',
		'x-semmachina-internal-transport'
	];
	if (
		request.url !== '/play' ||
		request.headers.authorization !== 'Bearer player-bearer-that-is-distinct' ||
		forbidden.some((name) => request.headers[name] !== undefined)
	) {
		socket.write('HTTP/1.1 401 Unauthorized\r\nConnection: close\r\nContent-Length: 0\r\n\r\n');
		socket.destroy();
		return;
	}
	sockets.handleUpgrade(request, socket, head, (websocket) =>
		sockets.emit('connection', websocket)
	);
});

sockets.on('connection', (websocket) => {
	fixture.connections += 1;
	websocket.on('message', (data, binary) => {
		if (binary) {
			websocket.close(1003);
			return;
		}

		let document;
		try {
			document = JSON.parse(data.toString('utf8'));
		} catch {
			websocket.send(data, { binary: false, compress: false });
			return;
		}

		if (strictRetrieve(document, 'latest')) {
			fixture.latestRequests += 1;
			if (!fixture.recoveryPending) {
				schedule(websocket, RETRIEVAL_DELAY_MS, () => sendJson(websocket, latestNotFound()));
				return;
			}
			const unrelated = automaticDelivery(
				'act-unrelated-evidence',
				'Unrelated activity from this player remains evidence only.'
			);
			schedule(websocket, Math.floor(RETRIEVAL_DELAY_MS / 2), () => {
				fixture.unrelatedDeliveries += 1;
				sendJson(websocket, frame('turn_delivery', 'delivery', unrelated));
			});
			schedule(websocket, RETRIEVAL_DELAY_MS, () =>
				sendJson(websocket, latestUnrelated(unrelated))
			);
			return;
		}

		if (strictSubmit(document)) {
			const submission = structuredClone(document);
			fixture.submissions.push(submission);
			if (document.text === DISCONNECT_ACTION && fixture.originalSubmission === undefined) {
				fixture.originalSubmission = submission;
				fixture.recoveryPending = true;
				schedule(websocket, SUBMIT_DELAY_MS, () => {
					fixture.disconnectedBeforeAcceptance += 1;
					websocket.close(1011, 'fixture disconnect before acceptance');
				});
				return;
			}

			const isReplay = fixture.originalSubmission !== undefined;
			if (isReplay && JSON.stringify(submission) !== JSON.stringify(fixture.originalSubmission)) {
				websocket.close(1002, 'replay payload changed');
				return;
			}
			schedule(websocket, SUBMIT_DELAY_MS, () => {
				fixture.acceptedResponses += 1;
				sendJson(websocket, accepted(submission));
				if (!isReplay) {
					schedule(websocket, DELIVERY_DELAY_MS, () => {
						fixture.intendedDeliveries += 1;
						sendJson(
							websocket,
							frame(
								'turn_delivery',
								'delivery',
								intendedDelivery('Wren points out fresh tool marks beneath the brass latch.')
							)
						);
					});
				}
			});
			return;
		}

		if (strictRetrieve(document, 'turn')) {
			fixture.exactRequests.push(structuredClone(document));
			const delivery = intendedDelivery('The replay resolves the intended brass-latch action.');
			schedule(websocket, RETRIEVAL_DELAY_MS, () => {
				fixture.intendedDeliveries += 1;
				sendJson(websocket, exactFound(document, delivery));
				fixture.duplicateDeliveries += 1;
				sendJson(websocket, frame('turn_delivery', 'delivery', delivery));
				schedule(websocket, Math.floor(DELIVERY_DELAY_MS / 2), () => {
					fixture.duplicateDeliveries += 1;
					sendJson(websocket, frame('turn_delivery', 'delivery', delivery));
				});
			});
			return;
		}

		websocket.send(data, { binary: false, compress: false });
	});
});

server.listen(4180, '127.0.0.1');
tlsProxy.listen(TLS_PROXY_PORT, '127.0.0.1');

function shutdown() {
	clearFixtureActivity();
	tlsProxy.close();
	server.close();
}
process.once('SIGTERM', shutdown);
process.once('SIGINT', shutdown);
