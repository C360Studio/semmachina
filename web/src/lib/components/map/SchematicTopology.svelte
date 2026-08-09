<script lang="ts">
	import type { LayoutEdge, LayoutNode, PlacesLayout } from '../../world-view/schematic-layout';

	interface Point {
		readonly x: number;
		readonly y: number;
	}

	interface Props {
		readonly layout: PlacesLayout;
		readonly onNodeActivate?: (nodeId: string) => void;
	}

	// Panel-legibility constants. The panel this renders into is ~20rem wide
	// (≈300px of actual SVG); node x/y positions come from the schematic-layout
	// module (off-limits) and can easily span 700+ user units, so the viewBox
	// gets scaled down by roughly half. A larger source font size and a tighter
	// label-width estimate (less viewBox inflation from long labels) both push
	// the effective on-panel text size up without touching node coordinates.
	const NODE_HALF_SIZE = 16;
	const LABEL_OFFSET = 22;
	// Measured glyph width ≈8.6px/char at the 18px label font below; rounded up
	// so long labels don't under-budget against the fixed LABEL_OFFSET padding.
	const LABEL_CHARACTER_ALLOWANCE = 9;
	const LOOP_EXTENT = 48;
	const VISUAL_PADDING = 32;
	const EMPTY_VIEW_BOX = '-32 -32 64 64';

	let { layout, onNodeActivate }: Props = $props();
	let nodeById = $derived(new Map(layout.nodes.map((node) => [node.id, node])));
	let hasFallbackPositions = $derived(
		layout.nodes.some((node) => node.position.kind === 'schematic')
	);
	let visualViewBox = $derived(viewBox(layout.nodes, layout.edges));

	function xCoordinate(node: LayoutNode): number {
		return node.position.kind === 'authored' ? node.position.longitude : node.position.x;
	}

	function yCoordinate(node: LayoutNode): number {
		return node.position.kind === 'authored' ? node.position.latitude : node.position.y;
	}

	function transform(node: LayoutNode): string {
		return `translate(${xCoordinate(node)} ${yCoordinate(node)})`;
	}

	function coincides(source: LayoutNode, target: LayoutNode): boolean {
		return (
			xCoordinate(source) === xCoordinate(target) && yCoordinate(source) === yCoordinate(target)
		);
	}

	function loopPath(node: LayoutNode): string {
		const x = xCoordinate(node);
		const y = yCoordinate(node);
		return `M ${x + NODE_HALF_SIZE} ${y} C ${x + LOOP_EXTENT} ${y - LOOP_EXTENT} ${x - LOOP_EXTENT} ${y - LOOP_EXTENT} ${x - NODE_HALF_SIZE} ${y}`;
	}

	function viewBox(nodes: readonly LayoutNode[], edges: readonly LayoutEdge[]): string {
		if (nodes.length === 0) return EMPTY_VIEW_BOX;

		let left = Number.POSITIVE_INFINITY;
		let top = Number.POSITIVE_INFINITY;
		let right = Number.NEGATIVE_INFINITY;
		let bottom = Number.NEGATIVE_INFINITY;
		for (const node of nodes) {
			const x = xCoordinate(node);
			const y = yCoordinate(node);
			left = Math.min(left, x - NODE_HALF_SIZE);
			top = Math.min(top, y - NODE_HALF_SIZE);
			right = Math.max(
				right,
				x + LABEL_OFFSET + Array.from(node.label).length * LABEL_CHARACTER_ALLOWANCE
			);
			bottom = Math.max(bottom, y + NODE_HALF_SIZE);
		}

		const nodesById = new Map(nodes.map((node) => [node.id, node]));
		for (const edge of edges) {
			const source = nodesById.get(edge.from);
			const target = nodesById.get(edge.to);
			if (source === undefined || target === undefined || !coincides(source, target)) continue;
			const x = xCoordinate(source);
			const y = yCoordinate(source);
			left = Math.min(left, x - LOOP_EXTENT);
			top = Math.min(top, y - LOOP_EXTENT);
			right = Math.max(right, x + LOOP_EXTENT);
		}

		const paddedLeft = left - VISUAL_PADDING;
		const paddedTop = top - VISUAL_PADDING;
		return `${paddedLeft} ${paddedTop} ${right - left + VISUAL_PADDING * 2} ${bottom - top + VISUAL_PADDING * 2}`;
	}

	function targetEndpoint(source: LayoutNode, target: LayoutNode): Point {
		const targetX = xCoordinate(target);
		const targetY = yCoordinate(target);
		const towardSourceX = xCoordinate(source) - targetX;
		const towardSourceY = yCoordinate(source) - targetY;

		if (target.position.kind === 'authored') {
			const distance = Math.hypot(towardSourceX, towardSourceY);
			if (distance === 0) return { x: targetX - NODE_HALF_SIZE, y: targetY };
			return {
				x: targetX + (towardSourceX / distance) * NODE_HALF_SIZE,
				y: targetY + (towardSourceY / distance) * NODE_HALF_SIZE
			};
		}

		const largestDelta = Math.max(Math.abs(towardSourceX), Math.abs(towardSourceY));
		if (largestDelta === 0) return { x: targetX - NODE_HALF_SIZE, y: targetY };
		const scale = NODE_HALF_SIZE / largestDelta;
		return {
			x: targetX + towardSourceX * scale,
			y: targetY + towardSourceY * scale
		};
	}

	function nodeLabel(id: string): string {
		return nodeById.get(id)?.label ?? id;
	}

	function positionDescription(node: LayoutNode): string {
		if (node.position.kind === 'authored') {
			return `Authored position: latitude ${node.position.latitude}, longitude ${node.position.longitude}.`;
		}
		return `Schematic position: x ${node.position.x}, y ${node.position.y}.`;
	}
