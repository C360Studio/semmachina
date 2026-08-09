<script lang="ts">
	import type { ResolutionView } from '../../player-view/resolution-projection';
	import type { Presentation } from '../presentation';

	interface Props {
		readonly resolution: Readonly<ResolutionView>;
		readonly presentation?: Presentation;
	}

	let { resolution, presentation = 'fiction' }: Props = $props();

	function signed(value: number): string {
		return value > 0 ? `+${value}` : String(value);
	}

	interface SummaryChip {
		readonly kind: 'status' | 'band' | 'roll' | 'risk';
		readonly text: string;
		readonly band?: string;
	}

	function buildSummaryChips(view: Readonly<ResolutionView>): readonly SummaryChip[] {
		const chips: SummaryChip[] = [];
		if (view.phase === 'failed') {
			chips.push({ kind: 'status', text: 'Turn failed' });
		}
		if (view.band !== undefined) {
			chips.push({ kind: 'band', text: `Outcome ${view.band}`, band: view.band });
		}
		if (view.roll !== undefined && view.roll.kind === 'rolled') {
			chips.push({
				kind: 'roll',
				text: `2d6${signed(view.roll.modifier_total)} = ${view.roll.total}`
			});
		}
		if (view.verdict !== undefined) {
			chips.push({ kind: 'risk', text: `Risk ${view.verdict.risk}` });
		}
		return chips;
	}

	// The mechanics disclosure is the payoff of the presentation dial: fiction
	// mode starts collapsed (narration + the compact chip strip only), crunch
	// mode starts expanded (full breakdown visible immediately). A reader can
	// still toggle it manually in either mode — this only sets the default.
	let summaryChips = $derived(buildSummaryChips(resolution));
</script>

<article
	class="resolution-card"
	class:resolution-card--failed={resolution.phase === 'failed'}
	aria-label={`Resolution for turn ${resolution.turn_id}`}
