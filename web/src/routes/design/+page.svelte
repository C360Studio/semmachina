<script lang="ts">
	import CreatorSurface from '$lib/components/CreatorSurface.svelte';
	import type { Presentation } from '$lib/components/presentation';

	import { bellweatherWorld, HarnessController, reconnecting, refused, signedOut } from './harness';

	let controller: HarnessController | undefined;
	let generation = 100;
	let presentation = $state<Presentation>('fiction');

	const controllerFactory = (): HarnessController => {
		controller = new HarnessController();
		return controller;
	};
	const worldLoader = (): Promise<typeof bellweatherWorld> => Promise.resolve(bellweatherWorld);
	const keyFactory = (): string => `harness-key-${++generation}`;
</script>

<svelte:head>
	<title>SemMachina design harness</title>
</svelte:head>

<nav class="harness-bar" aria-label="Harness state jumps">
	<span class="harness-bar__label">Harness</span>
	<button type="button" onclick={() => controller?.emit(signedOut)}>Signed out</button>
	<button type="button" onclick={() => controller?.emit(refused(++generation))}>Refused</button>
	<button type="button" onclick={() => controller?.emit(reconnecting(++generation))}
		>Reconnecting</button
	>

	<span class="harness-bar__divider" aria-hidden="true"></span>

	<span class="harness-bar__label" id="presentation-dial-label">Presentation</span>
	<div class="harness-bar__dial" role="group" aria-labelledby="presentation-dial-label">
		<button
			type="button"
			aria-pressed={presentation === 'fiction'}
			onclick={() => (presentation = 'fiction')}>Fiction</button
		>
		<button
			type="button"
			aria-pressed={presentation === 'crunch'}
			onclick={() => (presentation = 'crunch')}>Crunch</button
		>
	</div>
</nav>

<CreatorSurface {controllerFactory} {worldLoader} {keyFactory} {presentation} />

<style>
	.harness-bar {
		/* Fixed to the top (not the bottom) corner deliberately: the app itself
		   pins its own action bar to the bottom of the viewport, and at narrow
		   widths this toolbar's width would otherwise sit directly on top of it
		   and intercept clicks meant for Submit. */
		position: fixed;
		right: var(--space-3, 0.75rem);
		top: var(--space-3, 0.75rem);
		z-index: 100;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.4rem;
		max-width: min(80vw, 34rem);
		background: rgba(20, 16, 12, 0.92);
		color: #f1e6d3;
		border-radius: 0.5rem;
		padding: 0.5rem 0.6rem;
		font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
		font-size: 0.7rem;
		box-shadow: 0 4px 16px rgba(0, 0, 0, 0.35);
	}

	.harness-bar__label {
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: #cbb896;
	}

	.harness-bar__divider {
		width: 1px;
		align-self: stretch;
		background: rgba(241, 230, 211, 0.25);
	}

	.harness-bar__dial {
		display: inline-flex;
		gap: 0.25rem;
	}

	.harness-bar button {
		font-family: inherit;
		font-size: inherit;
		background: rgba(241, 230, 211, 0.08);
		color: #f1e6d3;
		border: 1px solid rgba(241, 230, 211, 0.3);
		border-radius: 0.3rem;
		padding: 0.2rem 0.5rem;
		cursor: pointer;
	}

	.harness-bar button:hover {
		background: rgba(241, 230, 211, 0.18);
	}

	.harness-bar button[aria-pressed='true'] {
		background: #c17f66;
		border-color: #c17f66;
		color: #241209;
	}
</style>
