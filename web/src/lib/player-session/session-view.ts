import type { ActionSessionShellView } from '../components/session/ActionSessionShell.types';
import type { TurnDelivery } from '../player-v1/parser';
import {
	createResolutionLedger,
	projectTerminal,
	type ResolutionView
} from '../player-view/resolution-projection';
import type { SessionState } from './session-machine';

export interface SessionView {
	readonly showAction: boolean;
	readonly action: Readonly<ActionSessionShellView>;
	readonly replayExplanation?: string;
	readonly canAuthorizeReplay: boolean;
	readonly canCheckExact: boolean;
	readonly canAcknowledgeRefusal: boolean;
	readonly canAcknowledgeTerminal: boolean;
	readonly protocolError?: string;
	readonly resolution?: Readonly<ResolutionView>;
}

function projectOneDelivery(delivery: TurnDelivery): ResolutionView {
	return projectTerminal(createResolutionLedger(), delivery).ledger.entries[0] as ResolutionView;
}

const statusText = (state: SessionState): string => {
	switch (state.tag) {
		case 'signed_out':
			return 'Sign in to enter the world.';
		case 'authenticating':
			return 'Authenticating creator.';
		case 'connecting':
			return 'Connecting to the world.';
		case 'idle':
			return 'Ready for action.';
		case 'watermarking':
			return 'Checking prior activity.';
		case 'submitting':
			return 'Submitting action.';
		case 'waiting':
			return 'Waiting for resolution.';
		case 'reconnecting':
			return 'Connection interrupted.';
		case 'recovering_exact':
			return 'Recovering the exact resolution.';
		case 'recovering_evidence':
			return 'Checking player activity without assuming correlation.';
		case 'recovery_required':
			return 'Recovery evidence requires authorization.';
		case 'refused':
			return 'Action refused.';
		case 'terminal':
			return 'Resolution received.';
		case 'protocol_error':
			return 'Player protocol error. Session controls are disabled.';
	}
};

const isBusy = (state: SessionState): boolean =>
	[
		'authenticating',
		'connecting',
		'watermarking',
		'submitting',
		'waiting',
		'recovering_exact',
		'recovering_evidence'
	].includes(state.tag);

const hasAuthenticatedSession = (state: SessionState): boolean =>
	'sessionCsrf' in state && typeof state.sessionCsrf === 'string';

export function projectSessionView(state: SessionState, actionValue: string): SessionView {
	const announcementId = [
		state.tag,
		state.authenticationGeneration,
		state.connectionGeneration,
		state.operationGeneration
	].join(':');
	const action: ActionSessionShellView = {
		label: 'What do you do?',
		value: actionValue,
		busy: isBusy(state),
		disabled: state.tag !== 'idle',
		...(state.tag === 'refused'
			? {
					inputDisabled: false,
					submitDisabled: true,
					refusal: {
						message: state.refusal.message,
						focusRequestId: `refusal:${announcementId}`
					}
				}
			: {}),
		...(state.tag === 'reconnecting'
			? {
					reconnect: {
						text: 'Connection interrupted.',
						available: true
					}
				}
			: {}),
		liveStatus: { announcementId, text: statusText(state) }
	};

	return {
		showAction: hasAuthenticatedSession(state),
		action,
		...(state.tag === 'recovery_required' ? { replayExplanation: state.replayExplanation } : {}),
		canAuthorizeReplay: state.tag === 'recovery_required',
		canCheckExact: state.tag === 'recovering_exact' && state.operation === null,
		canAcknowledgeRefusal: state.tag === 'refused',
		canAcknowledgeTerminal: state.tag === 'terminal' && state.pendingExact === undefined,
		...(state.tag === 'protocol_error'
			? { protocolError: 'Player protocol error. Session controls are disabled.' }
			: {}),
		...(state.tag === 'terminal' ? { resolution: projectOneDelivery(state.delivery) } : {})
	};
}