>
	<h2 class="kicker">Resolution</h2>

	{#if resolution.narration !== undefined}
		<p class="narration">{resolution.narration.prose}</p>
	{/if}

	<details class="mechanics" data-presentation={presentation} open={presentation === 'crunch'}>
		<summary class="mechanics-summary">
			<span class="mechanics-summary__label">Mechanics</span>
			<span class="chip-row">
				{#each summaryChips as chip (chip.kind)}
					<span class="chip" data-chip={chip.kind} data-band={chip.band}>{chip.text}</span>
				{/each}
			</span>
		</summary>

		<div class="mechanics-body">
			<dl class="fields">
				<dt>Status</dt>
				<dd>{resolution.phase === 'complete' ? 'Complete' : 'Failed'}</dd>

				<dt class="fine-print">Turn</dt>
				<dd class="fine-print">{resolution.turn_id}</dd>

				<dt class="fine-print">Action</dt>
				<dd class="fine-print">{resolution.action_id}</dd>

				<dt class="fine-print">Player</dt>
				<dd class="fine-print">{resolution.player_id}</dd>

				<dt class="fine-print">Resolved at</dt>
				<dd class="fine-print">{resolution.resolved_at}</dd>

				{#if resolution.phase === 'failed'}
					<dt>Failure reason</dt>
					<dd>{resolution.failure_reason}</dd>
				{/if}

				{#if resolution.verdict !== undefined}
					<dt>Plausibility</dt>
					<dd>{resolution.verdict.plausibility}</dd>

					<dt>Risk</dt>
					<dd>{resolution.verdict.risk}</dd>

					<dt>Consequence</dt>
					<dd>{resolution.verdict.consequence}</dd>

					<dt>Requires roll</dt>
					<dd>{resolution.verdict.requires_roll ? 'Yes' : 'No'}</dd>
				{/if}

				{#if resolution.band !== undefined}
					<dt>Outcome</dt>
					<dd>{resolution.band}</dd>
				{/if}

				{#if resolution.roll !== undefined}
					<dt>Roll</dt>
					{#if resolution.roll.kind === 'not_required'}
						<dd>Not required</dd>
					{:else}
						<dd>
							<dl class="fields fields--nested">
								<dt>Mechanic</dt>
								<dd>{resolution.roll.mechanic}</dd>

								<dt>Dice</dt>
								<dd>{resolution.roll.dice.join(', ')}</dd>

								{#if resolution.roll.modifiers !== undefined}
									<dt>Modifiers</dt>
									<dd>
										<ul>
											{#each resolution.roll.modifiers as modifier (modifier)}
												<li>
													{`${modifier.source}: ${signed(modifier.value)}${
														modifier.note === undefined ? '' : ` (${modifier.note})`
													}`}
												</li>
											{/each}
										</ul>
									</dd>
								{/if}

								<dt>Modifier total</dt>
								<dd>{signed(resolution.roll.modifier_total)}</dd>

								<dt>Total</dt>
								<dd>{resolution.roll.total}</dd>
							</dl>
						</dd>
					{/if}
				{/if}

				{#if resolution.companion_resolution !== undefined}
					<dt>Companion resolution</dt>
					<dd>
						<span
							>{`${resolution.companion_resolution.kind}${
								resolution.companion_resolution.hint_level === undefined
									? ''
									: ` (hint level: ${resolution.companion_resolution.hint_level})`
							}`}</span
						>
						<span>Companion {resolution.companion_resolution.companion_id}</span>
					</dd>
				{/if}
			</dl>
		</div>
	</details>
</article>

<style>
	.resolution-card {
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-sm);
		padding: var(--space-5);
		display: grid;
		gap: var(--space-3);
	}

	.resolution-card--failed {
		border-color: var(--color-danger);
		border-inline-start-width: 0.3rem;
		background: var(--color-danger-surface);
	}

	.kicker {
		margin: 0;
		font-size: var(--font-size-xs);
		font-weight: 650;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--color-ink-muted);
	}

	.resolution-card--failed .kicker {
		color: var(--color-danger-ink);
	}

	.narration {
		margin: 0;
		font-family: var(--font-serif);
		font-size: 1.1rem;
		line-height: var(--line-height-prose);
		color: var(--color-ink);
	}

	.mechanics {
		border-top: 1px solid var(--color-border);
		padding-top: var(--space-3);
	}

	.mechanics-summary {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-3);
		list-style: none;
		font-size: var(--font-size-sm);
	}

	.mechanics-summary::-webkit-details-marker {
		display: none;
	}

	.mechanics-summary::before {
		content: '▸';
		display: inline-block;
		color: var(--color-ink-faint);
		transition: transform var(--transition-fast);
	}

	.mechanics[open] > .mechanics-summary::before {
		transform: rotate(90deg);
	}

	.mechanics-summary__label {
		font-weight: 650;
		color: var(--color-ink-muted);
	}

	.chip-row {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}

	.chip {
		display: inline-block;
		background: var(--color-neutral-surface);
		color: var(--color-neutral);
		border-radius: var(--radius-pill);
		padding: 0.15rem var(--space-3);
		font-size: var(--font-size-xs);
		font-weight: 600;
		white-space: nowrap;
	}

	.mechanics[data-presentation='crunch'] .chip {
		font-size: var(--font-size-sm);
		padding: 0.25rem var(--space-3);
	}

	.chip[data-chip='status'],
	.chip[data-chip='band'][data-band='miss'] {
		background: var(--color-danger-surface);
		color: var(--color-danger);
	}

	.chip[data-chip='band'][data-band='full'] {
		background: var(--color-success-surface);
		color: var(--color-success);
	}

	.chip[data-chip='band'][data-band='partial'] {
		background: var(--color-amber-surface);
		color: var(--color-amber);
	}

	.chip[data-chip='band'][data-band='auto'] {
		background: var(--color-neutral-surface);
		color: var(--color-neutral);
	}

	.mechanics-body {
		padding-top: var(--space-3);
	}

	.fields {
		display: grid;
		/* minmax(0, 1fr), not a bare 1fr: a bare fr track won't shrink a grid
		   item below its content's min-content width, so long unbroken values
		   (entity IDs, ISO timestamps) would push the card wider than the
		   viewport instead of wrapping. */
		grid-template-columns: max-content minmax(0, 1fr);
		gap: var(--space-1) var(--space-4);
		margin: 0;
	}

	.fields dt {
		font-weight: 600;
		color: var(--color-ink-muted);
	}

	.fields dd {
		margin: 0;
		overflow-wrap: anywhere;
	}

	/* Below this width, a nested roll <dl> inside the already-narrow value
	   column of the outer <dl> would otherwise squeeze its own label/value
	   columns down to almost nothing. Stacking label-above-value at every
	   nesting depth sidesteps that instead of fighting it with more wrapping. */
	@media (max-width: 28rem) {
		.fields {
			grid-template-columns: minmax(0, 1fr);
			gap: 0 0;
		}

		.fields dt {
			margin-top: var(--space-2);
		}

		.fields dt:first-child {
			margin-top: 0;
		}
	}

	.fields--nested {
		margin-top: var(--space-1);
	}

	.fine-print {
		font-family: var(--font-mono);
		font-size: var(--font-size-xs);
		color: var(--color-ink-faint);
	}

	dt.fine-print {
		font-weight: 500;
	}
</style>
