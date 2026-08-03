import { describe, expect, it } from 'vitest';
import type { PlaceProjection } from '../server/world-projection';
import { layoutPlaces } from './schematic-layout';

function place(
	id: string,
	connections: readonly string[] = [],
	position?: Readonly<{ latitude: number; longitude: number }>
): PlaceProjection {
	return {
		id,
		label: `Place ${id}`,
		connections,
		...(position === undefined ? {} : { position })
	};
}

describe('layoutPlaces', () => {
	it('is independent of place and connection query order', () => {
		const first = layoutPlaces([place('c'), place('a', ['c', 'b']), place('b', ['c'])]);
		const second = layoutPlaces([place('b', ['c']), place('c'), place('a', ['b', 'c'])]);

		expect(first).toEqual(second);
		expect(first.nodes.map(({ id }) => id)).toEqual(['a', 'b', 'c']);
		expect(first.edges).toEqual([
			{ from: 'a', to: 'b' },
			{ from: 'a', to: 'c' },
			{ from: 'b', to: 'c' }
		]);
	});

	it('keeps authored-only positions exact', () => {
		const result = layoutPlaces([
			place('b', [], { latitude: -12.5, longitude: 179.75 }),
			place('a', ['b'], { latitude: 0, longitude: -0 })
		]);

		expect(result.mode).toBe('authored');
		expect(result.nodes).toEqual([
			{
				id: 'a',
				label: 'Place a',
				position: { kind: 'authored', latitude: 0, longitude: -0 }
			},
			{
				id: 'b',
				label: 'Place b',
				position: { kind: 'authored', latitude: -12.5, longitude: 179.75 }
			}
		]);
	});

	it('lays out a topology-only DAG by longest predecessor path', () => {
		const result = layoutPlaces([
			place('d'),
			place('b', ['d']),
			place('a', ['b', 'c']),
			place('c', ['d'])
		]);

		expect(result.mode).toBe('schematic');
		expect(result.nodes.map((node) => ({ id: node.id, position: node.position }))).toEqual([
			{ id: 'a', position: { kind: 'schematic', x: 0, y: 0 } },
			{ id: 'b', position: { kind: 'schematic', x: 240, y: 0 } },
			{ id: 'c', position: { kind: 'schematic', x: 240, y: 160 } },
			{ id: 'd', position: { kind: 'schematic', x: 480, y: 0 } }
		]);
	});

	it('uses directed edges without inventing reverse edges', () => {
		const result = layoutPlaces([place('a', ['b']), place('b')]);

		expect(result.edges).toEqual([{ from: 'a', to: 'b' }]);
		expect(result.nodes.map((node) => node.position)).toEqual([
			{ kind: 'schematic', x: 0, y: 0 },
			{ kind: 'schematic', x: 240, y: 0 }
		]);
	});

	it('anchors authored nodes while they still participate in topology', () => {
		const result = layoutPlaces([
			place('c'),
			place('a', ['b'], { latitude: 41.9, longitude: -87.6 }),
			place('b', ['c'])
		]);

		expect(result.mode).toBe('mixed');
		expect(result.nodes).toEqual([
			{
				id: 'a',
				label: 'Place a',
				position: { kind: 'authored', latitude: 41.9, longitude: -87.6 }
			},
			{ id: 'b', label: 'Place b', position: { kind: 'schematic', x: 240, y: 0 } },
			{ id: 'c', label: 'Place c', position: { kind: 'schematic', x: 480, y: 0 } }
		]);
	});

	it('condenses cycles and orders SCC members by ID', () => {
		const result = layoutPlaces([
			place('d'),
			place('c', ['d', 'a']),
			place('b', ['a']),
			place('a', ['b', 'c'])
		]);

		expect(result.nodes.map((node) => ({ id: node.id, position: node.position }))).toEqual([
			{ id: 'a', position: { kind: 'schematic', x: 0, y: 0 } },
			{ id: 'b', position: { kind: 'schematic', x: 0, y: 160 } },
			{ id: 'c', position: { kind: 'schematic', x: 0, y: 320 } },
			{ id: 'd', position: { kind: 'schematic', x: 240, y: 0 } }
		]);
	});

	it('keeps multiple components and isolated places visible in lowest-ID order', () => {
		const result = layoutPlaces([
			place('z'),
			place('n'),
			place('b'),
			place('m', ['n']),
			place('a', ['b'])
		]);

		expect(result.nodes.map((node) => ({ id: node.id, position: node.position }))).toEqual([
			{ id: 'a', position: { kind: 'schematic', x: 0, y: 0 } },
			{ id: 'b', position: { kind: 'schematic', x: 240, y: 0 } },
			{ id: 'm', position: { kind: 'schematic', x: 0, y: 160 } },
			{ id: 'n', position: { kind: 'schematic', x: 240, y: 160 } },
			{ id: 'z', position: { kind: 'schematic', x: 0, y: 320 } }
		]);
	});

	it('stays operation-bounded at the supported dense-graph cap', () => {
		const ids = Array.from({ length: 999 }, (_, index) => `p${index.toString().padStart(3, '0')}`);
		const densePlaces = ids.map((id) => place(id, ids));
		const arrayPrototype = Array.prototype as unknown as {
			filter: (
				callback: (value: unknown, index: number, array: unknown[]) => unknown,
				thisArg?: unknown
			) => unknown[];
		};
		const originalFilter = arrayPrototype.filter;
		let predicateVisits = 0;
		arrayPrototype.filter = function (callback, thisArg) {
			return originalFilter.call(this, (value, index, array) => {
				predicateVisits += 1;
				if (predicateVisits > 5_000) throw new Error('filter predicate budget exceeded');
				return callback.call(thisArg, value, index, array);
			});
		};

		let result: ReturnType<typeof layoutPlaces>;
		try {
			result = layoutPlaces(densePlaces);
		} finally {
			arrayPrototype.filter = originalFilter;
		}

		expect(predicateVisits).toBeLessThanOrEqual(5_000);
		expect(result.nodes).toHaveLength(999);
		expect(result.edges).toHaveLength(998_001);
		expect(result.nodes[0]).toMatchObject({
			id: 'p000',
			position: { kind: 'schematic', x: 0, y: 0 }
		});
		expect(result.nodes[500]).toMatchObject({
			id: 'p500',
			position: { kind: 'schematic', x: 0, y: 80_000 }
		});
		expect(result.nodes[998]).toMatchObject({
			id: 'p998',
			position: { kind: 'schematic', x: 0, y: 159_680 }
		});
		expect(result.edges[0]).toEqual({ from: 'p000', to: 'p000' });
		expect(result.edges[998_000]).toEqual({ from: 'p998', to: 'p998' });
	}, 15_000);

	it('returns fresh deeply frozen output without changing frozen inputs', () => {
		const authoredPosition = Object.freeze({ latitude: 10, longitude: 20 });
		const connections = Object.freeze(['b']);
		const places = Object.freeze([
			Object.freeze(place('a', connections, authoredPosition)),
			Object.freeze(place('b'))
		]);
		const snapshot = structuredClone(places);

		const first = layoutPlaces(places);
		const second = layoutPlaces(places);

		expect(places).toEqual(snapshot);
		expect(first).toEqual(second);
		expect(first).not.toBe(second);
		expect(first.nodes).not.toBe(second.nodes);
		expect(first.edges).not.toBe(second.edges);
		expect(first.nodes[0]).not.toBe(second.nodes[0]);
		expect(first.nodes[0].position).not.toBe(authoredPosition);
		expect(Object.isFrozen(first)).toBe(true);
		expect(Object.isFrozen(first.nodes)).toBe(true);
		expect(Object.isFrozen(first.edges)).toBe(true);
		expect(
			first.nodes.every((node) => Object.isFrozen(node) && Object.isFrozen(node.position))
		).toBe(true);
		expect(first.edges.every(Object.isFrozen)).toBe(true);
	});
});
