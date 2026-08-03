import { render } from 'vitest-browser-svelte';
import { describe, expect, it, vi } from 'vitest';

import { rolledDelivery } from '../player-v1/fixtures';
import type { TurnDelivery } from '../player-v1/parser';
import type { SessionController } from '../player-session/session-controller';
import type { SessionEvent, SessionState } from '../player-session/session-machine';
import CreatorSurface from './CreatorSurface.svelte';

const signedOut: SessionState = {
	tag: 'signed_out',
	authenticationGeneration: 0,
	connectionGeneration: 0,
	operationGeneration: 0
};

class FakeController implements SessionController {
	state: SessionState = signedOut;
	events: SessionEvent[] = [];
	destroy = vi.fn();
	unsubscribe = vi.fn();
	#subscriber?: (state: SessionState) => void;

	getState(): SessionState {
		return this.state;
	}

	subscribe(subscriber: (state: SessionState) => void): () => void {
		this.#subscriber = subscriber;
		subscriber(this.state);
		return this.unsubscribe;
	}

	dispatch(event: SessionEvent): void {
		this.events.push(event);
	}

	emit(state: SessionState): void {
		this.state = state;
		this.#subscriber?.(state);
	}
}

const connecting = (): Extract<SessionState, { tag: 'connecting' }> => ({
	tag: 'connecting',
	authenticationGeneration: 1,
	connectionGeneration: 1,
	operationGeneration: 0,
	sessionCsrf: 'csrf',
	resume: { kind: 'idle' }
});

const idle = (): Extract<SessionState, { tag: 'idle' }> => ({
	tag: 'idle',
	authenticationGeneration: 1,
	connectionGeneration: 1,
	operationGeneration: 0,
	sessionCsrf: 'csrf'
});

const terminal = (delivery: TurnDelivery): Extract<SessionState, { tag: 'terminal' }> => ({
	...idle(),
	tag: 'terminal',
	intent: {
		text: 'Open',
		idempotencyKey: 'key-1',
		watermark: 'empty',
		ids: { actionId: delivery.result.action_id, turnId: delivery.result.turn_id }
	},
	delivery
});

const world = Object.freeze({
	places: Object.freeze([
		Object.freeze({
			id: 'place.gate',
			label: 'Gate',
			connections: Object.freeze(['place.square'])
		}),
		Object.freeze({ id: 'place.square', label: 'Square', connections: Object.freeze([]) })
	]),
	clock: Object.freeze({ state: 'configured' as const, label: 'Day', value: 3, unit: 'days' })
});

