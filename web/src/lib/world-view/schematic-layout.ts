import type { PlaceProjection } from '../server/world-projection';

export type LayoutMode = 'authored' | 'schematic' | 'mixed';

export type LayoutPosition =
	| Readonly<{
			kind: 'authored';
			latitude: number;
			longitude: number;
	  }>
	| Readonly<{
			kind: 'schematic';
			x: number;
			y: number;
	  }>;

export interface LayoutNode {
	readonly id: string;
	readonly label: string;
	readonly position: LayoutPosition;
}

export interface LayoutEdge {
	readonly from: string;
	readonly to: string;
}

export interface PlacesLayout {
	readonly mode: LayoutMode;
	readonly nodes: readonly LayoutNode[];
	readonly edges: readonly LayoutEdge[];
}

const LAYER_SPACING = 240;
const RANK_SPACING = 160;

function compareIds(left: string, right: string): number {
	return left < right ? -1 : left > right ? 1 : 0;
}

function sortedUnique(values: Iterable<string>): string[] {
	return [...new Set(values)].sort(compareIds);
}

function weakComponents(
	ids: readonly string[],
	neighbors: ReadonlyMap<string, readonly string[]>
): string[][] {
	const visited = new Set<string>();
	const components: string[][] = [];

	for (const id of ids) {
		if (visited.has(id)) continue;
		const component: string[] = [];
		const pending = [id];
		visited.add(id);
		while (pending.length > 0) {
			const current = pending.pop() as string;
			component.push(current);
			for (const neighbor of neighbors.get(current) ?? []) {
				if (visited.has(neighbor)) continue;
				visited.add(neighbor);
				pending.push(neighbor);
			}
		}
		component.sort(compareIds);
		components.push(component);
	}

	return components.sort((left, right) => compareIds(left[0], right[0]));
}

function stronglyConnectedComponents(
	component: readonly string[],
	outgoing: ReadonlyMap<string, readonly string[]>
): string[][] {
	let nextIndex = 0;
	const indexes = new Map<string, number>();
	const lowLinks = new Map<string, number>();
	const stack: string[] = [];
	const onStack = new Set<string>();
	const result: string[][] = [];
	const members = new Set(component);

	function visit(id: string): void {
		const index = nextIndex++;
		indexes.set(id, index);
		lowLinks.set(id, index);
		stack.push(id);
		onStack.add(id);

		for (const target of outgoing.get(id) ?? []) {
			if (!members.has(target)) continue;
			if (!indexes.has(target)) {
				visit(target);
				lowLinks.set(id, Math.min(lowLinks.get(id) as number, lowLinks.get(target) as number));
			} else if (onStack.has(target)) {
				lowLinks.set(id, Math.min(lowLinks.get(id) as number, indexes.get(target) as number));
			}
		}

		if (lowLinks.get(id) !== indexes.get(id)) return;
		const stronglyConnected: string[] = [];
		let member: string;
		do {
			member = stack.pop() as string;
			onStack.delete(member);
			stronglyConnected.push(member);
		} while (member !== id);
		stronglyConnected.sort(compareIds);
		result.push(stronglyConnected);
	}

	for (const id of component) {
		if (!indexes.has(id)) visit(id);
	}

	return result.sort((left, right) => compareIds(left[0], right[0]));
}

