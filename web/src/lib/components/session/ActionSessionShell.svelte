<script lang="ts">
	import { actionTextViolation, MAX_ACTION_TEXT_BYTES } from '../../player-v1/parser';
	import type { ActionSessionShellProps } from './ActionSessionShell.types';

	let { view, onInput, onSubmit, onReconnect }: ActionSessionShellProps = $props();

	const componentId = $props.id();
	const refusalId = `${componentId}-refusal`;
	const validationId = `${componentId}-validation`;
	let actionInput: HTMLInputElement;
	let touched = $state(false);
	let draftValue = $derived(view.value);
	let lastFocusRequestId: string | undefined;
	let lastAnnouncementId: string | undefined;
	let announcedStatus = $state<Readonly<{ announcementId: string; text: string }>>();

	const inputDisabled = $derived(view.busy || (view.inputDisabled ?? view.disabled));
	const textViolation = $derived(actionTextViolation(draftValue));
	const validationMessage = $derived(
		touched
			? textViolation === 'blank'
				? 'Enter a nonblank action.'
				: textViolation === 'too_long'
					? `Action text exceeds ${MAX_ACTION_TEXT_BYTES} bytes.`
					: undefined
			: undefined
	);
	const submitDisabled = $derived(
		view.busy || (view.submitDisabled ?? view.disabled) || textViolation !== undefined
	);
	const describedBy = $derived(
		[
			view.refusal === undefined ? undefined : refusalId,
			validationMessage === undefined ? undefined : validationId
		]
			.filter((value) => value !== undefined)
			.join(' ') || undefined
	);

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
		touched = true;
		draftValue = (event.currentTarget as HTMLInputElement).value;
		onInput(draftValue);
	}

	function handleSubmit(event: SubmitEvent): void {
		event.preventDefault();
		touched = true;
		if (textViolation !== undefined) {
			actionInput?.focus();
			return;
		}
		if (!submitDisabled) {
			onSubmit();
			touched = false;
		}
	}
</script>

<section aria-label="Action session">
	<form aria-label="Action submission" aria-busy={view.busy} onsubmit={handleSubmit}>
		<label for={`${componentId}-action`}>{view.label}</label>
		<input
			bind:this={actionInput}
			id={`${componentId}-action`}
			type="text"
			value={draftValue}
			disabled={inputDisabled}
			required
			maxlength={MAX_ACTION_TEXT_BYTES}
			aria-describedby={describedBy}
			oninput={handleInput}
		/>
		<button type="submit" disabled={submitDisabled}>Submit</button>
	</form>

	{#if view.refusal !== undefined}
		<p id={refusalId}>{view.refusal.message}</p>
	{/if}
	{#if validationMessage !== undefined}
		<p id={validationId} role="alert">{validationMessage}</p>
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
