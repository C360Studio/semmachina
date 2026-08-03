import type {
	ProtocolParseFailure,
	RetrieveResponse,
	SubmitRefusal,
	SubmitResponse,
	TurnDelivery
} from '../player-v1/parser';

export type SessionTag =
	| 'signed_out'
	| 'authenticating'
	| 'connecting'
	| 'idle'
	| 'watermarking'
	| 'submitting'
	| 'waiting'
	| 'reconnecting'
	| 'recovering_exact'
	| 'recovering_evidence'
	| 'recovery_required'
	| 'refused'
	| 'terminal'
	| 'protocol_error';

export type Watermark = 'empty' | { readonly actionId: string; readonly turnId: string };

export interface IntentDraft {
	readonly text: string;
	readonly idempotencyKey: string;
}

export interface WatermarkedIntent extends IntentDraft {
	readonly watermark: Watermark;
}

export interface IdentifiedIntent extends WatermarkedIntent {
	readonly ids: { readonly actionId: string; readonly turnId: string };
}

export type Intent = IntentDraft | WatermarkedIntent | IdentifiedIntent;
type RetrieveRefusal = Extract<RetrieveResponse, { status: 'refused' }>['refusal'];

export interface RecoveryEvidence {
	readonly latestChanged?: boolean;
	readonly latest?: { readonly actionId: string; readonly turnId: string };
	readonly latestRefusal?: RetrieveRefusal;
	readonly deliveries?: readonly { readonly actionId: string; readonly turnId: string }[];
}

type SubmitOperation = {
	readonly kind: 'submit';
	readonly generation: number;
	readonly replay: boolean;
};
type LatestOperation = {
	readonly kind: 'latest';
	readonly generation: number;
	readonly purpose: 'watermark' | 'evidence';
};
type ExactOperation = {
	readonly kind: 'exact';
	readonly generation: number;
	readonly by: 'turn' | 'action';
	readonly id: string;
};
type OutstandingOperation = SubmitOperation | LatestOperation | ExactOperation;

export type ResumeContext =
	| { readonly kind: 'idle' }
	| { readonly kind: 'watermark'; readonly intent: IntentDraft }
	| {
			readonly kind: 'unknown';
			readonly intent: WatermarkedIntent;
			readonly evidence?: RecoveryEvidence;
			readonly replayRefusal?: SubmitRefusal;
	  }
	| { readonly kind: 'exact'; readonly intent: IdentifiedIntent }
	| {
			readonly kind: 'terminal';
			readonly intent: IdentifiedIntent;
			readonly delivery: TurnDelivery;
	  }
	| {
			readonly kind: 'refused';
			readonly intent: IntentDraft | WatermarkedIntent;
			readonly refusal: SubmitRefusal | RetrieveRefusal;
	  };

interface Counters {
	readonly authenticationGeneration: number;
	readonly connectionGeneration: number;
	readonly operationGeneration: number;
}

interface AuthenticatedCounters extends Counters {
	readonly sessionCsrf: string;
}

interface WithoutIntent {
	readonly intent?: never;
}

interface WithoutOperation {
	readonly operation?: never;
}

interface WithoutDelivery {
	readonly delivery?: never;
	readonly pendingExact?: never;
}

