<script lang="ts">
	import type { ClockStatusView } from './ClockStatus.types';

	interface Props {
		readonly clock: ClockStatusView;
	}

	let { clock }: Props = $props();
</script>

<section
	class="clock-status"
	data-state={clock.state}
	role={clock.state === 'error' ? 'alert' : 'status'}
	aria-label="Clock status"
>
	{#if clock.state === 'configured'}
		<span class="state-label sr-only">Configured</span>
		<dl>
			<dt>{clock.label}</dt>
			<dd>{clock.value} {clock.unit}</dd>
		</dl>
	{:else if clock.state === 'not_configured'}
		<span class="state-label">Not configured</span>
		<p>Clock not configured.</p>
	{:else}
		<span class="state-label">Error</span>
		<p>Clock unavailable: {clock.message}</p>
	{/if}
</section>

<style>
	.clock-status {
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-inline-start: 0.25rem solid var(--color-accent);
		border-radius: var(--radius-md);
		box-shadow: var(--shadow-sm);
		display: grid;
		gap: var(--space-2);
		padding: var(--space-3) var(--space-4);
	}

	.clock-status[data-state='not_configured'] {
		border-inline-start-color: var(--color-ink-faint);
	}

	.clock-status[data-state='error'] {
		border-inline-start-color: var(--color-danger);
		background: var(--color-danger-surface);
	}

	.state-label {
		font-size: var(--font-size-xs);
		font-weight: 650;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--color-ink-muted);
	}

	.clock-status[data-state='error'] .state-label {
		color: var(--color-danger-ink);
	}

	/* The "Configured" label is redundant for sighted users once the clock's
	   own label/value pair is visible (e.g. "Day — 3 days"), but screen
	   reader users still benefit from the explicit state announcement.
	   .sr-only is defined once, globally, in src/lib/styles/app.css. */

	dt {
		font-size: var(--font-size-xs);
		font-weight: 600;
		color: var(--color-ink-muted);
	}

	dl,
	dd,
	p {
		margin: 0;
	}

	dl {
		display: grid;
		gap: 0.1rem;
	}

	dd {
		font-size: var(--font-size-lg);
		font-weight: 600;
	}

	.clock-status p {
		color: var(--color-ink-muted);
		font-size: var(--font-size-sm);
	}

	.clock-status[data-state='error'] p {
		color: var(--color-danger-ink);
	}
</style>