describe('CreatorSurface', () => {
	it('clears credentials on submit, loads the world once after authentication, and tears down', async () => {
		const controller = new FakeController();
		const worldLoader = vi.fn(async () => world);
		const screen = await render(CreatorSurface, {
			controllerFactory: () => controller,
			worldLoader,
			keyFactory: () => 'key-1'
		});
		const credential = screen.getByLabelText('Creator credential');

		await credential.fill('creator-secret');
		await screen.getByRole('button', { name: 'Enter world' }).click();
		expect(controller.events).toEqual([
			{ type: 'AuthenticateRequested', credential: 'creator-secret' }
		]);
		await expect.element(credential).toHaveValue('');

		controller.emit(connecting());
		await vi.waitFor(() => expect(worldLoader).toHaveBeenCalledOnce());
		controller.emit(idle());
		await expect.element(screen.getByRole('region', { name: 'World topology' })).toBeVisible();
		await expect
			.element(screen.getByRole('status', { name: 'Clock status' }))
			.toHaveTextContent('3 days');
		expect(worldLoader).toHaveBeenCalledOnce();

		await screen.unmount();
		expect(controller.unsubscribe).toHaveBeenCalledOnce();
		expect(controller.destroy).toHaveBeenCalledOnce();
	});

	it('delegates action, reconnect, replay, exact check, and acknowledgement events', async () => {
		const controller = new FakeController();
		const screen = await render(CreatorSurface, {
			controllerFactory: () => controller,
			worldLoader: async () => world,
			keyFactory: () => 'key-1'
		});
		controller.emit(idle());
		const action = screen.getByRole('textbox', { name: 'What do you do?' });
		await action.fill('Open the gate');
		await screen.getByRole('button', { name: 'Submit' }).click();
		expect(controller.events.at(-1)).toEqual({
			type: 'IntentCreated',
			text: 'Open the gate',
			idempotencyKey: 'key-1'
		});

		controller.emit({ ...connecting(), tag: 'reconnecting', resume: { kind: 'idle' } });
		await screen.getByRole('button', { name: 'Reconnect' }).click();
		expect(controller.events.at(-1)).toEqual({ type: 'ReconnectRequested' });

		controller.emit({
			...idle(),
			tag: 'recovery_required',
			intent: { text: 'Open', idempotencyKey: 'key-1', watermark: 'empty' },
			replayExplanation: 'Exact reducer explanation.'
		});
		await expect.element(screen.getByText('Exact reducer explanation.')).toBeVisible();
		await screen.getByRole('button', { name: 'Authorize exact replay' }).click();
		expect(controller.events.at(-1)).toEqual({ type: 'ReplayAuthorized' });

		controller.emit({
			...idle(),
			tag: 'recovering_exact',
			intent: {
				text: 'Open',
				idempotencyKey: 'key-1',
				watermark: 'empty',
				ids: { actionId: 'act-1', turnId: 'turn-act-1' }
			},
			operation: null,
			refusal: { code: 'not_ready', message: 'Still resolving.' }
		});
		await screen.getByRole('button', { name: 'Check exact result' }).click();
		expect(controller.events.at(-1)).toEqual({ type: 'CheckExactRequested' });

		controller.emit({
			...idle(),
			tag: 'refused',
			intent: { text: 'Open', idempotencyKey: 'key-1' },
			refusal: { code: 'invalid_field', message: 'Be specific.', field: 'text' }
		});
		await expect.element(action).toBeEnabled();
		await expect.element(action).toHaveFocus();
		await expect.element(screen.getByRole('button', { name: 'Submit' })).toBeDisabled();
		await screen.getByRole('button', { name: 'Acknowledge refusal' }).click();
		expect(controller.events.at(-1)).toEqual({ type: 'RefusalAcknowledged' });
	});

	it('renders and focuses only the current machine terminal once per canonical identity', async () => {
		const controller = new FakeController();
		const screen = await render(CreatorSurface, {
			controllerFactory: () => controller,
			worldLoader: async () => world,
			keyFactory: () => 'key-1'
		});
		const terminalState = terminal(rolledDelivery as unknown as TurnDelivery);

		controller.emit(terminalState);
		const terminalRegion = screen.getByRole('region', {
			name: `Terminal resolution for turn ${rolledDelivery.result.turn_id}`
		});
		const resolution = screen.getByRole('article', {
			name: `Resolution for turn ${rolledDelivery.result.turn_id}`
		});
		await expect.element(terminalRegion).toHaveFocus();
		await expect.element(resolution).toBeVisible();
		expect(resolution.length).toBe(1);
		await expect.element(screen.getByText('hint (hint level: connect)')).toBeVisible();

		const continueButton = screen.getByRole('button', { name: 'Continue' });
		continueButton.element().focus();
		controller.emit(terminalState);
		await expect.element(continueButton).toHaveFocus();
		expect(resolution.length).toBe(1);

		await continueButton.click();
		expect(controller.events.at(-1)).toEqual({ type: 'TerminalAcknowledged' });
	});

	it('removes terminal presentation and fails closed only when the machine reports protocol_error', async () => {
		const controller = new FakeController();
		const screen = await render(CreatorSurface, {
			controllerFactory: () => controller,
			worldLoader: async () => world,
			keyFactory: () => 'key-1'
		});
		controller.emit(terminal(rolledDelivery as unknown as TurnDelivery));
		await expect
			.element(
				screen.getByRole('article', {
					name: `Resolution for turn ${rolledDelivery.result.turn_id}`
				})
			)
			.toBeVisible();
		controller.emit({
			...idle(),
			tag: 'protocol_error',
			protocolFailure: {
				kind: 'correlation',
				message: 'conflicting terminal content for one identity'
			}
		});

		await expect
			.element(screen.getByRole('alert'))
			.toHaveTextContent('Player protocol error. Session controls are disabled.');
		await expect.element(screen.getByRole('textbox', { name: 'What do you do?' })).toBeDisabled();
		await expect.element(screen.getByRole('button', { name: 'Submit' })).toBeDisabled();
		expect(screen.getByRole('button', { name: 'Continue' }).query()).toBeNull();
		expect(screen.getByRole('article', { name: /Resolution for turn/ }).query()).toBeNull();
		expect(screen.getByText('Conflicting resolution evidence.').query()).toBeNull();
	});
});
