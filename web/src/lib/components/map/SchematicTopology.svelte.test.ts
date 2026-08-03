import { userEvent } from 'vitest/browser';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';

import type { PlacesLayout } from '../../world-view/schematic-layout';
import SchematicTopology from './SchematicTopology.svelte';

const mixedLayout: PlacesLayout = Object.freeze({
	mode: 'mixed',
	nodes: Object.freeze([
		Object.freeze({
			id: 'north-gate',
			label: 'North Gate',
			position: Object.freeze({ kind: 'authored', latitude: 41.9, longitude: -87.6 })
		}),
		Object.freeze({
			id: 'market',
			label: 'Market Square',
			position: Object.freeze({ kind: 'schematic', x: 240, y: 160 })
		})
	]),
	edges: Object.freeze([Object.freeze({ from: 'north-gate', to: 'market' })])
});

describe('SchematicTopology', () => {
	it('provides complete nonvisual parity while preserving directed relations', async () => {
		const screen = await render(SchematicTopology, { layout: mixedLayout });

		await expect
			.element(screen.getByText('Schematic mode — some positions are inferred.'))
			.toBeVisible();
		await expect.element(screen.getByRole('region', { name: 'Topology details' })).toBeVisible();
		await expect.element(screen.getByText('North Gate (north-gate)')).toBeVisible();
		await expect.element(screen.getByText('Market Square (market)')).toBeVisible();
		await expect.element(screen.getByText('North Gate to Market Square')).toBeVisible();
		expect(screen.getByText('Market Square to North Gate').query()).toBeNull();

		const visual = screen.container.querySelector('svg[data-topology-visual]');
		expect(visual).not.toBeNull();
		expect(visual).toHaveAttribute('aria-hidden', 'true');
		expect(visual?.querySelectorAll('[data-node-id]')).toHaveLength(2);
		expect(visual?.querySelector('[data-node-id="north-gate"]')).toHaveAttribute(
			'transform',
			'translate(-87.6 41.9)'
		);
		expect(visual?.querySelector('[data-node-id="market"]')).toHaveAttribute(
			'transform',
			'translate(240 160)'
		);
		const edge = visual?.querySelector('[data-edge-from="north-gate"][data-edge-to="market"]');
		expect(edge).not.toBeNull();
		expect(edge).toHaveAttribute('marker-end', 'url(#topology-arrow)');
		expect(
			visual?.querySelector('[data-edge-from="market"][data-edge-to="north-gate"]')
		).toBeNull();
	});

	it('pads the visual bounds around far-right and deep disconnected topology content', async () => {
		const farRightLabel = 'Far Right Watchtower With A Long Label';
		const largeLayout: PlacesLayout = Object.freeze({
			mode: 'schematic',
			nodes: Object.freeze([
				Object.freeze({
					id: 'origin',
					label: 'Origin',
					position: Object.freeze({ kind: 'schematic', x: 0, y: 0 })
				}),
				Object.freeze({
					id: 'far-right',
					label: farRightLabel,
					position: Object.freeze({ kind: 'schematic', x: 960, y: 0 })
				}),
				Object.freeze({
					id: 'deep',
					label: 'Deep Disconnected Place',
					position: Object.freeze({ kind: 'schematic', x: 0, y: 159_680 })
				})
			]),
			edges: Object.freeze([Object.freeze({ from: 'far-right', to: 'deep' })])
		});
		const screen = await render(SchematicTopology, { layout: largeLayout });
		const visual = screen.container.querySelector('svg[data-topology-visual]');
		const viewBox = visual
			?.getAttribute('viewBox')
			?.split(/\s+/)
			.map((value) => Number(value));

		expect(viewBox).toHaveLength(4);
		const [left, top, width, height] = viewBox as number[];
		const right = left + width;
		const bottom = top + height;
		expect(left).toBeLessThan(-14);
		expect(top).toBeLessThan(-14);
		expect(right).toBeGreaterThan(960 + 20 + farRightLabel.length * 7);
		expect(bottom).toBeGreaterThan(159_680 + 14);

		const edge = visual?.querySelector('[data-edge-from="far-right"][data-edge-to="deep"]');
		for (const attribute of ['x1', 'x2']) {
			const value = Number(edge?.getAttribute(attribute));
			expect(value).toBeGreaterThanOrEqual(left);
			expect(value).toBeLessThanOrEqual(right);
		}
		for (const attribute of ['y1', 'y2']) {
			const value = Number(edge?.getAttribute(attribute));
			expect(value).toBeGreaterThanOrEqual(top);
			expect(value).toBeLessThanOrEqual(bottom);
		}
	});

	it('terminates directed edges at authored circle and schematic rectangle boundaries', async () => {
		const boundaryLayout: PlacesLayout = Object.freeze({
			mode: 'mixed',
			nodes: Object.freeze([
				Object.freeze({
					id: 'circle-source',
					label: 'Circle Source',
					position: Object.freeze({ kind: 'schematic', x: 0, y: 0 })
				}),
				Object.freeze({
					id: 'circle-target',
					label: 'Circle Target',
					position: Object.freeze({ kind: 'authored', latitude: 0, longitude: 100 })
				}),
				Object.freeze({
					id: 'rect-source',
					label: 'Rectangle Source',
					position: Object.freeze({ kind: 'schematic', x: 0, y: 50 })
				}),
				Object.freeze({
					id: 'rect-target',
					label: 'Rectangle Target',
					position: Object.freeze({ kind: 'schematic', x: 100, y: 100 })
				})
			]),
			edges: Object.freeze([
				Object.freeze({ from: 'circle-source', to: 'circle-target' }),
				Object.freeze({ from: 'rect-source', to: 'rect-target' })
			])
		});
		const screen = await render(SchematicTopology, { layout: boundaryLayout });
		const visual = screen.container.querySelector('svg[data-topology-visual]');
		const circleEdge = visual?.querySelector(
			'[data-edge-from="circle-source"][data-edge-to="circle-target"]'
		);
		const rectangleEdge = visual?.querySelector(
			'[data-edge-from="rect-source"][data-edge-to="rect-target"]'
		);

		expect(Number(circleEdge?.getAttribute('x2'))).toBeCloseTo(86);
		expect(Number(circleEdge?.getAttribute('y2'))).toBeCloseTo(0);
		expect(Number(rectangleEdge?.getAttribute('x2'))).toBeCloseTo(86);
		expect(Number(rectangleEdge?.getAttribute('y2'))).toBeCloseTo(93);
	});

	it('renders self and coincident-position edges as visible directed loops outside node bounds', async () => {
		const loopLayout: PlacesLayout = Object.freeze({
			mode: 'mixed',
			nodes: Object.freeze([
				Object.freeze({
					id: 'self',
					label: 'Loop Node',
					position: Object.freeze({ kind: 'schematic', x: 100, y: 100 })
				}),
				Object.freeze({
					id: 'coincident-source',
					label: 'Coincident Source',
					position: Object.freeze({ kind: 'schematic', x: 300, y: 300 })
				}),
				Object.freeze({
					id: 'coincident-target',
					label: 'Coincident Target',
					position: Object.freeze({ kind: 'authored', latitude: 300, longitude: 300 })
				})
			]),
			edges: Object.freeze([
				Object.freeze({ from: 'self', to: 'self' }),
				Object.freeze({ from: 'coincident-source', to: 'coincident-target' })
			])
		});
		const screen = await render(SchematicTopology, { layout: loopLayout });
		const visual = screen.container.querySelector('svg[data-topology-visual]');
		const selfLoop = visual?.querySelector(
			'path[data-edge-from="self"][data-edge-to="self"][data-edge-kind="loop"]'
		);
		const coincidentLoop = visual?.querySelector(
			'path[data-edge-from="coincident-source"][data-edge-to="coincident-target"][data-edge-kind="loop"]'
		);

		expect(selfLoop).toHaveAttribute('d', 'M 114 100 C 148 52 52 52 86 100');
		expect(coincidentLoop).toHaveAttribute('d', 'M 314 300 C 348 252 252 252 286 300');
		expect(selfLoop).toHaveAttribute('marker-end', 'url(#topology-arrow)');
		expect(coincidentLoop).toHaveAttribute('marker-end', 'url(#topology-arrow)');
		expect(visual?.querySelector('line[data-edge-from="self"][data-edge-to="self"]')).toBeNull();
		expect(
			visual?.querySelector(
				'line[data-edge-from="coincident-source"][data-edge-to="coincident-target"]'
			)
		).toBeNull();

		const [left, top, width] = (visual?.getAttribute('viewBox') ?? '')
			.split(/\s+/)
			.map((value) => Number(value));
		expect(left).toBeLessThan(52);
		expect(top).toBeLessThan(52);
		expect(left + width).toBeGreaterThan(348);
		await expect.element(screen.getByText('Loop Node to Loop Node')).toBeVisible();
		await expect.element(screen.getByText('Coincident Source to Coincident Target')).toBeVisible();
	});

	it('does not announce schematic mode for a fully authored layout', async () => {
		const authoredLayout: PlacesLayout = Object.freeze({
			mode: 'authored',
			nodes: Object.freeze([
				Object.freeze({
					id: 'harbor',
					label: 'Harbor',
					position: Object.freeze({ kind: 'authored', latitude: 0, longitude: 0 })
				})
			]),
			edges: Object.freeze([])
		});

		const screen = await render(SchematicTopology, { layout: authoredLayout });

		await expect.element(screen.getByText('Authored topology.')).toBeVisible();
		expect(screen.getByText(/Schematic mode/).query()).toBeNull();
	});

	it('activates nodes in their labelled keyboard focus order', async () => {
		const onNodeActivate = vi.fn();
		const screen = await render(SchematicTopology, { layout: mixedLayout, onNodeActivate });
		const northGate = screen.getByRole('button', { name: 'Activate North Gate' });
		const market = screen.getByRole('button', { name: 'Activate Market Square' });
		const buttons = screen.getByRole('button').elements() as HTMLButtonElement[];

		expect(buttons.map((button) => button.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
			'Activate North Gate',
			'Activate Market Square'
		]);
		expect(buttons.map(({ tabIndex }) => tabIndex)).toEqual([0, 0]);
		buttons[0].focus();
		await expect.element(northGate).toHaveFocus();
		await userEvent.keyboard('{Enter}');
		buttons[1].focus();
		await expect.element(market).toHaveFocus();
		await userEvent.keyboard(' ');

		expect(onNodeActivate).toHaveBeenNthCalledWith(1, 'north-gate');
		expect(onNodeActivate).toHaveBeenNthCalledWith(2, 'market');
	});

	it('keeps noninteractive nodes out of the tab order without hiding their labels', async () => {
		const screen = await render(SchematicTopology, { layout: mixedLayout });

		expect(screen.getByRole('button').elements()).toHaveLength(0);
		await expect.element(screen.getByText('North Gate (north-gate)')).toBeVisible();
		await expect.element(screen.getByText('Market Square (market)')).toBeVisible();
	});
});
