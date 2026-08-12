<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';

	import {
		createSessionController,
		type SessionController
	} from '../player-session/session-controller';
	import { actionTextViolation } from '../player-v1/parser';
	import { createSessionState, type SessionState } from '../player-session/session-machine';
	import { projectSessionView } from '../player-session/session-view';
	import type { ResolutionView } from '../player-view/resolution-projection';
	import type { WorldProjection } from '../server/world-projection';
	import { layoutPlaces } from '../world-view/schematic-layout';
	import { loadWorld } from '../world-view/world-client';
	import ClockStatus from './clock/ClockStatus.svelte';
	import SchematicTopology from './map/SchematicTopology.svelte';
	import ResolutionCard from './resolution/ResolutionCard.svelte';
	import ActionSessionShell from './session/ActionSessionShell.svelte';
	import type { Presentation } from './presentation';

	interface Props {
		readonly controllerFactory?: () => SessionController;
		readonly worldLoader?: () => Promise<WorldProjection>;
		readonly keyFactory?: () => string;
		readonly presentation?: Presentation;
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
		keyFactory = productionKey,
		presentation = 'fiction'
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

	// UI-side turn history. This is presentation state only, layered on top of
	// the session machine's single "current resolution" — the machine itself
	// still only ever tracks the latest terminal delivery. Each newly seen
	// resolution identity is appended; the entry matching the CURRENT
	// resolution is filtered back out at render time since it already renders
	// through the focus-managed section below.
	interface HistoryEntry {
		readonly playerText: string;
		readonly resolution: ResolutionView;
	}

	// Caps how many past turns the UI carries so an unbounded session (left
	// open for hours of play) can't grow this array — and the DOM it renders
	// into — without limit.
	const MAX_HISTORY_ENTRIES = 200;

	let history = $state<HistoryEntry[]>([]);
	// A Set (not just the most-recently-seen identity) so a resolution can't
	// re-enter history after it scrolls out of the capped array above.
	const seenHistoryIdentities = new SvelteSet<string>();

	$effect(() => {
		const resolution = sessionView.resolution;
		if (resolution === undefined) return;
		const identity = JSON.stringify([resolution.turn_id, resolution.action_id]);
		if (seenHistoryIdentities.has(identity)) return;
		seenHistoryIdentities.add(identity);
		const playerText = sessionState.tag === 'terminal' ? sessionState.intent.text : '';
		const next = [...history, { playerText, resolution }];
		history =
			next.length > MAX_HISTORY_ENTRIES ? next.slice(next.length - MAX_HISTORY_ENTRIES) : next;
	});

	// Re-authentication (sign-out, then a fresh sign-in) starts a new
	// authentication generation. Turn history is scoped to ONE authenticated
	// session, so a generation bump resets it — otherwise the previous
	// player's turns would render into the newly authenticated session.
	let lastAuthenticationGeneration: number | undefined;

	$effect(() => {
		const generation = sessionState.authenticationGeneration;
		if (lastAuthenticationGeneration === undefined) {
			lastAuthenticationGeneration = generation;
			return;
		}
		if (generation === lastAuthenticationGeneration) return;
		lastAuthenticationGeneration = generation;
		history = [];
		seenHistoryIdentities.clear();
		lastFocusedTerminalIdentity = undefined;
	});

	let currentIdentity = $derived(
		sessionView.resolution === undefined
			? undefined
			: JSON.stringify([sessionView.resolution.turn_id, sessionView.resolution.action_id])
	);
	let pastHistory = $derived(
		history.filter(
			(entry) =>
				JSON.stringify([entry.resolution.turn_id, entry.resolution.action_id]) !== currentIdentity
		)
	);

	async function startWorldLoad(): Promise<void> {
		if (worldLoading) return;
		worldError = false;
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

	function retryWorldLoad(): void {
		void startWorldLoad();
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
	{#if sessionState.tag === 'signed_out' || sessionState.tag === 'authenticating'}
		<div class="login-screen">
			<div class="login-card">
				<h1 class="wordmark">SemMachina</h1>
				<p class="surface-role">Creator surface</p>
				<p class="tagline">Where the story and the world agree.</p>

				<form
					aria-label="Creator login"
					aria-busy={sessionState.tag === 'authenticating'}
					onsubmit={authenticate}
				>
					<label for="creator-credential">World passphrase</label>
					<input
						id="creator-credential"
						type="password"
						autocomplete="off"
						aria-describedby="creator-credential-hint"
						bind:value={credential}
						disabled={sessionState.tag === 'authenticating'}
					/>
					<p class="field-hint" id="creator-credential-hint">
						The access passphrase set when this world was deployed.
					</p>
					<button type="submit" disabled={sessionState.tag === 'authenticating'}>Enter world</button
					>
				</form>
				{#if sessionState.tag === 'signed_out' && sessionState.authenticationRefusal !== undefined}
					<p role="alert">{sessionState.authenticationRefusal.message}</p>
				{/if}
			</div>
		</div>
	{/if}

	{#if sessionView.showAction}
		<div class="app-shell">
			<header class="app-header">
				<h1 class="wordmark wordmark--compact">
					SemMachina <span class="surface-role surface-role--inline">Creator surface</span>
				</h1>
			</header>

			<div class="shell-grid surface-layout">
				<div class="story-column">
					{#if worldLoading}
						<p class="status-line" role="status" aria-live="polite">Loading world.</p>
					{/if}
					{#if worldError}
						<div class="callout" role="alert">
							<p>World projection unavailable. Session controls are disabled.</p>
							<button type="button" disabled={worldLoading} onclick={retryWorldLoad}
								>Retry world projection</button
							>
						</div>
					{/if}

					{#each pastHistory as entry (entry.resolution.turn_id + ':' + entry.resolution.action_id)}
						<div class="history-entry">
							{#if entry.playerText !== ''}
								<p class="history-entry__player">You: {entry.playerText}</p>
							{/if}
							{#if entry.resolution.narration !== undefined}
								<p class="history-entry__prose">{entry.resolution.narration.prose}</p>
							{/if}
							{#if entry.resolution.band !== undefined}
								<span class="chip history-entry__chip" data-band={entry.resolution.band}
									>{entry.resolution.band}</span
								>
							{/if}
						</div>
					{/each}

					{#if sessionView.resolution !== undefined}
						<section
							bind:this={terminalWrapper}
							tabindex="-1"
							aria-label={`Terminal resolution for turn ${sessionView.resolution.turn_id}`}
							class="terminal-section"
						>
							<ResolutionCard resolution={sessionView.resolution} {presentation} />
						</section>
					{/if}

					{#if sessionView.canAcknowledgeTerminal}
						<button
							type="button"
							class="inline-button"
							onclick={() => controller?.dispatch({ type: 'TerminalAcknowledged' })}
							>Continue</button
						>
					{/if}

					{#if !worldError && world !== undefined}
						{#if sessionView.replayExplanation !== undefined}
							<section aria-label="Replay authorization" class="callout">
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
							<button
								type="button"
								onclick={() => controller?.dispatch({ type: 'CheckExactRequested' })}
								>Check exact result</button
							>
						{/if}

						{#if sessionView.canAcknowledgeRefusal}
							<button
								type="button"
								class="inline-button"
								onclick={() => controller?.dispatch({ type: 'RefusalAcknowledged' })}
								>Acknowledge refusal</button
							>
						{/if}

						{#if sessionView.protocolError !== undefined}
							<p role="alert">{sessionView.protocolError}</p>
						{/if}
					{/if}
				</div>

				{#if world !== undefined && placesLayout !== undefined}
					<aside class="side-panel" aria-label="World overview">
						<p class="map-caption">Map</p>
						<SchematicTopology layout={placesLayout} />
						<ClockStatus clock={world.clock} />
					</aside>
				{/if}
			</div>

			{#if !worldError && world !== undefined}
				<div class="action-bar">
					<div class="shell-grid action-bar-inner">
						<ActionSessionShell
							view={sessionView.action}
							onInput={(value) => (actionValue = value)}
							onSubmit={submitAction}
							onReconnect={() => controller?.dispatch({ type: 'ReconnectRequested' })}
						/>
					</div>
				</div>
			{/if}
		</div>
	{/if}
</main>

<style>
	.login-screen {
		min-height: 100dvh;
		display: grid;
		place-items: center;
		padding: var(--space-5);
	}

	.login-card {
		width: 100%;
		max-width: 24rem;
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-md);
		padding: var(--space-6);
		display: grid;
		gap: var(--space-4);
		text-align: center;
	}

	.login-card form {
		display: grid;
		gap: var(--space-3);
		text-align: left;
		margin-top: var(--space-2);
	}

	.wordmark {
		margin: 0;
		font-family: var(--font-sans);
		font-weight: 700;
		letter-spacing: 0.18em;
		text-transform: uppercase;
		font-size: var(--font-size-lg);
		color: var(--color-accent);
	}

	.wordmark--compact {
		font-size: var(--font-size-sm);
		letter-spacing: 0.14em;
	}

	.tagline {
		margin: 0;
		color: var(--color-ink-muted);
		font-size: var(--font-size-sm);
	}

	.surface-role {
		margin: 0;
		color: var(--color-ink-muted);
		font-size: var(--font-size-xs);
		font-weight: 600;
		letter-spacing: 0.12em;
		text-transform: uppercase;
	}

	.surface-role--inline {
		margin-inline-start: var(--space-2);
		letter-spacing: 0.1em;
	}

	.field-hint {
		margin: 0;
		color: var(--color-ink-muted);
		font-size: var(--font-size-xs);
	}

	.app-shell {
		min-height: 100dvh;
		display: flex;
		flex-direction: column;
	}

	.app-header {
		padding: var(--space-3) var(--space-5);
		border-bottom: 1px solid var(--color-border);
	}

	.shell-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-6);
		max-width: 68rem;
		width: 100%;
		margin-inline: auto;
		padding-inline: var(--space-5);
		box-sizing: border-box;
	}

	@media (min-width: 64rem) {
		.shell-grid {
			grid-template-columns: minmax(0, 46rem) 20rem;
		}
	}

	.surface-layout {
		flex: 1;
		padding-block: var(--space-6);
		align-items: start;
	}

	.story-column {
		min-width: 0;
		max-width: 46rem;
		display: grid;
		gap: var(--space-4);
		align-content: start;
	}

	.side-panel {
		min-width: 0;
		display: grid;
		gap: var(--space-4);
		align-content: start;
	}

	.map-caption {
		margin: 0;
		font-size: var(--font-size-sm);
		font-weight: 600;
		color: var(--color-ink-muted);
	}

	.status-line {
		margin: 0;
		color: var(--color-ink-muted);
		font-size: var(--font-size-sm);
	}

	.callout {
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
		padding: var(--space-4);
		display: grid;
		gap: var(--space-3);
	}

	.history-entry {
		display: grid;
		gap: var(--space-1);
		padding-inline-start: var(--space-3);
		border-inline-start: 2px solid var(--color-border);
		opacity: 0.7;
	}

	.history-entry__player {
		margin: 0;
		font-size: var(--font-size-sm);
		font-weight: 600;
		color: var(--color-ink-muted);
		text-align: end;
	}

	.history-entry__prose {
		margin: 0;
		font-family: var(--font-serif);
		font-size: var(--font-size-md);
		line-height: var(--line-height-prose);
	}

	.history-entry__chip {
		justify-self: start;
		display: inline-block;
		background: var(--color-neutral-surface);
		color: var(--color-neutral);
		border-radius: var(--radius-pill);
		padding: 0.1rem var(--space-2);
		font-size: var(--font-size-xs);
		font-weight: 600;
	}

	.history-entry__chip[data-band='full'] {
		background: var(--color-success-surface);
		color: var(--color-success);
	}

	.history-entry__chip[data-band='partial'] {
		background: var(--color-amber-surface);
		color: var(--color-amber);
	}

	.history-entry__chip[data-band='miss'] {
		background: var(--color-danger-surface);
		color: var(--color-danger);
	}

	.terminal-section {
		outline: none;
	}

	/* This section receives a programmatic .focus() after each turn resolves
	   (see the focus-management effect above). Programmatic focus does not
	   reliably satisfy :focus-visible heuristics, so the ring is styled on
	   plain :focus to guarantee it's visible when focus lands here. */
	.terminal-section:focus {
		outline: 2px solid var(--color-focus-ring);
		outline-offset: 4px;
	}

	.inline-button {
		justify-self: start;
	}

	.action-bar {
		position: sticky;
		bottom: 0;
		background: var(--color-surface);
		border-top: 1px solid var(--color-border);
		box-shadow: var(--shadow-action-bar);
		padding-block: var(--space-3);
	}

	.action-bar-inner {
		padding-block: 0;
	}
</style>