</script>

<section class="topology" aria-label="World topology">
	{#if hasFallbackPositions}
		<p class="mode-label">Schematic mode — some positions are inferred.</p>
	{:else}
		<p class="mode-label">Authored topology.</p>
	{/if}

	<svg
		data-topology-visual
		aria-hidden="true"
		viewBox={visualViewBox}
		preserveAspectRatio="xMidYMid meet"
	>
		<defs>
			<marker
				id="topology-arrow"
				viewBox="0 0 10 10"
				refX="10"
				refY="5"
				markerWidth="7"
				markerHeight="7"
				orient="auto-start-reverse"
			>
				<path d="M 0 0 L 10 5 L 0 10 z"></path>
			</marker>
		</defs>

		<g class="edges">
			{#each layout.edges as edge (`${edge.from}:${edge.to}`)}
				{@const source = nodeById.get(edge.from)}
				{@const target = nodeById.get(edge.to)}
				{#if source !== undefined && target !== undefined}
					{#if coincides(source, target)}
						<path
							class="directed-edge"
							data-edge-kind="loop"
							data-edge-from={edge.from}
							data-edge-to={edge.to}
							d={loopPath(source)}
							marker-end="url(#topology-arrow)"
						></path>
					{:else}
						{@const endpoint = targetEndpoint(source, target)}
						<line
							data-edge-from={edge.from}
							data-edge-to={edge.to}
							x1={xCoordinate(source)}
							y1={yCoordinate(source)}
							x2={endpoint.x}
							y2={endpoint.y}
							marker-end="url(#topology-arrow)"
						></line>
					{/if}
				{/if}
			{/each}
		</g>

		<g class="nodes">
			{#each layout.nodes as node (node.id)}
				<g
					class:authored={node.position.kind === 'authored'}
					class:schematic={node.position.kind === 'schematic'}
					data-node-id={node.id}
					data-position-kind={node.position.kind}
					transform={transform(node)}
				>
					{#if node.position.kind === 'authored'}
						<circle r={NODE_HALF_SIZE}></circle>
					{:else}
						<rect
							x={-NODE_HALF_SIZE}
							y={-NODE_HALF_SIZE}
							width={NODE_HALF_SIZE * 2}
							height={NODE_HALF_SIZE * 2}
							rx={NODE_HALF_SIZE / 2}
						></rect>
					{/if}
					<text x={LABEL_OFFSET} y="6">{node.label}</text>
				</g>
			{/each}
		</g>
	</svg>

	<section class="topology-details" aria-label="Topology details">
		<details class="world-details">
			<summary>World details</summary>

			<h2>Places</h2>
			<ul>
				{#each layout.nodes as node (node.id)}
					<li>
						{#if onNodeActivate === undefined}
							<span>{node.label} ({node.id})</span>
						{:else}
							<button type="button" onclick={() => onNodeActivate?.(node.id)}>
								<span class="action">Activate</span>
								{node.label}
							</button>
							<span> ({node.id})</span>
						{/if}
						<span class="position"> {positionDescription(node)}</span>
					</li>
				{/each}
			</ul>

			<h2>Directed connections</h2>
			{#if layout.edges.length === 0}
				<p>No directed connections.</p>
			{:else}
				<ul>
					{#each layout.edges as edge (`${edge.from}:${edge.to}`)}
						<li>{nodeLabel(edge.from)} to {nodeLabel(edge.to)}</li>
					{/each}
				</ul>
			{/if}
		</details>
	</section>
</section>

<style>
	.topology {
		display: grid;
		gap: var(--space-4);
		color: var(--color-ink);
	}

	.mode-label {
		font-style: italic;
		font-size: var(--font-size-sm);
		font-weight: 400;
		color: var(--color-ink-muted);
		margin: 0;
	}

	svg {
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
		inline-size: 100%;
		min-block-size: 18rem;
	}

	line,
	.directed-edge {
		stroke: var(--color-ink-faint);
		stroke-width: 1.5;
		vector-effect: non-scaling-stroke;
	}

	.directed-edge {
		fill: none;
	}

	marker path {
		fill: var(--color-ink-faint);
	}

	circle,
	rect {
		fill: var(--color-surface-raised);
		stroke: var(--color-accent);
		stroke-width: 2;
		vector-effect: non-scaling-stroke;
	}

	.schematic rect {
		stroke-dasharray: 3 3;
		stroke-linecap: round;
	}

	text {
		fill: var(--color-ink);
		/* Sized well above the app's base UI font: this SVG is scaled down to
		   fit the ~20rem side panel against a viewBox shaped by (off-limits)
		   node coordinates, so the rendered label ends up meaningfully smaller
		   than the source font-size. Starting from a larger source size is what
		   keeps the scaled-down result legible. */
		font: 600 18px/1.2 var(--font-sans);
		/* Halo the label in the SVG background color so it stays legible where
		   an edge line runs underneath it. */
		paint-order: stroke fill;
		stroke: var(--color-surface);
		stroke-width: 5px;
		stroke-linejoin: round;
	}

	.topology-details {
		border-inline-start: 0.25rem solid var(--color-border);
		padding-inline-start: var(--space-4);
	}

	.world-details summary {
		font-weight: 600;
		font-size: var(--font-size-sm);
		color: var(--color-ink-muted);
		padding-block: var(--space-1);
	}

	.topology-details h2 {
		font-size: var(--font-size-sm);
		font-weight: 650;
		color: var(--color-ink-muted);
		margin-block: var(--space-3) var(--space-1);
	}

	.topology-details ul {
		list-style: none;
		padding-inline-start: 0;
		margin-block: var(--space-1) var(--space-3);
		display: grid;
		gap: var(--space-1);
	}

	.topology-details li {
		font-size: var(--font-size-sm);
	}

	.position {
		display: block;
		font-family: var(--font-mono);
		font-size: var(--font-size-xs);
		color: var(--color-ink-faint);
	}

	.action {
		font-weight: 650;
	}
</style>