function componentPositions(
	component: readonly string[],
	outgoing: ReadonlyMap<string, readonly string[]>,
	componentOffset: number
): { positions: Map<string, Readonly<{ x: number; y: number }>>; height: number } {
	const sccs = stronglyConnectedComponents(component, outgoing);
	const sccByMember = new Map<string, number>();
	for (const [sccIndex, scc] of sccs.entries()) {
		for (const member of scc) sccByMember.set(member, sccIndex);
	}

	const successors = sccs.map(() => new Set<number>());
	const predecessors = sccs.map(() => new Set<number>());
	for (const from of component) {
		const fromScc = sccByMember.get(from) as number;
		for (const to of outgoing.get(from) ?? []) {
			const toScc = sccByMember.get(to);
			if (toScc === undefined || toScc === fromScc) continue;
			successors[fromScc].add(toScc);
			predecessors[toScc].add(fromScc);
		}
	}

	const indegrees = predecessors.map((entries) => entries.size);
	const layers = sccs.map(() => 0);
	const ready = indegrees
		.map((degree, index) => ({ degree, index }))
		.filter(({ degree }) => degree === 0)
		.map(({ index }) => index)
		.sort((left, right) => compareIds(sccs[left][0], sccs[right][0]));

	while (ready.length > 0) {
		const current = ready.shift() as number;
		const orderedSuccessors = [...successors[current]].sort((left, right) =>
			compareIds(sccs[left][0], sccs[right][0])
		);
		for (const successor of orderedSuccessors) {
			layers[successor] = Math.max(layers[successor], layers[current] + 1);
			indegrees[successor] -= 1;
			if (indegrees[successor] === 0) {
				ready.push(successor);
				ready.sort((left, right) => compareIds(sccs[left][0], sccs[right][0]));
			}
		}
	}

	const sccsByLayer = new Map<number, number[]>();
	for (const [sccIndex, layer] of layers.entries()) {
		const inLayer = sccsByLayer.get(layer) ?? [];
		inLayer.push(sccIndex);
		sccsByLayer.set(layer, inLayer);
	}

	const positions = new Map<string, Readonly<{ x: number; y: number }>>();
	let maxRankCount = 0;
	for (const [layer, inLayer] of sccsByLayer) {
		inLayer.sort((left, right) => compareIds(sccs[left][0], sccs[right][0]));
		let rank = 0;
		for (const sccIndex of inLayer) {
			for (const id of sccs[sccIndex]) {
				positions.set(id, { x: layer * LAYER_SPACING, y: componentOffset + rank * RANK_SPACING });
				rank += 1;
			}
		}
		maxRankCount = Math.max(maxRankCount, rank);
	}

	return { positions, height: maxRankCount * RANK_SPACING };
}

export function layoutPlaces(places: readonly PlaceProjection[]): PlacesLayout {
	const sortedPlaces = [...places].sort((left, right) => compareIds(left.id, right.id));
	const placeIds = new Set(sortedPlaces.map(({ id }) => id));
	const outgoingSets = new Map<string, Set<string>>();
	const weakNeighborSets = new Map<string, Set<string>>();
	for (const { id } of sortedPlaces) {
		outgoingSets.set(id, new Set());
		weakNeighborSets.set(id, new Set());
	}

	for (const place of sortedPlaces) {
		for (const to of place.connections) {
			if (!placeIds.has(to)) continue;
			outgoingSets.get(place.id)?.add(to);
			weakNeighborSets.get(place.id)?.add(to);
			weakNeighborSets.get(to)?.add(place.id);
		}
	}

	const outgoing = new Map<string, readonly string[]>();
	const weakNeighbors = new Map<string, readonly string[]>();
	const edgePairs: Array<Readonly<{ from: string; to: string }>> = [];
	for (const { id } of sortedPlaces) {
		const targets = sortedUnique(outgoingSets.get(id) ?? []);
		outgoing.set(id, targets);
		weakNeighbors.set(id, sortedUnique(weakNeighborSets.get(id) ?? []));
		for (const to of targets) edgePairs.push({ from: id, to });
	}

	const schematicPositions = new Map<string, Readonly<{ x: number; y: number }>>();
	let componentOffset = 0;
	for (const component of weakComponents(
		sortedPlaces.map(({ id }) => id),
		weakNeighbors
	)) {
		const { positions, height } = componentPositions(component, outgoing, componentOffset);
		for (const [id, position] of positions) schematicPositions.set(id, position);
		componentOffset += height;
	}

	const nodes = sortedPlaces.map((place): LayoutNode => {
		const position: LayoutPosition =
			place.position === undefined
				? Object.freeze({
						kind: 'schematic',
						...(schematicPositions.get(place.id) as { x: number; y: number })
					})
				: Object.freeze({
						kind: 'authored',
						latitude: place.position.latitude,
						longitude: place.position.longitude
					});
		return Object.freeze({ id: place.id, label: place.label, position });
	});
	const edges = edgePairs.map(({ from, to }): LayoutEdge => Object.freeze({ from, to }));
	const authoredCount = sortedPlaces.filter(({ position }) => position !== undefined).length;
	const mode: LayoutMode =
		authoredCount === 0
			? 'schematic'
			: authoredCount === sortedPlaces.length
				? 'authored'
				: 'mixed';

	return Object.freeze({ mode, nodes: Object.freeze(nodes), edges: Object.freeze(edges) });
}
