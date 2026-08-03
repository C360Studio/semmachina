import { describe, expect, it, vi } from 'vitest';

import {
	acceptedFrame,
	deliveryFrame,
	operationRefusedFrame,
	retrievalFoundFrame,
	TURN_ID
} from '../player-v1/fixtures';
import type { SessionEvent } from './session-machine';
import { createPlayerSocket, type WebSocketLike } from './player-socket';

class FakeSocket implements WebSocketLike {
	readyState = 0;
	onopen: ((event: Event) => void) | null = null;
	onclose: ((event: CloseEvent) => void) | null = null;
	onerror: ((event: Event) => void) | null = null;
	onmessage: ((event: MessageEvent) => void) | null = null;
	sent: string[] = [];
	close = vi.fn();
	send(value: string) {
		this.sent.push(value);
	}
	open() {
		this.readyState = 1;
		this.onopen?.(new Event('open'));
	}
	message(data: unknown) {
		this.onmessage?.({ data } as MessageEvent);
	}
}

function setup(origin = 'https://play.example.test/path?x=1#hash') {
	const sockets: FakeSocket[] = [];
	const calls: Array<{ url: string; protocols: string[] }> = [];
	const events: SessionEvent[] = [];
	const player = createPlayerSocket({
		origin,
		createWebSocket(url, protocols) {
			calls.push({ url, protocols: [...protocols] });
			const socket = new FakeSocket();
			sockets.push(socket);
			return socket;
		},
		emit: (event) => events.push(event)
	});
	return { player, sockets, calls, events };
}

