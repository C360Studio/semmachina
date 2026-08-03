import { describe, expect, it, vi } from 'vitest';

import { createBoundedRelayQueue } from './player-relay-queue';

describe('bounded relay write queue', () => {
	it('counts the active write plus FIFO and preserves exact payload bytes', () => {
		const completions: Array<(error?: Error) => void> = [];
		const send = vi.fn((_payload: Buffer, complete: (error?: Error) => void) => {
			completions.push(complete);
		});
		const overflow = vi.fn();
		const queue = createBoundedRelayQueue({ maxMessages: 2, maxBytes: 5, send, overflow });
		const first = Buffer.from([0, 1]);
		const second = Buffer.from([2, 3, 4]);
		expect(queue.enqueue(first)).toBe(true);
		expect(queue.enqueue(second)).toBe(true);
		expect(queue.snapshot()).toEqual({ messages: 2, bytes: 5, active: true });
		expect(queue.enqueue(Buffer.from([5]))).toBe(false);
		expect(overflow).toHaveBeenCalledOnce();
		expect(send).toHaveBeenCalledTimes(1);
		expect(send.mock.calls[0][0]).toEqual(first);
		completions.shift()?.();
		expect(send).toHaveBeenCalledTimes(2);
		expect(send.mock.calls[1][0]).toEqual(second);
		completions.shift()?.();
		expect(queue.snapshot()).toEqual({ messages: 0, bytes: 0, active: false });
	});

	it('stops idempotently, discards queued work, and ignores late callbacks', () => {
		let complete: ((error?: Error) => void) | undefined;
		const send = vi.fn((_payload: Buffer, callback: (error?: Error) => void) => {
			complete = callback;
		});
		const queue = createBoundedRelayQueue({
			maxMessages: 8,
			maxBytes: 100,
			send,
			overflow: vi.fn()
		});
		queue.enqueue(Buffer.from('one'));
		queue.enqueue(Buffer.from('two'));
		queue.stop();
		queue.stop();
		expect(queue.snapshot()).toEqual({ messages: 0, bytes: 0, active: false });
		complete?.();
		expect(send).toHaveBeenCalledOnce();
		expect(queue.enqueue(Buffer.from('later'))).toBe(false);
	});

	it('contains a synchronous injected send throw and reports one stable failure', () => {
		const failed = vi.fn();
		const queue = createBoundedRelayQueue({
			maxMessages: 1,
			maxBytes: 10,
			send: () => {
				throw new Error('sync send failure');
			},
			overflow: vi.fn(),
			failed
		});
		expect(() => queue.enqueue(Buffer.from('text'))).not.toThrow();
		expect(failed).toHaveBeenCalledOnce();
	});
});
