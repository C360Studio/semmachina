import { expect, type Page } from '@playwright/test';

import { emitSurfaceCheckpoint, requireExactHTTPStatus } from './bellweather-surface-contract.mjs';

export const browserOrigin = 'https://127.0.0.1:4181';
export const creatorCredential = 'bellweather-surface-creator-secret';
export const locationLabels = [
	['fete-green-place', 'Bellweather Fete Green Place'],
	['prize-maze-place', 'Bellweather Prize Maze Place'],
	['parish-hall-place', 'Bellweather Parish Hall Place'],
	['bell-tower-place', "St Etheldreda's Bell Tower Place"],
	['river-cottage-place', 'River Cottage Place']
] as const;

async function journeyStep(label: string, assertion: () => Promise<void>): Promise<void> {
	try {
		await assertion();
	} catch (error) {
		throw new Error(`action-free surface journey failed at ${label}`, { cause: error });
	}
}

export async function completeActionFreeJourney(page: Page, worldPrefix: string): Promise<void> {
	await journeyStep('unauthorized world status', async () => {
		const response = await page.goto(`${browserOrigin}/api/world`);
		requireExactHTTPStatus(response, 401, 'unauthorized world');
	});
	emitSurfaceCheckpoint('unauthorized_world_verified');

	await journeyStep('surface document', async () => {
		const response = await page.goto(browserOrigin);
		requireExactHTTPStatus(response, 200, 'surface document');
	});
	emitSurfaceCheckpoint('surface_document_loaded');

	await journeyStep('creator login', async () => {
		const preauth = page.waitForResponse((response) =>
			response.url().endsWith('/api/auth/preauth')
		);
		const login = page.waitForResponse((response) => response.url().endsWith('/api/auth/login'));
		const world = page.waitForResponse((response) => response.url().endsWith('/api/world'));
		const credentialInput = page.getByLabel('Creator credential');
		await credentialInput.fill(creatorCredential);
		await page.getByRole('button', { name: 'Enter world' }).click();
		emitSurfaceCheckpoint('login_submitted');
		await expect
			.poll(
				async () =>
					(await credentialInput.count()) === 0 || (await credentialInput.inputValue()) === '',
				{ message: 'creator credential input was not cleared after submission' }
			)
			.toBe(true);
		for (const [label, response] of [
			['preauth', await preauth],
			['login', await login],
			['world', await world]
		] as const) {
			if (response.status() !== 200) {
				const projectionUnavailable = await page
					.getByText('World projection unavailable. Session controls are disabled.')
					.isVisible({ timeout: 1_000 })
					.catch(() => false);
				throw new Error(
					`${label} returned HTTP ${response.status()}; projection_unavailable=${projectionUnavailable}`
				);
			}
		}
		emitSurfaceCheckpoint('login_http_verified');
		await expect(page.getByRole('textbox', { name: 'What do you do?' })).toBeVisible();
	});
	emitSurfaceCheckpoint('action_controls_visible');

	await journeyStep('schematic world', async () => {
		await expect(page.getByText('Schematic mode — some positions are inferred.')).toBeVisible();
		for (const [localID, label] of locationLabels) {
			const node = page.locator(`[data-node-id="${worldPrefix}.location.${localID}"]`);
			await expect(node).toHaveAttribute('data-position-kind', 'schematic');
			await expect(node.locator('text')).toHaveText(label);
		}
	});
	emitSurfaceCheckpoint('schematic_world_visible');

	await journeyStep('unconfigured clock', async () => {
		await expect(page.getByText('Clock not configured.', { exact: true })).toBeVisible();
	});
	emitSurfaceCheckpoint('clock_visible');
	emitSurfaceCheckpoint('pre_action_ready');
}
