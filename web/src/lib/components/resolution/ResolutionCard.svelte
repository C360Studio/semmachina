<script lang="ts">
	import type { ResolutionView } from '../../player-view/resolution-projection';

	let { resolution }: { readonly resolution: Readonly<ResolutionView> } = $props();

	function signed(value: number): string {
		return value > 0 ? `+${value}` : String(value);
	}
</script>

<article aria-label={`Resolution for turn ${resolution.turn_id}`}>
	<h2>Resolution</h2>

	{#if resolution.narration !== undefined}
		<p>{resolution.narration.prose}</p>
	{/if}

	<dl>
		<dt>Status</dt>
		<dd>{resolution.phase === 'complete' ? 'Complete' : 'Failed'}</dd>

		<dt>Turn</dt>
		<dd>{resolution.turn_id}</dd>

		<dt>Action</dt>
		<dd>{resolution.action_id}</dd>

		<dt>Player</dt>
		<dd>{resolution.player_id}</dd>

		<dt>Resolved at</dt>
		<dd>{resolution.resolved_at}</dd>

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
					<dl>
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
</article>
