import { describe, expect, it, vi } from 'vitest';

import { loadWorld } from './world-client';

const world = {
	places: [
		{
			id: 'world.place.gate',
			label: 'Old Gate',
			position: { latitude: 41.9, longitude: -87.6 },
			connections: ['world.place.square']
		},
		{ id: 'world.place.square', label: 'Square', connections: [] }
	],
	clock: { state: 'configured', label: 'Day', value: 3, unit: 'days' }
} as const;

const response = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json; charset=utf-8' }
	});

describe('world browser client', () => {
	it('loads the exact same-origin endpoint with cookie authority only', async () => {
		const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
			void input;
			void init;
			return response(world);
		});

		await expect(
			loadWorld(fetcher as unknown as typeof fetch, 'https://play.example.test/path?query=ignored')
		).resolves.toEqual(world);
		expect(fetcher).toHaveBeenCalledOnce();
		expect(fetcher).toHaveBeenCalledWith('https://play.example.test/api/world', {
			method: 'GET',
			credentials: 'same-origin',
			cache: 'no-store',
			redirect: 'error'
		});
		expect(fetcher.mock.calls[0][1]?.headers).toBeUndefined();
	});

	it.each([
		['unknown root member', { ...world, authority: 'bearer' }],
		['unknown place member', { ...world, places: [{ ...world.places[0], extra: true }] }],
		['duplicate place identity', { ...world, places: [world.places[0], world.places[0]] }],
		[
			'unknown connection',
			{ ...world, places: [{ ...world.places[0], connections: ['missing'] }] }
		],
		[
			'invalid position',
			{ ...world, places: [{ ...world.places[0], position: { latitude: 91, longitude: 0 } }] }
		],
		['unknown clock member', { ...world, clock: { ...world.clock, extra: true } }],
		['non-finite clock value', { ...world, clock: { ...world.clock, value: '3' } }]
	] as const)('rejects %s in the browser DTO', async (_name, body) => {
		await expect(
			loadWorld(
				vi.fn(async () => response(body)) as unknown as typeof fetch,
				'https://play.example.test'
			)
		).rejects.toThrow('World projection is invalid.');
	});

	it('rejects non-success status before parsing a body', async () => {
		const result = loadWorld(
			vi.fn(async () =>
				response({ error: { code: 'unauthorized' } }, 401)
			) as unknown as typeof fetch,
			'https://play.example.test'
		);

		await expect(result).rejects.toThrow('World projection request failed.');
	});
});