export type SessionState =
	| (Counters &
			WithoutIntent &
			WithoutOperation &
			WithoutDelivery & {
				readonly tag: 'signed_out';
				readonly authenticationRefusal?: AuthenticationRefusal;
			})
	| (Counters &
			WithoutIntent &
			WithoutOperation &
			WithoutDelivery & { readonly tag: 'authenticating' })
	| (AuthenticatedCounters &
			WithoutIntent &
			WithoutOperation &
			WithoutDelivery & { readonly tag: 'connecting'; readonly resume: ResumeContext })
	| (AuthenticatedCounters &
			WithoutIntent &
			WithoutOperation &
			WithoutDelivery & { readonly tag: 'idle' })
	| (AuthenticatedCounters &
			WithoutDelivery & {
				readonly tag: 'watermarking';
				readonly intent: IntentDraft;
				readonly operation: LatestOperation & { readonly purpose: 'watermark' };
			})
	| (AuthenticatedCounters &
			WithoutDelivery & {
				readonly tag: 'submitting';
				readonly intent: WatermarkedIntent;
				readonly operation: SubmitOperation;
			})
	| (AuthenticatedCounters &
			WithoutOperation &
			WithoutDelivery & { readonly tag: 'waiting'; readonly intent: IdentifiedIntent })
	| (AuthenticatedCounters &
			WithoutIntent &
			WithoutOperation &
			WithoutDelivery & {
				readonly tag: 'reconnecting';
				readonly resume: ResumeContext;
				readonly transportFailure?: string;
			})
	| (AuthenticatedCounters &
			WithoutDelivery & {
				readonly tag: 'recovering_exact';
				readonly intent: IdentifiedIntent;
				readonly operation: ExactOperation | null;
				readonly refusal?: RetrieveRefusal;
			})
	| (AuthenticatedCounters &
			WithoutDelivery & {
				readonly tag: 'recovering_evidence';
				readonly intent: WatermarkedIntent;
				readonly operation: LatestOperation & { readonly purpose: 'evidence' };
				readonly evidence?: RecoveryEvidence;
				readonly replayRefusal?: SubmitRefusal;
			})
	| (AuthenticatedCounters &
			WithoutOperation &
			WithoutDelivery & {
				readonly tag: 'recovery_required';
				readonly intent: WatermarkedIntent;
				readonly evidence?: RecoveryEvidence;
				readonly replayRefusal?: SubmitRefusal;
				readonly replayExplanation: string;
			})
	| (AuthenticatedCounters &
			WithoutOperation &
			WithoutDelivery & {
				readonly tag: 'refused';
				readonly intent: IntentDraft | WatermarkedIntent;
				readonly refusal: SubmitRefusal | RetrieveRefusal;
			})
	| (AuthenticatedCounters & {
			readonly tag: 'terminal';
			readonly intent: IdentifiedIntent;
			readonly delivery: TurnDelivery;
			readonly operation?: never;
			readonly pendingExact?: ExactOperation;
	  })
	| (AuthenticatedCounters &
			WithoutIntent &
			WithoutOperation &
			WithoutDelivery & {
				readonly tag: 'protocol_error';
				readonly protocolFailure:
					ProtocolParseFailure | { readonly kind: 'correlation'; readonly message: string };
			})
	| (Counters &
			WithoutIntent &
			WithoutOperation &
			WithoutDelivery & {
				readonly tag: 'protocol_error';
				readonly sessionCsrf?: never;
				readonly protocolFailure: { readonly kind: 'correlation'; readonly message: string };
			});

export interface AuthenticationRefusal {
	readonly code: 'authentication_refused';
	readonly message: string;
}

export type SessionEvent =
	| { readonly type: 'AuthenticateRequested'; readonly credential: string }
	| {
			readonly type: 'Authenticated';
			readonly authenticationGeneration: number;
			readonly sessionCsrf: string;
	  }
	| {
			readonly type: 'AuthenticationRefused';
			readonly authenticationGeneration: number;
			readonly refusal: AuthenticationRefusal;
	  }
	| { readonly type: 'IntentCreated'; readonly text: string; readonly idempotencyKey: string }
	| { readonly type: 'SocketOpened'; readonly connectionGeneration: number }
	| { readonly type: 'SocketClosed'; readonly connectionGeneration: number }
	| {
			readonly type: 'SocketFailed';
			readonly connectionGeneration: number;
			readonly message?: string;
	  }
	| {
			readonly type: 'SubmitAnswered';
			readonly connectionGeneration: number;
			readonly operationGeneration: number;
			readonly response: SubmitResponse;
	  }
	| {
			readonly type: 'RetrieveAnswered';
			readonly connectionGeneration: number;
			readonly operationGeneration: number;
			readonly response: RetrieveResponse;
	  }
	| {
			readonly type: 'DeliveryReceived';
			readonly connectionGeneration: number;
			readonly delivery: TurnDelivery;
	  }
	| {
			readonly type: 'ProtocolFailed';
			readonly connectionGeneration: number;
			readonly failure: ProtocolParseFailure;
	  }
	| {
			readonly type: 'EffectFailed';
			readonly connectionGeneration: number;
			readonly operationGeneration?: number;
			readonly message: string;
	  }
	| { readonly type: 'ReplayAuthorized' }
	| { readonly type: 'ReconnectRequested' }
	| { readonly type: 'CheckExactRequested' }
	| { readonly type: 'RefusalAcknowledged' }
	| { readonly type: 'TerminalAcknowledged' }
	| { readonly type: 'LogoutRequested' };

