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
		<span class="state-label">Configured</span>
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
		border-inline-start: 0.25rem solid currentColor;
		display: grid;
		gap: 0.25rem;
		padding: 0.75rem 1rem;
	}

	.state-label,
	dt {
		font-weight: 650;
	}

	dl,
	dd,
	p {
		margin: 0;
	}

	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.25rem 0.75rem;
	}
</style>
