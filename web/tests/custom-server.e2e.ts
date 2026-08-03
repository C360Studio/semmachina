import { request as nodeRequest } from 'node:http';
import { expect, test } from '@playwright/test';

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