export type SessionEffect =
	| {
			readonly type: 'Authenticate';
			readonly authenticationGeneration: number;
			readonly credential: string;
	  }
	| { readonly type: 'CancelAuthentication'; readonly authenticationGeneration: number }
	| { readonly type: 'CloseSocket'; readonly connectionGeneration: number }
	| { readonly type: 'Logout'; readonly sessionCsrf: string }
	| {
			readonly type: 'OpenSocket';
			readonly connectionGeneration: number;
			readonly sessionCsrf: string;
	  }
	| {
			readonly type: 'SendSubmit';
			readonly connectionGeneration: number;
			readonly operationGeneration: number;
			readonly text: string;
			readonly idempotencyKey: string;
	  }
	| {
			readonly type: 'SendRetrieveLatest';
			readonly connectionGeneration: number;
			readonly operationGeneration: number;
	  }
	| {
			readonly type: 'SendRetrieveExact';
			readonly connectionGeneration: number;
			readonly operationGeneration: number;
			readonly by: 'turn' | 'action';
			readonly id: string;
	  };

export interface SessionTransition {
	readonly state: SessionState;
	readonly effects: readonly SessionEffect[];
}

const REPLAY_EXPLANATION =
	'Authorize replay only if you want to continue: an accepted original converges idempotently to the same result; otherwise this may submit the action now.';

export function createSessionState(): SessionState {
	return {
		tag: 'signed_out',
		authenticationGeneration: 0,
		connectionGeneration: 0,
		operationGeneration: 0
	};
}

function unchanged(state: SessionState): SessionTransition {
	return { state, effects: [] };
}

function changed(state: SessionState, effects: readonly SessionEffect[] = []): SessionTransition {
	return { state, effects };
}

function protocolError(
	state: Extract<SessionState, { sessionCsrf: string }>,
	message: string
): SessionTransition {
	return changed({
		tag: 'protocol_error',
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf,
		protocolFailure: { kind: 'correlation', message }
	});
}

function authenticationProtocolError(state: SessionState, message: string): SessionTransition {
	const common = {
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		protocolFailure: { kind: 'correlation' as const, message }
	};
	if ('sessionCsrf' in state && state.sessionCsrf !== undefined) {
		return changed({ tag: 'protocol_error', ...common, sessionCsrf: state.sessionCsrf });
	}
	return changed({ tag: 'protocol_error', ...common });
}

function terminalIdentity(value: TurnDelivery): { actionId: string; turnId: string } {
	return { actionId: value.result.action_id, turnId: value.result.turn_id };
}

function sameKnownTerminal(left: TurnDelivery, right: TurnDelivery): boolean {
	return JSON.stringify(left) === JSON.stringify(right);
}

function clearPendingExact(
	state: Extract<SessionState, { tag: 'terminal' }>
): Extract<SessionState, { tag: 'terminal' }> {
	return {
		tag: 'terminal',
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf,
		intent: state.intent,
		delivery: state.delivery
	};
}

function exactIntentDelivery(intent: IdentifiedIntent, value: TurnDelivery): boolean {
	return (
		intent.ids.actionId === value.result.action_id && intent.ids.turnId === value.result.turn_id
	);
}

function recordDeliveryEvidence(
	evidence: RecoveryEvidence | undefined,
	value: TurnDelivery
): RecoveryEvidence {
	const identity = terminalIdentity(value);
	const deliveries = evidence?.deliveries ?? [];
	if (
		deliveries.some(
			(item) => item.actionId === identity.actionId && item.turnId === identity.turnId
		)
	) {
		return evidence ?? { deliveries };
	}
	return { ...evidence, deliveries: [...deliveries, identity] };
}

function latestEffect(state: AuthenticatedCounters, generation: number): SessionEffect {
	return {
		type: 'SendRetrieveLatest',
		connectionGeneration: state.connectionGeneration,
		operationGeneration: generation
	};
}

function startWatermark(state: AuthenticatedCounters, intent: IntentDraft): SessionTransition {
	const generation = state.operationGeneration + 1;
	return changed(
		{
			tag: 'watermarking',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: generation,
			sessionCsrf: state.sessionCsrf,
			intent,
			operation: { kind: 'latest', generation, purpose: 'watermark' }
		},
		[latestEffect(state, generation)]
	);
}

