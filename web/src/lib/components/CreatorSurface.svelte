<script lang="ts">
	import { onMount, tick } from 'svelte';

	import {
		createSessionController,
		type SessionController
	} from '../player-session/session-controller';
	import { actionTextViolation } from '../player-v1/parser';
	import { createSessionState, type SessionState } from '../player-session/session-machine';
	import { projectSessionView } from '../player-session/session-view';
	import type { WorldProjection } from '../server/world-projection';
	import { layoutPlaces } from '../world-view/schematic-layout';
	import { loadWorld } from '../world-view/world-client';
	import ClockStatus from './clock/ClockStatus.svelte';
	import SchematicTopology from './map/SchematicTopology.svelte';
	import ResolutionCard from './resolution/ResolutionCard.svelte';
	import ActionSessionShell from './session/ActionSessionShell.svelte';

	interface Props {
		readonly controllerFactory?: () => SessionController;
		readonly worldLoader?: () => Promise<WorldProjection>;
		readonly keyFactory?: () => string;
	}

	const productionController = (): SessionController =>
		createSessionController({
			fetch: window.fetch.bind(window) as typeof fetch,
			origin: window.location.origin,
			createWebSocket: (url, protocols) => new window.WebSocket(url, protocols)
		});
	const productionWorldLoader = (): Promise<WorldProjection> =>
		loadWorld(window.fetch.bind(window) as typeof fetch, window.location.origin);
	const productionKey = (): string => crypto.randomUUID();

	let {
		controllerFactory = productionController,
		worldLoader = productionWorldLoader,
		keyFactory = productionKey
	}: Props = $props();

	let controller: SessionController | undefined;
	let active = false;
	let worldLoadStarted = false;
	let credential = $state('');
	let actionValue = $state('');
	let sessionState = $state<SessionState>(createSessionState());
	let world = $state<WorldProjection>();
	let worldLoading = $state(false);
	let worldError = $state(false);
	let terminalWrapper = $state<HTMLElement>();
	let lastFocusedTerminalIdentity: string | undefined;

	let sessionView = $derived(projectSessionView(sessionState, actionValue));
	let placesLayout = $derived(world === undefined ? undefined : layoutPlaces(world.places));

	$effect(() => {
		const resolution = sessionView.resolution;
		if (resolution === undefined) return;
		const identity = JSON.stringify([resolution.turn_id, resolution.action_id]);
		if (identity === lastFocusedTerminalIdentity) return;
		lastFocusedTerminalIdentity = identity;
		void tick().then(() => {
			const current = sessionView.resolution;
			if (
				active &&
				current !== undefined &&
				JSON.stringify([current.turn_id, current.action_id]) === identity
			) {
				terminalWrapper?.focus();
			}
		});
	});

	async function startWorldLoad(): Promise<void> {
		worldLoading = true;
		try {
			const projection = await worldLoader();
			if (!active) return;
			world = projection;
		} catch {
			if (active) worldError = true;
		} finally {
			if (active) worldLoading = false;
		}
	}

	function observeState(next: SessionState): void {
		sessionState = next;
		if ('sessionCsrf' in next && typeof next.sessionCsrf === 'string' && !worldLoadStarted) {
			worldLoadStarted = true;
			void startWorldLoad();
		}
	}

	onMount(() => {
		active = true;
		controller = controllerFactory();
		const unsubscribe = controller.subscribe(observeState);
		return () => {
			active = false;
			unsubscribe();
			controller?.destroy();
			controller = undefined;
		};
	});

	function authenticate(event: SubmitEvent): void {
		event.preventDefault();
		const submittedCredential = credential;
		credential = '';
		controller?.dispatch({ type: 'AuthenticateRequested', credential: submittedCredential });
	}

	function submitAction(): void {
		const text = actionValue;
		if (actionTextViolation(text) !== undefined) return;
		controller?.dispatch({ type: 'IntentCreated', text, idempotencyKey: keyFactory() });
		actionValue = '';
	}
</script>

<main>
	<h1>SemMachina</h1>

	{#if sessionState.tag === 'signed_out' || sessionState.tag === 'authenticating'}
		<form
			aria-label="Creator login"
			aria-busy={sessionState.tag === 'authenticating'}
			onsubmit={authenticate}
		>
			<label for="creator-credential">Creator credential</label>
			<input
				id="creator-credential"
				type="password"
				autocomplete="off"
				bind:value={credential}
				disabled={sessionState.tag === 'authenticating'}
			/>
			<button type="submit" disabled={sessionState.tag === 'authenticating'}>Enter world</button>
		</form>
		{#if sessionState.tag === 'signed_out' && sessionState.authenticationRefusal !== undefined}
			<p role="alert">{sessionState.authenticationRefusal.message}</p>
		{/if}
	{/if}

	{#if sessionView.showAction}
		{#if worldLoading}
			<p>Loading world.</p>
		{:else if worldError}
			<p role="alert">World projection unavailable. Session controls are disabled.</p>
		{:else if world !== undefined && placesLayout !== undefined}
			<section aria-label="World overview">
				<SchematicTopology layout={placesLayout} />
				<ClockStatus clock={world.clock} />
			</section>
		{/if}

		{#if !worldError && world !== undefined}
			<ActionSessionShell
				view={sessionView.action}
				onInput={(value) => (actionValue = value)}
				onSubmit={submitAction}
				onReconnect={() => controller?.dispatch({ type: 'ReconnectRequested' })}
			/>

			{#if sessionView.replayExplanation !== undefined}
				<section aria-label="Replay authorization">
					<p>{sessionView.replayExplanation}</p>
					<button
						type="button"
						disabled={!sessionView.canAuthorizeReplay}
						onclick={() => controller?.dispatch({ type: 'ReplayAuthorized' })}
						>Authorize exact replay</button
					>
				</section>
			{/if}

			{#if sessionView.canCheckExact}
				<button type="button" onclick={() => controller?.dispatch({ type: 'CheckExactRequested' })}
					>Check exact result</button
				>
			{/if}

			{#if sessionView.canAcknowledgeRefusal}
				<button type="button" onclick={() => controller?.dispatch({ type: 'RefusalAcknowledged' })}
					>Acknowledge refusal</button
				>
			{/if}

			{#if sessionView.protocolError !== undefined}
				<p role="alert">{sessionView.protocolError}</p>
			{/if}
		{/if}

		{#if sessionView.resolution !== undefined}
			<section
				bind:this={terminalWrapper}
				tabindex="-1"
				aria-label={`Terminal resolution for turn ${sessionView.resolution.turn_id}`}
			>
				<ResolutionCard resolution={sessionView.resolution} />
			</section>
		{/if}

		{#if sessionView.canAcknowledgeTerminal}
			<button type="button" onclick={() => controller?.dispatch({ type: 'TerminalAcknowledged' })}
				>Continue</button
			>
		{/if}
	{/if}
</main>
