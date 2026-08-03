import { render } from 'vitest-browser-svelte';
import { userEvent } from 'vitest/browser';
import { describe, expect, it, vi } from 'vitest';
import ActionSessionShell from './ActionSessionShell.svelte';
import type { ActionSessionShellProps, ActionSessionShellView } from './ActionSessionShell.types';

const idleView: Readonly<ActionSessionShellView> = Object.freeze({
	label: 'What do you do?',
	value: '',
	busy: false,
	disabled: false
});

function props(
	view: Readonly<ActionSessionShellView>,
	overrides: Partial<Omit<ActionSessionShellProps, 'view'>> = {}
): ActionSessionShellProps {
	return {
		view,
		onInput: vi.fn(),
		onSubmit: vi.fn(),
		onReconnect: vi.fn(),
		...overrides
	};
}

describe('ActionSessionShell', () => {
	it('uses a labelled single-line input and native form submission', async () => {
		const onInput = vi.fn();
		const onSubmit = vi.fn();
		const screen = await render(ActionSessionShell, props(idleView, { onInput, onSubmit }));
		const input = screen.getByRole('textbox', { name: 'What do you do?' });

		expect(input.element()).toBeInstanceOf(HTMLInputElement);
		expect((input.element() as HTMLInputElement).type).toBe('text');

		await input.fill('I open the gate');
		expect(onInput).toHaveBeenLastCalledWith('I open the gate');

		input.element().focus();
		await userEvent.keyboard('{Enter}');
		expect(onSubmit).toHaveBeenCalledOnce();
	});

	it('disables action controls while busy or explicitly disabled', async () => {
		const screen = await render(
			ActionSessionShell,
			props({ ...idleView, value: 'Hold steady', busy: true })
		);

		await expect.element(screen.getByRole('form')).toHaveAttribute('aria-busy', 'true');
		await expect.element(screen.getByRole('textbox')).toBeDisabled();
		await expect.element(screen.getByRole('button', { name: 'Submit' })).toBeDisabled();

		await screen.rerender(props({ ...idleView, disabled: true }));
		await expect.element(screen.getByRole('textbox')).toBeDisabled();
		await expect.element(screen.getByRole('button', { name: 'Submit' })).toBeDisabled();
	});

	it('links refusal text and focuses only for a new controller focus request', async () => {
		const screen = await render(
			ActionSessionShell,
			props({
				...idleView,
				refusal: { message: 'Describe an action first.', focusRequestId: 'focus-1' },
				reconnect: { text: 'Connection interrupted.', available: true }
			})
		);
		const input = screen.getByRole('textbox');
		const reconnect = screen.getByRole('button', { name: 'Reconnect' });

		await expect.element(input).toHaveAccessibleDescription('Describe an action first.');
		await expect.element(input).toHaveFocus();

		await reconnect.click();
		await expect.element(reconnect).toHaveFocus();

		await screen.rerender(
			props({
				...idleView,
				refusal: { message: 'Still refused.', focusRequestId: 'focus-1' },
				reconnect: { text: 'Connection interrupted.', available: true }
			})
		);
		await expect.element(reconnect).toHaveFocus();

		await screen.rerender(
			props({
				...idleView,
				refusal: { message: 'Try another action.', focusRequestId: 'focus-2' },
				reconnect: { text: 'Connection interrupted.', available: true }
			})
		);
		await expect.element(input).toHaveFocus();
	});

	it('renders reconnect status and delegates an available reconnect action', async () => {
		const onReconnect = vi.fn();
		const screen = await render(
			ActionSessionShell,
			props(
				{
					...idleView,
					reconnect: { text: 'The connection was lost.', available: true }
				},
				{ onReconnect }
			)
		);

		await expect.element(screen.getByText('The connection was lost.')).toBeVisible();
		await screen.getByRole('button', { name: 'Reconnect' }).click();
		expect(onReconnect).toHaveBeenCalledOnce();

		await screen.rerender(
			props({
				...idleView,
				reconnect: { text: 'Reconnect is not available yet.', available: false }
			})
		);
		await expect.element(screen.getByRole('button', { name: 'Reconnect' })).toBeDisabled();
	});

	it('keeps one polite live region and announces each announcement id once', async () => {
		const screen = await render(
			ActionSessionShell,
			props({
				...idleView,
				liveStatus: { announcementId: 'status-1', text: 'Waiting for the world.' }
			})
		);
		const status = screen.getByRole('status');

		expect(status.length).toBe(1);
		await expect.element(status).toHaveAttribute('aria-live', 'polite');
		await expect.element(status).toHaveTextContent('Waiting for the world.');

		await screen.rerender(
			props({
				...idleView,
				liveStatus: { announcementId: 'status-1', text: 'Duplicate announcement.' }
			})
		);
		await expect.element(status).toHaveTextContent('Waiting for the world.');

		await screen.rerender(
			props({
				...idleView,
				liveStatus: { announcementId: 'status-2', text: 'Waiting for the world.' }
			})
		);
		await expect.element(status).toHaveTextContent('Waiting for the world.');
		expect(status.element().textContent).toBe('Waiting for the world.');
	});
});