function startEvidence(
	state: AuthenticatedCounters,
	intent: WatermarkedIntent,
	evidence?: RecoveryEvidence,
	replayRefusal?: SubmitRefusal
): SessionTransition {
	const generation = state.operationGeneration + 1;
	return changed(
		{
			tag: 'recovering_evidence',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: generation,
			sessionCsrf: state.sessionCsrf,
			intent,
			evidence,
			replayRefusal,
			operation: { kind: 'latest', generation, purpose: 'evidence' }
		},
		[latestEffect(state, generation)]
	);
}

function startExact(state: AuthenticatedCounters, intent: IdentifiedIntent): SessionTransition {
	const generation = state.operationGeneration + 1;
	const operation: ExactOperation = {
		kind: 'exact',
		generation,
		by: 'turn',
		id: intent.ids.turnId
	};
	return changed(
		{
			tag: 'recovering_exact',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: generation,
			sessionCsrf: state.sessionCsrf,
			intent,
			operation
		},
		[
			{
				type: 'SendRetrieveExact',
				connectionGeneration: state.connectionGeneration,
				operationGeneration: generation,
				by: operation.by,
				id: operation.id
			}
		]
	);
}

function startSubmit(
	state: AuthenticatedCounters,
	intent: WatermarkedIntent,
	replay: boolean
): SessionTransition {
	const generation = state.operationGeneration + 1;
	return changed(
		{
			tag: 'submitting',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: generation,
			sessionCsrf: state.sessionCsrf,
			intent,
			operation: { kind: 'submit', generation, replay }
		},
		[
			{
				type: 'SendSubmit',
				connectionGeneration: state.connectionGeneration,
				operationGeneration: generation,
				text: intent.text,
				idempotencyKey: intent.idempotencyKey
			}
		]
	);
}

function resumeContext(state: SessionState): ResumeContext | undefined {
	switch (state.tag) {
		case 'connecting':
		case 'reconnecting':
			return state.resume;
		case 'idle':
			return { kind: 'idle' };
		case 'watermarking':
			return { kind: 'watermark', intent: state.intent };
		case 'submitting':
			return { kind: 'unknown', intent: state.intent };
		case 'waiting':
		case 'recovering_exact':
			return { kind: 'exact', intent: state.intent };
		case 'recovering_evidence':
			return {
				kind: 'unknown',
				intent: state.intent,
				evidence: state.evidence,
				replayRefusal: state.replayRefusal
			};
		case 'recovery_required':
			return {
				kind: 'unknown',
				intent: state.intent,
				evidence: state.evidence,
				replayRefusal: state.replayRefusal
			};
		case 'refused':
			return { kind: 'refused', intent: state.intent, refusal: state.refusal };
		case 'terminal':
			return { kind: 'terminal', intent: state.intent, delivery: state.delivery };
		case 'protocol_error':
		case 'signed_out':
		case 'authenticating':
			return undefined;
	}
}

function disconnect(
	state: SessionState,
	connectionGeneration: number,
	message?: string
): SessionTransition {
	if (state.tag === 'reconnecting') return unchanged(state);
	if (!hasSessionCsrf(state) || connectionGeneration !== state.connectionGeneration) {
		return unchanged(state);
	}
	const resume = resumeContext(state);
	if (resume === undefined) return unchanged(state);
	return changed({
		tag: 'reconnecting',
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf,
		resume,
		transportFailure: message
	});
}

function openResume(state: Extract<SessionState, { tag: 'connecting' }>): SessionTransition {
	const counters: AuthenticatedCounters = {
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf
	};
	switch (state.resume.kind) {
		case 'idle':
			return changed({ tag: 'idle', ...counters });
		case 'watermark':
			return startWatermark(counters, state.resume.intent);
		case 'unknown':
			return startEvidence(
				counters,
				state.resume.intent,
				state.resume.evidence,
				state.resume.replayRefusal
			);
		case 'exact':
			return startExact(counters, state.resume.intent);
		case 'refused':
			return changed({
				tag: 'refused',
				...counters,
				intent: state.resume.intent,
				refusal: state.resume.refusal
			});
		case 'terminal':
			return changed({
				tag: 'terminal',
				...counters,
				intent: state.resume.intent,
				delivery: state.resume.delivery
			});
	}
}

