import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';

// .sr-only lives in the shared app-wide stylesheet (normally loaded once via
// the root layout); component tests render in isolation, so this reproduces
// that global stylesheet for the bounding-box assertion below.
import '../../styles/app.css';
import ClockStatus from './ClockStatus.svelte';
import type { ClockStatusView } from './ClockStatus.types';

describe('ClockStatus', () => {
	it('renders a configured clock as labelled text', async () => {
		const clock: ClockStatusView = Object.freeze({
			state: 'configured',
			label: 'Village time',
			value: 317,
			unit: 'minute'
		});

		const screen = await render(ClockStatus, { clock });

		await expect.element(screen.getByRole('status', { name: 'Clock status' })).toBeVisible();
		await expect.element(screen.getByText('Village time')).toBeVisible();
		await expect.element(screen.getByText('317 minute')).toBeVisible();
		expect(screen.getByRole('alert').query()).toBeNull();
		// The "Configured" label is visually redundant once the labelled value
		// renders, but must stay in the DOM for assistive tech. The standard
		// sr-only technique deliberately keeps a nonzero (1px) footprint rather
		// than display:none/width:0 — some assistive tech skips zero-size
		// elements — so toBeVisible() (which only checks for an empty bounding
		// box) can't tell it apart from a genuinely visible label. Assert the
		// visual footprint directly instead.
		const stateLabel = screen.getByText('Configured');
		expect(stateLabel.query()).not.toBeNull();
		const rect = stateLabel.element().getBoundingClientRect();
		expect(rect.width).toBeLessThanOrEqual(1);
		expect(rect.height).toBeLessThanOrEqual(1);
	});

	it('states explicitly when the clock is not configured', async () => {
		const screen = await render(ClockStatus, {
			clock: Object.freeze({ state: 'not_configured' })
		});

		await expect.element(screen.getByText('Clock not configured.')).toBeVisible();
		expect(screen.getByRole('alert').query()).toBeNull();
	});

	it('announces an error with semantic text instead of a color-only affordance', async () => {
		const clock: ClockStatusView = Object.freeze({
			state: 'error',
			message: 'Clock facts are ambiguous.'
		});
		const screen = await render(ClockStatus, { clock });

		await expect
			.element(screen.getByRole('alert'))
			.toHaveTextContent('Clock unavailable: Clock facts are ambiguous.');
		await expect.element(screen.getByText('Error')).toBeVisible();
	});

	it('changes only when its immutable projection prop changes', async () => {
		const screen = await render(ClockStatus, {
			clock: Object.freeze({ state: 'configured', label: 'Round', value: 4, unit: 'turn' })
		});

		await expect.element(screen.getByText('4 turn')).toBeVisible();
		await screen.rerender({
			clock: Object.freeze({ state: 'configured', label: 'Round', value: 5, unit: 'turn' })
		});
		await expect.element(screen.getByText('5 turn')).toBeVisible();
		expect(screen.getByText('4 turn').query()).toBeNull();
	});
});
