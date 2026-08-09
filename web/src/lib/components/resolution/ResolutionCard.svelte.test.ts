import { render } from 'vitest-browser-svelte';
import { describe, expect, it } from 'vitest';
import type { ResolutionView } from '../../player-view/resolution-projection';
import ResolutionCard from './ResolutionCard.svelte';

const base = {
	turn_id: 'turn-act-1',
	action_id: 'act-1',
	player_id: 'player-1',
	resolved_at: '2026-07-28T09:15:30.123456Z'
} as const;

const rolled = {
	...base,
	phase: 'complete',
	narration_ref: 'obj://ARTIFACTS/narration/turn-act-1',
	narration: {
		turn_id: 'turn-act-1',
		band: 'partial',
		prose: 'The hinges scream, but the gate gives.'
	},
	verdict: {
		plausibility: 'plausible',
		risk: 'high',
		consequence: 'harm',
		requires_roll: true
	},
	band: 'partial',
	roll: {
		kind: 'rolled',
		mechanic: '2d6-pbta/v1',
		dice: [4, 4],
		modifiers: [{ source: 'equipment', value: 1, note: 'crowbar' }],
		modifier_total: 1,
		total: 9
	},
	companion_resolution: {
		companion_id: 'companion-wren',
		kind: 'hint',
		hint_level: 'connect'
	}
} as const satisfies Readonly<ResolutionView>;

describe('ResolutionCard', () => {
	it('renders authoritative rolled evidence with article, heading, and definition-list semantics', async () => {
		const screen = await render(ResolutionCard, { resolution: rolled });
		const article = screen.getByRole('article', { name: 'Resolution for turn turn-act-1' });

		await expect.element(article).toBeVisible();
		await expect.element(article.getByRole('heading', { name: 'Resolution' })).toBeVisible();
		expect(article.element().querySelector('dl')).not.toBeNull();
		await expect.element(article.getByText('The hinges scream, but the gate gives.')).toBeVisible();
		// The chip strip is visible in the collapsed (default, fiction) state; the
		// exact-match "Risk high" text is unambiguous even though the dl below,
		// once expanded, separately renders a bare "high" dd for the same field.
		await expect.element(article.getByText('Risk high', { exact: true })).toBeVisible();

		await article.getByText('Mechanics').click();
		await expect.element(article.getByText('partial', { exact: true })).toBeVisible();
		await expect.element(article.getByText('2d6-pbta/v1')).toBeVisible();
		await expect.element(article.getByText('4, 4')).toBeVisible();
		await expect.element(article.getByText('equipment: +1 (crowbar)')).toBeVisible();
		await expect.element(article.getByText('Modifier total')).toBeVisible();
		await expect.element(article.getByText('+1', { exact: true })).toBeVisible();
		await expect.element(article.getByText('9', { exact: true })).toBeVisible();
		await expect.element(article.getByText('hint (hint level: connect)')).toBeVisible();
	});

	it('renders explicit no-roll evidence without manufacturing dice or arithmetic', async () => {
		const noRoll = {
			...base,
			phase: 'complete',
			narration_ref: 'obj://ARTIFACTS/narration/turn-act-1',
			narration: { turn_id: 'turn-act-1', band: 'auto', prose: 'The latch opens.' },
			verdict: {
				plausibility: 'certain',
				risk: 'none',
				consequence: 'none',
				requires_roll: false
			},
			band: 'auto',
			roll: { kind: 'not_required' },
			companion_resolution: { companion_id: 'companion-wren', kind: 'quip' }
		} as const satisfies Readonly<ResolutionView>;
		const screen = await render(ResolutionCard, { resolution: noRoll });

		await screen.getByText('Mechanics').click();
		await expect.element(screen.getByText('Not required')).toBeVisible();
		expect(screen.getByText('Dice').query()).toBeNull();
		expect(screen.getByText('Modifier total').query()).toBeNull();
		await expect.element(screen.getByText('quip', { exact: true })).toBeVisible();
		expect(screen.getByText(/hint level/i).query()).toBeNull();
	});

	it('renders failed evidence and only the optional evidence actually supplied', async () => {
		const failed = {
			...base,
			phase: 'failed',
			failure_reason: 'effect-invalid',
			narration: {
				turn_id: 'turn-act-1',
				band: 'miss',
				prose: 'The mechanism jams.'
			},
			verdict: {
				plausibility: 'unlikely',
				risk: 'moderate',
				consequence: 'setback',
				requires_roll: true
			},
			band: 'miss'
		} as const satisfies Readonly<ResolutionView>;
		const screen = await render(ResolutionCard, { resolution: failed });

		await expect.element(screen.getByText('The mechanism jams.')).toBeVisible();
		await screen.getByText('Mechanics').click();
		await expect.element(screen.getByText('Failed', { exact: true })).toBeVisible();
		await expect.element(screen.getByText('effect-invalid')).toBeVisible();
		await expect.element(screen.getByText('unlikely')).toBeVisible();
		// Exact match, not a substring match: the risk chip in the summary
		// (always rendered, open or closed) reads "Risk moderate", which would
		// otherwise collide with the dl's bare "moderate" dd under Playwright's
		// strict-mode element resolution.
		await expect.element(screen.getByText('moderate', { exact: true })).toBeVisible();
		await expect.element(screen.getByText('setback')).toBeVisible();
		expect(screen.getByText('Roll', { exact: true }).query()).toBeNull();
		expect(screen.getByText('Dice').query()).toBeNull();
	});

	it('defaults the mechanics disclosure closed in fiction presentation and open in crunch presentation', async () => {
		const fictionScreen = await render(ResolutionCard, {
			resolution: rolled,
			presentation: 'fiction'
		});
		const fictionDetails = fictionScreen.container.querySelector('details.mechanics');
		expect(fictionDetails).not.toBeNull();
		expect((fictionDetails as HTMLDetailsElement).open).toBe(false);

		const crunchScreen = await render(ResolutionCard, {
			resolution: rolled,
			presentation: 'crunch'
		});
		const crunchDetails = crunchScreen.container.querySelector('details.mechanics');
		expect(crunchDetails).not.toBeNull();
		expect((crunchDetails as HTMLDetailsElement).open).toBe(true);
	});
});
