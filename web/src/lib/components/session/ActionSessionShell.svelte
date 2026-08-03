<script lang="ts">
	import type { ActionSessionShellProps } from './ActionSessionShell.types';

	let { view, onInput, onSubmit, onReconnect }: ActionSessionShellProps = $props();

	const componentId = $props.id();
	const refusalId = `${componentId}-refusal`;
	let actionInput: HTMLInputElement;
	let lastFocusRequestId: string | undefined;
	let lastAnnouncementId: string | undefined;
	let announcedStatus = $state<Readonly<{ announcementId: string; text: string }>>();

	const inputDisabled = $derived(view.busy || (view.inputDisabled ?? view.disabled));
	const submitDisabled = $derived(view.busy || (view.submitDisabled ?? view.disabled));

	$effect(() => {
		const focusRequestId = view.refusal?.focusRequestId;
		if (focusRequestId === undefined || focusRequestId === lastFocusRequestId) return;

		lastFocusRequestId = focusRequestId;
		actionInput?.focus();
	});

	$effect(() => {
		const status = view.liveStatus;
		if (status === undefined || status.announcementId === lastAnnouncementId) return;

		lastAnnouncementId = status.announcementId;
		announcedStatus = status;
	});

	function handleInput(event: Event): void {
		onInput((event.currentTarget as HTMLInputElement).value);
	}

	function handleSubmit(event: SubmitEvent): void {
		event.preventDefault();
		if (!submitDisabled) onSubmit();
	}
</script>

<section aria-label="Action session">
	<form aria-label="Action submission" aria-busy={view.busy} onsubmit={handleSubmit}>
		<label for={`${componentId}-action`}>{view.label}</label>
		<input
			bind:this={actionInput}
			id={`${componentId}-action`}
			type="text"
			value={view.value}
			disabled={inputDisabled}
			aria-describedby={view.refusal === undefined ? undefined : refusalId}
			oninput={handleInput}
		/>
		<button type="submit" disabled={submitDisabled}>Submit</button>
	</form>

	{#if view.refusal !== undefined}
		<p id={refusalId}>{view.refusal.message}</p>
	{/if}

	{#if view.reconnect !== undefined}
		<div>
			<p>{view.reconnect.text}</p>
			<button type="button" disabled={!view.reconnect.available} onclick={onReconnect}
				>Reconnect</button
			>
		</div>
	{/if}

	<p role="status" aria-live="polite" aria-atomic="true">
		{#if announcedStatus !== undefined}
			{#key announcedStatus.announcementId}
				{announcedStatus.text}
			{/key}
		{/if}
	</p>
</section>
