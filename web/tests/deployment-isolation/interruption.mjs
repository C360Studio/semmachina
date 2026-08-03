export class IsolationInterruptedError extends Error {
	constructor() {
		super('surface isolation interrupted');
	}
}

export function createSerializedInterruption() {
	let signal;
	let inFlight = Promise.resolve();
	let activeController;

	return Object.freeze({
		interrupt(received) {
			if (signal === undefined && ['SIGINT', 'SIGTERM'].includes(received)) {
				signal = received;
				activeController?.abort();
			}
		},
		async run(operation) {
			if (signal !== undefined) throw new IsolationInterruptedError();
			const controller = new AbortController();
			activeController = controller;
			const current = Promise.resolve().then(() => operation(controller.signal));
			inFlight = current.then(
				() => undefined,
				() => undefined
			);
			try {
				return await current;
			} finally {
				if (activeController === controller) activeController = undefined;
			}
		},
		async settled() {
			await inFlight;
		},
		exitCode() {
			return signal === 'SIGINT' ? 130 : signal === 'SIGTERM' ? 143 : 0;
		},
		isInterrupted() {
			return signal !== undefined;
		}
	});
}
