import {
	parsePlayerFrame,
	parseRetrieveRequest,
	parseSubmitAction,
	type ProtocolParseFailure,
	type RetrieveRequest,
	type SubmitAction
} from '../player-v1/parser';
import type { SessionEffect, SessionEvent } from './session-machine';

export interface WebSocketLike {
	readonly readyState: number;
	onopen: ((event: Event) => void) | null;
	onclose: ((event: CloseEvent) => void) | null;
	onerror: ((event: Event) => void) | null;
	onmessage: ((event: MessageEvent) => void) | null;
	send(data: string): void;
	close(): void;
}

export type PlayerSocketSend = Extract<
	SessionEffect,
	{ type: 'SendSubmit' | 'SendRetrieveLatest' | 'SendRetrieveExact' }
>;

export interface PlayerSocket {
	open(connectionGeneration: number, sessionCsrf: string): void;
	close(connectionGeneration: number): void;
	send(effect: PlayerSocketSend): void;
	destroy(): void;
}

export interface PlayerSocketDependencies {
	readonly origin: string;
	readonly createWebSocket: (url: string, protocols: string[]) => WebSocketLike;
	readonly emit: (event: SessionEvent) => void;
}

type Pending =
	| {
			readonly kind: 'submit';
			readonly connectionGeneration: number;
			readonly operationGeneration: number;
	  }
	| {
			readonly kind: 'retrieve';
			readonly connectionGeneration: number;
			readonly operationGeneration: number;
			readonly by: RetrieveRequest['by'];
			readonly id?: string;
	  };

interface ActiveSocket {
	readonly socket: WebSocketLike;
	readonly connectionGeneration: number;
	open: boolean;
}

const correlationFailure = (message: string): ProtocolParseFailure => ({
	kind: 'invalid_document',
	path: '$',
	message
});

const errorMessage = (error: unknown): string | undefined =>
	error instanceof Error && error.message !== '' ? error.message : undefined;

