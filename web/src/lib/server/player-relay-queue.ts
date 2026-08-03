export interface RelayQueueOptions {
	readonly maxMessages: number;
	readonly maxBytes: number;
	readonly send: (payload: Buffer, complete: (error?: Error) => void) => void;
	readonly overflow: () => void;
	readonly failed?: (error: Error) => void;
}

export interface BoundedRelayQueue {
	readonly enqueue: (payload: Buffer) => boolean;
	readonly stop: () => void;
	readonly snapshot: () => { messages: number; bytes: number; active: boolean };
}

export function createBoundedRelayQueue(options: RelayQueueOptions): BoundedRelayQueue {
	const queued: Buffer[] = [];
	let current: Buffer | undefined;
	let bytes = 0;
	let stopped = false;
	let generation = 0;

	function pump(): void {
		if (stopped || current !== undefined) return;
		current = queued.shift();
		if (current === undefined) return;
		const sent = current;
		const sendGeneration = generation;
		const complete = (error?: Error) => {
			if (stopped || generation !== sendGeneration || current !== sent) return;
			if (error !== undefined) {
				try {
					options.failed?.(error);
				} catch {
					// An injected failure sink cannot escape the bounded queue boundary.
				}
				return;
			}
			bytes -= sent.byteLength;
			current = undefined;
			pump();
		};
		try {
			options.send(sent, complete);
		} catch {
			complete(new Error('relay send failed'));
		}
	}

	function enqueue(payload: Buffer): boolean {
		if (stopped) return false;
		const messages = queued.length + (current === undefined ? 0 : 1);
		if (messages + 1 > options.maxMessages || bytes + payload.byteLength > options.maxBytes) {
			try {
				options.overflow();
			} catch {
				// An injected overflow sink cannot escape the bounded queue boundary.
			}
			return false;
		}
		queued.push(payload);
		bytes += payload.byteLength;
		pump();
		return true;
	}

	function stop(): void {
		if (stopped) return;
		stopped = true;
		generation += 1;
		queued.length = 0;
		current = undefined;
		bytes = 0;
	}

	return Object.freeze({
		enqueue,
		stop,
		snapshot: () =>
			Object.freeze({
				messages: queued.length + (current === undefined ? 0 : 1),
				bytes,
				active: current !== undefined
			})
	});
}