function currentOperation(state: SessionState): OutstandingOperation | undefined {
	switch (state.tag) {
		case 'watermarking':
		case 'submitting':
		case 'recovering_evidence':
			return state.operation;
		case 'recovering_exact':
			return state.operation ?? undefined;
		case 'terminal':
			return state.pendingExact;
		default:
			return undefined;
	}
}

function correlatedOperation(
	state: Extract<SessionState, { sessionCsrf: string }>,
	connectionGeneration: number,
	operationGeneration: number
): OutstandingOperation | 'stale' | 'invalid' {
	if (connectionGeneration !== state.connectionGeneration) return 'stale';
	if (operationGeneration < state.operationGeneration) return 'stale';
	const operation = currentOperation(state);
	if (operation === undefined || operation.generation !== operationGeneration) return 'invalid';
	return operation;
}

function matchesRetrieval(
	operation: LatestOperation | ExactOperation,
	response: RetrieveResponse
): boolean {
	if (operation.kind === 'latest') return response.by === 'latest' && response.id === undefined;
	return response.by === operation.by && response.id === operation.id;
}

function reduceWatermarkResponse(
	state: Extract<SessionState, { tag: 'watermarking' }>,
	response: RetrieveResponse
): SessionTransition {
	if (response.status === 'found') {
		return startSubmit(
			state,
			{
				...state.intent,
				watermark: terminalIdentity(response.delivery)
			},
			false
		);
	}
	if (response.refusal.code === 'not_found') {
		return startSubmit(state, { ...state.intent, watermark: 'empty' }, false);
	}
	return changed({
		tag: 'refused',
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf,
		intent: state.intent,
		refusal: response.refusal
	});
}

function recoveryRequired(
	state: AuthenticatedCounters,
	intent: WatermarkedIntent,
	evidence?: RecoveryEvidence,
	replayRefusal?: SubmitRefusal
): SessionTransition {
	return changed({
		tag: 'recovery_required',
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf,
		intent,
		evidence,
		replayRefusal,
		replayExplanation: REPLAY_EXPLANATION
	});
}

function reduceEvidenceResponse(
	state: Extract<SessionState, { tag: 'recovering_evidence' }>,
	response: RetrieveResponse
): SessionTransition {
	if (response.status === 'refused' && response.refusal.code !== 'not_found') {
		return recoveryRequired(
			state,
			state.intent,
			{
				...state.evidence,
				latestRefusal: response.refusal
			},
			state.replayRefusal
		);
	}
	const latest = response.status === 'found' ? terminalIdentity(response.delivery) : undefined;
	const watermark = state.intent.watermark;
	const latestChanged =
		latest !== undefined &&
		(watermark === 'empty' ||
			watermark.actionId !== latest.actionId ||
			watermark.turnId !== latest.turnId);
	return recoveryRequired(
		state,
		state.intent,
		{
			...state.evidence,
			latest,
			latestChanged
		},
		state.replayRefusal
	);
}

function reduceRetrieve(
	state: Extract<SessionState, { sessionCsrf: string }>,
	event: Extract<SessionEvent, { type: 'RetrieveAnswered' }>
): SessionTransition {
	const operation = correlatedOperation(
		state,
		event.connectionGeneration,
		event.operationGeneration
	);
	if (operation === 'stale') return unchanged(state);
	if (operation === 'invalid' || operation.kind === 'submit') {
		return protocolError(state, 'retrieve response does not match the outstanding operation');
	}
	if (!matchesRetrieval(operation, event.response)) {
		return protocolError(state, 'retrieve response by/id does not match the request');
	}
	if (state.tag === 'terminal') {
		if (event.response.status === 'found') {
			if (!exactIntentDelivery(state.intent, event.response.delivery)) {
				return protocolError(state, 'exact retrieval returned a different terminal identity');
			}
			if (!sameKnownTerminal(state.delivery, event.response.delivery)) {
				return protocolError(state, 'conflicting terminal content for one identity');
			}
		}
		return changed(clearPendingExact(state));
	}
	if (state.tag === 'watermarking') return reduceWatermarkResponse(state, event.response);
	if (state.tag === 'recovering_evidence') return reduceEvidenceResponse(state, event.response);
	if (state.tag !== 'recovering_exact') {
		return protocolError(state, 'retrieve response arrived in an incompatible phase');
	}
	if (event.response.status === 'refused') {
		return changed({
			tag: 'recovering_exact',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: state.operationGeneration,
			sessionCsrf: state.sessionCsrf,
			intent: state.intent,
			operation: null,
			refusal: event.response.refusal
		});
	}
	if (!exactIntentDelivery(state.intent, event.response.delivery)) {
		return protocolError(state, 'exact retrieval returned a different terminal identity');
	}
	return changed({
		tag: 'terminal',
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf,
		intent: state.intent,
		delivery: event.response.delivery
	});
}