export function createPlayerSocket(dependencies: PlayerSocketDependencies): PlayerSocket {
	let active: ActiveSocket | undefined;
	let pending: Pending | undefined;
	let destroyed = false;

	const emitProtocolFailure = (connectionGeneration: number, failure: ProtocolParseFailure) => {
		dependencies.emit({ type: 'ProtocolFailed', connectionGeneration, failure });
	};

	const failEffect = (effect: PlayerSocketSend, message: string) => {
		dependencies.emit({
			type: 'EffectFailed',
			connectionGeneration: effect.connectionGeneration,
			operationGeneration: effect.operationGeneration,
			message
		});
	};

	const handleMessage = (connectionGeneration: number, data: unknown) => {
		if (destroyed) return;
		if (typeof data !== 'string') {
			emitProtocolFailure(connectionGeneration, correlationFailure('player frame must be text'));
			return;
		}
		const parsed = parsePlayerFrame(data);
		if (!parsed.ok) {
			emitProtocolFailure(connectionGeneration, parsed.error);
			return;
		}

		const frame = parsed.value;
		if (frame.type === 'turn_delivery') {
			dependencies.emit({
				type: 'DeliveryReceived',
				connectionGeneration,
				delivery: frame.delivery
			});
			return;
		}

		const operation = pending?.connectionGeneration === connectionGeneration ? pending : undefined;
		if (operation !== undefined) pending = undefined;
		if (frame.type === 'operation_response') {
			emitProtocolFailure(
				connectionGeneration,
				correlationFailure('server refused the pending operation')
			);
			return;
		}
		if (operation === undefined) {
			emitProtocolFailure(
				connectionGeneration,
				correlationFailure('unsolicited operation response')
			);
			return;
		}
		if (frame.type === 'submit_response') {
			if (operation.kind !== 'submit') {
				emitProtocolFailure(
					connectionGeneration,
					correlationFailure('submit response does not match pending retrieval')
				);
				return;
			}
			dependencies.emit({
				type: 'SubmitAnswered',
				connectionGeneration,
				operationGeneration: operation.operationGeneration,
				response: frame.response
			});
			return;
		}

		if (
			operation.kind !== 'retrieve' ||
			frame.retrieval.by !== operation.by ||
			frame.retrieval.id !== operation.id
		) {
			emitProtocolFailure(
				connectionGeneration,
				correlationFailure('retrieve response by/id does not match pending request')
			);
			return;
		}
		dependencies.emit({
			type: 'RetrieveAnswered',
			connectionGeneration,
			operationGeneration: operation.operationGeneration,
			response: frame.retrieval
		});
	};

	return {
		open(connectionGeneration, sessionCsrf) {
			if (destroyed) return;
			const previous = active;
			if (previous !== undefined) {
				previous.open = false;
				if (pending?.connectionGeneration === previous.connectionGeneration) pending = undefined;
				active = undefined;
				try {
					previous.socket.close();
				} catch (error) {
					dependencies.emit({
						type: 'SocketFailed',
						connectionGeneration: previous.connectionGeneration,
						...(errorMessage(error) === undefined ? {} : { message: errorMessage(error) })
					});
				}
			}
			let socket: WebSocketLike;
			try {
				const base = new URL(dependencies.origin);
				base.protocol = base.protocol === 'https:' ? 'wss:' : 'ws:';
				base.pathname = '/api/player';
				base.search = '';
				base.hash = '';
				socket = dependencies.createWebSocket(base.toString(), [
					'semmachina.player.v1',
					`csrf.${sessionCsrf}`
				]);
			} catch (error) {
				dependencies.emit({
					type: 'SocketFailed',
					connectionGeneration,
					...(errorMessage(error) === undefined ? {} : { message: errorMessage(error) })
				});
				return;
			}
			const openedSocket: ActiveSocket = { socket, connectionGeneration, open: false };
			active = openedSocket;
			pending = undefined;
			socket.onopen = () => {
				openedSocket.open = true;
				if (!destroyed) dependencies.emit({ type: 'SocketOpened', connectionGeneration });
			};
			socket.onclose = () => {
				if (destroyed) return;
				openedSocket.open = false;
				if (active?.socket === socket) pending = undefined;
				dependencies.emit({ type: 'SocketClosed', connectionGeneration });
			};
			socket.onerror = () => {
				if (destroyed) return;
				openedSocket.open = false;
				if (active?.socket === socket) pending = undefined;
				dependencies.emit({ type: 'SocketFailed', connectionGeneration });
			};
			socket.onmessage = (event) => handleMessage(connectionGeneration, event.data);
		},

		close(connectionGeneration) {
			if (destroyed || active?.connectionGeneration !== connectionGeneration) return;
			pending = undefined;
			active.open = false;
			try {
				active.socket.close();
			} catch (error) {
				dependencies.emit({
					type: 'SocketFailed',
					connectionGeneration,
					...(errorMessage(error) === undefined ? {} : { message: errorMessage(error) })
				});
			}
		},

		send(effect) {
			if (destroyed) return;
			if (active?.connectionGeneration !== effect.connectionGeneration) {
				failEffect(effect, 'socket generation is not active');
				return;
			}
			if (!active.open || active.socket.readyState !== 1) {
				failEffect(effect, 'socket is not open');
				return;
			}
			if (pending !== undefined) {
				failEffect(effect, 'another solicited operation is pending');
				return;
			}

			let document: SubmitAction | RetrieveRequest;
			let nextPending: Pending;
			if (effect.type === 'SendSubmit') {
				const candidate = {
					protocol: 'player/v1',
					text: effect.text,
					idempotency_key: effect.idempotencyKey
				};
				const validated = parseSubmitAction(candidate);
				if (!validated.ok) {
					failEffect(effect, validated.error.message);
					return;
				}
				document = validated.value;
				nextPending = {
					kind: 'submit',
					connectionGeneration: effect.connectionGeneration,
					operationGeneration: effect.operationGeneration
				};
			} else {
				const candidate =
					effect.type === 'SendRetrieveLatest'
						? { protocol: 'player/v1', type: 'retrieve_result', by: 'latest' }
						: { protocol: 'player/v1', type: 'retrieve_result', by: effect.by, id: effect.id };
				const validated = parseRetrieveRequest(candidate);
				if (!validated.ok) {
					failEffect(effect, validated.error.message);
					return;
				}
				document = validated.value;
				nextPending = {
					kind: 'retrieve',
					connectionGeneration: effect.connectionGeneration,
					operationGeneration: effect.operationGeneration,
					by: validated.value.by,
					...('id' in validated.value ? { id: validated.value.id } : {})
				};
			}

			try {
				active.socket.send(JSON.stringify(document));
				pending = nextPending;
			} catch (error) {
				pending = undefined;
				failEffect(effect, errorMessage(error) ?? 'socket send failed');
			}
		},

		destroy() {
			if (destroyed) return;
			destroyed = true;
			pending = undefined;
			try {
				active?.socket.close();
			} catch {
				// Destruction is terminal; no future event may be dispatched by this adapter.
			}
			if (active !== undefined) {
				active.socket.onopen = null;
				active.socket.onclose = null;
				active.socket.onerror = null;
				active.socket.onmessage = null;
			}
			active = undefined;
		}
	};
}