describe('player socket', () => {
	it('uses the exact same-host URL and generation-capturing callbacks', () => {
		const { player, sockets, calls, events } = setup();
		player.open(3, 'csrf-value');
		expect(calls).toEqual([
			{
				url: 'wss://play.example.test/api/player',
				protocols: ['semmachina.player.v1', 'csrf.csrf-value']
			}
		]);
		player.open(4, 'next');
		expect(sockets[0].close).toHaveBeenCalledOnce();
		sockets[0].open();
		sockets[1].open();
		expect(events).toEqual([
			{ type: 'SocketOpened', connectionGeneration: 3 },
			{ type: 'SocketOpened', connectionGeneration: 4 }
		]);
	});

	it('encodes only validated submit, latest, and exact documents', () => {
		const { player, sockets } = setup('http://localhost:5173');
		player.open(1, 'csrf');
		sockets[0].open();
		player.send({
			type: 'SendSubmit',
			connectionGeneration: 1,
			operationGeneration: 7,
			text: 'Open the gate',
			idempotencyKey: 'key-1'
		});
		expect(JSON.parse(sockets[0].sent[0])).toEqual({
			protocol: 'player/v1',
			text: 'Open the gate',
			idempotency_key: 'key-1'
		});
		sockets[0].message(JSON.stringify(acceptedFrame));
		player.send({
			type: 'SendRetrieveLatest',
			connectionGeneration: 1,
			operationGeneration: 8
		});
		expect(JSON.parse(sockets[0].sent[1])).toEqual({
			protocol: 'player/v1',
			type: 'retrieve_result',
			by: 'latest'
		});
		sockets[0].message(
			JSON.stringify({
				...retrievalFoundFrame,
				retrieval: { ...retrievalFoundFrame.retrieval, by: 'latest', id: undefined }
			})
		);
		player.send({
			type: 'SendRetrieveExact',
			connectionGeneration: 1,
			operationGeneration: 9,
			by: 'turn',
			id: TURN_ID
		});
		expect(JSON.parse(sockets[0].sent[2])).toEqual({
			protocol: 'player/v1',
			type: 'retrieve_result',
			by: 'turn',
			id: TURN_ID
		});
	});

	it('allows delivery to interleave without consuming the solicited response', () => {
		const { player, sockets, events } = setup();
		player.open(1, 'csrf');
		sockets[0].open();
		player.send({
			type: 'SendSubmit',
			connectionGeneration: 1,
			operationGeneration: 2,
			text: 'Act',
			idempotencyKey: 'key'
		});
		sockets[0].message(JSON.stringify(deliveryFrame));
		sockets[0].message(JSON.stringify(acceptedFrame));
		expect(events.slice(1)).toEqual([
			{ type: 'DeliveryReceived', connectionGeneration: 1, delivery: deliveryFrame.delivery },
			{
				type: 'SubmitAnswered',
				connectionGeneration: 1,
				operationGeneration: 2,
				response: acceptedFrame.response
			}
		]);
	});

	it('rejects binary, malformed, unknown, unsolicited, second-pending, and mismatched replies', () => {
		const { player, sockets, events } = setup();
		player.open(1, 'csrf');
		sockets[0].open();
		sockets[0].message(new Uint8Array([1]));
		sockets[0].message('{');
		sockets[0].message(JSON.stringify({ protocol: 'player/v1', type: 'mystery' }));
		sockets[0].message(JSON.stringify(acceptedFrame));
		player.send({
			type: 'SendRetrieveExact',
			connectionGeneration: 1,
			operationGeneration: 1,
			by: 'turn',
			id: TURN_ID
		});
		player.send({ type: 'SendRetrieveLatest', connectionGeneration: 1, operationGeneration: 2 });
		sockets[0].message(
			JSON.stringify({
				...retrievalFoundFrame,
				retrieval: { ...retrievalFoundFrame.retrieval, by: 'action', id: 'act-1' }
			})
		);
		expect(events.filter((event) => event.type === 'ProtocolFailed')).toHaveLength(5);
		expect(events.filter((event) => event.type === 'EffectFailed')).toHaveLength(1);
		player.send({ type: 'SendRetrieveLatest', connectionGeneration: 1, operationGeneration: 3 });
		expect(sockets[0].sent).toHaveLength(2);
		sockets[0].message(JSON.stringify(operationRefusedFrame));
		const operationFailure = events.at(-1);
		expect(operationFailure).toMatchObject({
			type: 'ProtocolFailed',
			failure: { message: 'server refused the pending operation' }
		});
		expect(JSON.stringify(operationFailure)).not.toContain(
			operationRefusedFrame.operation.refusal.message
		);
		player.send({ type: 'SendRetrieveLatest', connectionGeneration: 1, operationGeneration: 4 });
		expect(sockets[0].sent).toHaveLength(3);
		expect(events.filter((event) => event.type === 'ProtocolFailed')).toHaveLength(6);
	});

	it('does not let an old-generation callback consume the active generation pending response', () => {
		const { player, sockets, events } = setup();
		player.open(1, 'old');
		sockets[0].open();
		player.open(2, 'new');
		sockets[1].open();
		player.send({ type: 'SendRetrieveLatest', connectionGeneration: 2, operationGeneration: 3 });
		sockets[0].onclose?.({} as CloseEvent);
		sockets[1].message(
			JSON.stringify({
				...retrievalFoundFrame,
				retrieval: { ...retrievalFoundFrame.retrieval, by: 'latest', id: undefined }
			})
		);
		expect(events).toContainEqual({ type: 'SocketClosed', connectionGeneration: 1 });
		expect(events).toContainEqual(
			expect.objectContaining({
				type: 'RetrieveAnswered',
				connectionGeneration: 2,
				operationGeneration: 3
			})
		);
	});

	it('contains replacement close failures and still opens the new generation', () => {
		const { player, sockets, events } = setup();
		player.open(1, 'old');
		sockets[0].close.mockImplementation(() => {
			throw new Error('replacement close failed');
		});
		expect(() => player.open(2, 'new')).not.toThrow();
		expect(sockets).toHaveLength(2);
		expect(events).toContainEqual({
			type: 'SocketFailed',
			connectionGeneration: 1,
			message: 'replacement close failed'
		});
	});

	it('reports invalid effects and sync transport failures without throwing', () => {
		const { player, sockets, events } = setup();
		player.open(1, 'csrf');
		sockets[0].open();
		player.send({
			type: 'SendSubmit',
			connectionGeneration: 2,
			operationGeneration: 2,
			text: 'Act',
			idempotencyKey: 'key'
		});
		player.send({
			type: 'SendSubmit',
			connectionGeneration: 1,
			operationGeneration: 3,
			text: ' ',
			idempotencyKey: 'key'
		});
		sockets[0].send = () => {
			throw new Error('boom');
		};
		player.send({ type: 'SendRetrieveLatest', connectionGeneration: 1, operationGeneration: 4 });
		expect(events.filter((event) => event.type === 'EffectFailed')).toHaveLength(3);
	});

	it('tags close/error callbacks, clears pending, closes only matching generation, and destroys', () => {
		const { player, sockets, events } = setup();
		player.open(5, 'csrf');
		sockets[0].open();
		player.send({ type: 'SendRetrieveLatest', connectionGeneration: 5, operationGeneration: 1 });
		sockets[0].onerror?.(new Event('error'));
		player.send({ type: 'SendRetrieveLatest', connectionGeneration: 5, operationGeneration: 2 });
		expect(events.at(-1)).toMatchObject({ type: 'EffectFailed', operationGeneration: 2 });
		player.close(4);
		expect(sockets[0].close).not.toHaveBeenCalled();
		player.close(5);
		expect(sockets[0].close).toHaveBeenCalledOnce();
		sockets[0].onclose?.({} as CloseEvent);
		expect(events).toContainEqual({ type: 'SocketFailed', connectionGeneration: 5 });
		expect(events).toContainEqual({ type: 'SocketClosed', connectionGeneration: 5 });
		player.destroy();
		player.open(6, 'later');
		expect(sockets).toHaveLength(1);
	});

	it('reports construction failures with the supplied generation', () => {
		const events: SessionEvent[] = [];
		const player = createPlayerSocket({
			origin: 'https://example.test',
			createWebSocket: () => {
				throw new Error('constructor rejected');
			},
			emit: (event) => events.push(event)
		});
		player.open(11, 'csrf');
		expect(events).toEqual([
			{ type: 'SocketFailed', connectionGeneration: 11, message: 'constructor rejected' }
		]);
	});
});
