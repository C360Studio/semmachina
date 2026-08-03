import { createServer } from 'node:http';
import { WebSocketServer } from 'ws';

const server = createServer((request, response) => {
	response.writeHead(request.url === '/health' ? 200 : 404, { 'content-length': '0' });
	response.end();
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
	websocket.on('message', (data, binary) => {
		if (binary) websocket.close(1003);
		else websocket.send(data, { binary: false, compress: false });
	});
});

server.listen(4180, '127.0.0.1');

function shutdown() {
	for (const socket of sockets.clients) socket.terminate();
	server.close();
}
process.once('SIGTERM', shutdown);
process.once('SIGINT', shutdown);