function reduceSubmit(
	state: Extract<SessionState, { sessionCsrf: string }>,
	event: Extract<SessionEvent, { type: 'SubmitAnswered' }>
): SessionTransition {
	const operation = correlatedOperation(
		state,
		event.connectionGeneration,
		event.operationGeneration
	);
	if (operation === 'stale') return unchanged(state);
	if (operation === 'invalid' || operation.kind !== 'submit' || state.tag !== 'submitting') {
		return protocolError(state, 'submit response does not match the outstanding operation');
	}
	if (
		(event.response.status === 'accepted' || event.response.idempotency_key !== undefined) &&
		event.response.idempotency_key !== state.intent.idempotencyKey
	) {
		return protocolError(state, 'submit response idempotency key does not match the intent');
	}
	if (event.response.status === 'accepted') {
		const intent: IdentifiedIntent = {
			...state.intent,
			ids: { actionId: event.response.action_id, turnId: event.response.turn_id }
		};
		if (operation.replay) return startExact(state, intent);
		return changed({
			tag: 'waiting',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: state.operationGeneration,
			sessionCsrf: state.sessionCsrf,
			intent
		});
	}
	if (operation.replay || event.response.refusal.code === 'turn_in_progress') {
		return recoveryRequired(state, state.intent, undefined, event.response.refusal);
	}
	return changed({
		tag: 'refused',
		authenticationGeneration: state.authenticationGeneration,
		connectionGeneration: state.connectionGeneration,
		operationGeneration: state.operationGeneration,
		sessionCsrf: state.sessionCsrf,
		intent: state.intent,
		refusal: event.response.refusal
	});
}

function reduceDelivery(
	state: Extract<SessionState, { sessionCsrf: string }>,
	delivery: TurnDelivery
): SessionTransition {
	if (state.tag === 'terminal') {
		const current = terminalIdentity(state.delivery);
		const incoming = terminalIdentity(delivery);
		if (current.actionId !== incoming.actionId || current.turnId !== incoming.turnId) {
			return unchanged(state);
		}
		return sameKnownTerminal(state.delivery, delivery)
			? unchanged(state)
			: protocolError(state, 'conflicting terminal content for one identity');
	}
	if (state.tag === 'waiting' && exactIntentDelivery(state.intent, delivery)) {
		return changed({
			tag: 'terminal',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: state.operationGeneration,
			sessionCsrf: state.sessionCsrf,
			intent: state.intent,
			delivery
		});
	}
	if (state.tag === 'recovering_exact' && exactIntentDelivery(state.intent, delivery)) {
		return changed({
			tag: 'terminal',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: state.operationGeneration,
			sessionCsrf: state.sessionCsrf,
			intent: state.intent,
			delivery,
			...(state.operation === null ? {} : { pendingExact: state.operation })
		});
	}
	if (state.tag === 'recovering_evidence') {
		return changed({
			tag: 'recovering_evidence',
			authenticationGeneration: state.authenticationGeneration,
			connectionGeneration: state.connectionGeneration,
			operationGeneration: state.operationGeneration,
			sessionCsrf: state.sessionCsrf,
			intent: state.intent,
			operation: state.operation,
			evidence: recordDeliveryEvidence(state.evidence, delivery),
			replayRefusal: state.replayRefusal
		});
	}
	if (state.tag === 'recovery_required') {
		return recoveryRequired(
			state,
			state.intent,
			recordDeliveryEvidence(state.evidence, delivery),
			state.replayRefusal
		);
	}
	return unchanged(state);
}

