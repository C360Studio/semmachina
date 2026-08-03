export interface ActionSessionShellView {
	readonly label: string;
	readonly value: string;
	readonly busy: boolean;
	readonly disabled: boolean;
	readonly inputDisabled?: boolean;
	readonly submitDisabled?: boolean;
	readonly refusal?: Readonly<{
		message: string;
		focusRequestId: string;
	}>;
	readonly reconnect?: Readonly<{
		text: string;
		available: boolean;
	}>;
	readonly liveStatus?: Readonly<{
		announcementId: string;
		text: string;
	}>;
}

export interface ActionSessionShellProps {
	readonly view: Readonly<ActionSessionShellView>;
	readonly onInput: (value: string) => void;
	readonly onSubmit: () => void;
	readonly onReconnect: () => void;
}
