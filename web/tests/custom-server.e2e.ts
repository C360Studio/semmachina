import { request as nodeRequest } from 'node:http';
import { expect, test } from '@playwright/test';
import WebSocket from 'ws';

const baseURL = 'http://127.0.0.1:4173';
const proxyHeaders = {
	host: 'play.example.test',
	'x-forwarded-proto': 'https'
};

test('custom entry shares its installed default-deny runtime with the lazy world route', async ({
	request
}) => {
	const response = await request.get(`${baseURL}/api/world`, { headers: proxyHeaders });
	expect(response.status()).toBe(401);
	expect(await response.json()).toEqual({ error: { code: 'unauthorized' } });
});

test('custom entry explicitly refuses WebSocket upgrades', async () => {
	const status = await new Promise<number>((resolve, reject) => {
		const request = nodeRequest(`${baseURL}/api/player`, {
			headers: {
				...proxyHeaders,
				origin: 'https://play.example.test',
				connection: 'Upgrade',
				upgrade: 'websocket'
			}
		});
		request.once('response', (response) => resolve(response.statusCode ?? 0));
		request.once('upgrade', () => reject(new Error('unexpected upgrade acceptance')));
		request.once('error', reject);
		request.end();
	});
	expect(status).toBe(426);
});

function cookie(header: string, name: string): string {
	const match = header.match(new RegExp(`${name}=[A-Za-z0-9_-]+`));
	if (match === null) throw new Error(`missing ${name}`);
	return match[0];
}

test('production bridge confines upstream headers and relays fragmented text bytes unchanged', async ({
	request
}) => {
	const authHeaders = { ...proxyHeaders, origin: 'https://play.example.test' };
	const preauth = await request.post(`${baseURL}/api/auth/preauth`, { headers: authHeaders });
	expect(preauth.status()).toBe(200);
	const preauthCsrf = ((await preauth.json()) as { csrf: string }).csrf;
	const preauthCookie = cookie(preauth.headers()['set-cookie'] ?? '', '__Host-semmachina_preauth');
	const login = await request.post(`${baseURL}/api/auth/login`, {
		headers: { ...authHeaders, cookie: preauthCookie },
		data: { credential: 'creator-secret-that-is-long', csrf: preauthCsrf }
	});
	expect(login.status()).toBe(200);
	const sessionCsrf = ((await login.json()) as { csrf: string }).csrf;
	const sessionCookie = cookie(login.headers()['set-cookie'] ?? '', '__Host-semmachina_session');

	const received = await new Promise<Buffer>((resolve, reject) => {
		const websocket = new WebSocket(
			`${baseURL.replace('http:', 'ws:')}/api/player`,
			['semmachina.player.v1', `csrf.${sessionCsrf}`],
			{
				headers: { ...authHeaders, cookie: sessionCookie },
				perMessageDeflate: false
			}
		);
		const expected = Buffer.from('Midsomer fragmented \u{1f50d} exact bytes', 'utf8');
		websocket.once('error', reject);
		websocket.once('unexpected-response', (_request, response) =>
			reject(new Error(`unexpected relay response ${response.statusCode ?? 0}`))
		);
		websocket.once('close', (code) => {
			if (code !== 1000) reject(new Error(`relay closed before echo with code ${code}`));
		});
		websocket.once('open', () => {
			try {
				expect(websocket.protocol).toBe('semmachina.player.v1');
				expect(websocket.extensions).toBe('');
				websocket.send(expected.subarray(0, 21), { binary: false, fin: false });
				websocket.send(expected.subarray(21), { binary: false, fin: true });
			} catch (error) {
				reject(error);
			}
		});
		websocket.once('message', (data, binary) => {
			try {
				expect(binary).toBe(false);
				const bytes = Buffer.isBuffer(data) ? data : Buffer.from(data as ArrayBuffer);
				expect(bytes).toEqual(expected);
				websocket.close(1000);
				resolve(bytes);
			} catch (error) {
				reject(error);
			}
		});
	});
	expect(received.byteLength).toBeGreaterThan(0);
});