function reduceEffectFailure(
	state: Extract<SessionState, { sessionCsrf: string }>,
	event: Extract<SessionEvent, { type: 'EffectFailed' }>
): SessionTransition {
	if (event.connectionGeneration !== state.connectionGeneration) return unchanged(state);
	if (event.operationGeneration === undefined) {
		if (state.tag === 'connecting') {
			return disconnect(state, event.connectionGeneration, event.message);
		}
		return protocolError(state, 'effect failure is missing its operation generation');
	}
	if (event.operationGeneration < state.operationGeneration) return unchanged(state);
	const operation = currentOperation(state);
	if (operation === undefined || operation.generation !== event.operationGeneration) {
		return protocolError(state, 'effect failure does not match the outstanding operation');
	}
	if (state.tag === 'terminal') {
		return changed(clearPendingExact(state));
	}
	return disconnect(state, event.connectionGeneration, event.message);
}

function hasSessionCsrf(
	state: SessionState
): state is Extract<SessionState, { sessionCsrf: string }> {
	return 'sessionCsrf' in state && typeof state.sessionCsrf === 'string';
}

export function reduceSession(state: SessionState, event: SessionEvent): SessionTransition {
	switch (event.type) {
		case 'AuthenticateRequested':
			if (state.tag !== 'signed_out') return unchanged(state);
			{
				const authenticationGeneration = state.authenticationGeneration + 1;
				return changed(
					{
						tag: 'authenticating',
						authenticationGeneration,
						connectionGeneration: state.connectionGeneration,
						operationGeneration: state.operationGeneration
					},
					[{ type: 'Authenticate', authenticationGeneration, credential: event.credential }]
				);
			}
		case 'Authenticated': {
			if (event.authenticationGeneration < state.authenticationGeneration) {
				if (hasSessionCsrf(state)) {
					return changed(
						{
							tag: 'signed_out',
							authenticationGeneration: state.authenticationGeneration,
							connectionGeneration: state.connectionGeneration,
							operationGeneration: state.operationGeneration
						},
						[
							{ type: 'CloseSocket', connectionGeneration: state.connectionGeneration },
							{ type: 'Logout', sessionCsrf: state.sessionCsrf },
							{ type: 'Logout', sessionCsrf: event.sessionCsrf }
						]
					);
				}
				return changed(state, [{ type: 'Logout', sessionCsrf: event.sessionCsrf }]);
			}
			if (event.authenticationGeneration > state.authenticationGeneration) {
				const failed = authenticationProtocolError(
					state,
					'authentication success generation does not match the current attempt'
				);
				return changed(failed.state, [{ type: 'Logout', sessionCsrf: event.sessionCsrf }]);
			}
			if (state.tag !== 'authenticating') {
				if (state.tag === 'signed_out') {
					return changed(state, [{ type: 'Logout', sessionCsrf: event.sessionCsrf }]);
				}
				const failed = authenticationProtocolError(state, 'authentication success has no attempt');
				return changed(failed.state, [{ type: 'Logout', sessionCsrf: event.sessionCsrf }]);
			}
			const connectionGeneration = state.connectionGeneration + 1;
			return changed(
				{
					tag: 'connecting',
					authenticationGeneration: state.authenticationGeneration,
					connectionGeneration,
					operationGeneration: state.operationGeneration,
					sessionCsrf: event.sessionCsrf,
					resume: { kind: 'idle' }
				},
				[
					{
						type: 'OpenSocket',
						connectionGeneration,
						sessionCsrf: event.sessionCsrf
					}
				]
			);
		}
		case 'AuthenticationRefused':
			if (event.authenticationGeneration < state.authenticationGeneration) return unchanged(state);
			if (event.authenticationGeneration > state.authenticationGeneration) {
				return authenticationProtocolError(
					state,
					'authentication refusal generation does not match the current attempt'
				);
			}
			if (state.tag !== 'authenticating') {
				return state.tag === 'signed_out'
					? unchanged(state)
					: authenticationProtocolError(state, 'authentication refusal has no attempt');
			}
			return changed({
				tag: 'signed_out',
				authenticationGeneration: state.authenticationGeneration,
				connectionGeneration: state.connectionGeneration,
				operationGeneration: state.operationGeneration,
				authenticationRefusal: event.refusal
			});
		case 'ReconnectRequested': {
			if (state.tag !== 'reconnecting') return unchanged(state);
			const connectionGeneration = state.connectionGeneration + 1;
			return changed(
				{
					tag: 'connecting',
					authenticationGeneration: state.authenticationGeneration,
					connectionGeneration,
					operationGeneration: state.operationGeneration,
					sessionCsrf: state.sessionCsrf,
					resume: state.resume
				},
				[
					{
						type: 'OpenSocket',
						connectionGeneration,
						sessionCsrf: state.sessionCsrf
					}
				]
			);
		}
		case 'SocketOpened':
			if (state.tag !== 'connecting' || event.connectionGeneration !== state.connectionGeneration) {
				return unchanged(state);
			}
			return openResume(state);
		case 'IntentCreated':
			if (state.tag !== 'idle') return unchanged(state);
			return startWatermark(state, {
				text: event.text,
				idempotencyKey: event.idempotencyKey
			});
		case 'SubmitAnswered':
			if (state.tag === 'reconnecting') return unchanged(state);
			if (!hasSessionCsrf(state)) return unchanged(state);
			return reduceSubmit(state, event);
		case 'RetrieveAnswered':
			if (state.tag === 'reconnecting') return unchanged(state);
			if (!hasSessionCsrf(state)) return unchanged(state);
			return reduceRetrieve(state, event);
		case 'DeliveryReceived':
			if (!hasSessionCsrf(state) || event.connectionGeneration !== state.connectionGeneration) {
				return unchanged(state);
			}
			return reduceDelivery(state, event.delivery);
		case 'SocketClosed':
			return disconnect(state, event.connectionGeneration);
		case 'SocketFailed':
			return disconnect(state, event.connectionGeneration, event.message);
		case 'ProtocolFailed':
			if (
				state.tag === 'reconnecting' ||
				!hasSessionCsrf(state) ||
				event.connectionGeneration !== state.connectionGeneration
			) {
				return unchanged(state);
			}
			return changed({
				tag: 'protocol_error',
				authenticationGeneration: state.authenticationGeneration,
				connectionGeneration: state.connectionGeneration,
				operationGeneration: state.operationGeneration,
				sessionCsrf: state.sessionCsrf,
				protocolFailure: event.failure
			});
		case 'EffectFailed':
			if (state.tag === 'reconnecting') return unchanged(state);
			if (!hasSessionCsrf(state)) return unchanged(state);
			return reduceEffectFailure(state, event);
		case 'ReplayAuthorized':
			if (state.tag !== 'recovery_required') return unchanged(state);
			return startSubmit(state, state.intent, true);
		case 'CheckExactRequested':
			if (state.tag === 'waiting') return startExact(state, state.intent);
			if (state.tag === 'recovering_exact' && state.operation === null) {
				return startExact(state, state.intent);
			}
			return unchanged(state);
		case 'RefusalAcknowledged':
			if (state.tag !== 'refused') return unchanged(state);
			return changed({
				tag: 'idle',
				authenticationGeneration: state.authenticationGeneration,
				connectionGeneration: state.connectionGeneration,
				operationGeneration: state.operationGeneration,
				sessionCsrf: state.sessionCsrf
			});
		case 'TerminalAcknowledged':
			if (state.tag !== 'terminal' || state.pendingExact !== undefined) return unchanged(state);
			return changed({
				tag: 'idle',
				authenticationGeneration: state.authenticationGeneration,
				connectionGeneration: state.connectionGeneration,
				operationGeneration: state.operationGeneration,
				sessionCsrf: state.sessionCsrf
			});
		case 'LogoutRequested':
			if (!hasSessionCsrf(state)) {
				if (state.tag === 'authenticating') {
					return changed(
						{
							tag: 'signed_out',
							authenticationGeneration: state.authenticationGeneration,
							connectionGeneration: state.connectionGeneration,
							operationGeneration: state.operationGeneration
						},
						[
							{
								type: 'CancelAuthentication',
								authenticationGeneration: state.authenticationGeneration
							}
						]
					);
				}
				return unchanged(state);
			}
			return changed(
				{
					tag: 'signed_out',
					authenticationGeneration: state.authenticationGeneration,
					connectionGeneration: state.connectionGeneration,
					operationGeneration: state.operationGeneration
				},
				[
					{ type: 'CloseSocket', connectionGeneration: state.connectionGeneration },
					{ type: 'Logout', sessionCsrf: state.sessionCsrf }
				]
			);
	}
}
