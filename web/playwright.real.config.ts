import { defineConfig } from '@playwright/test';

import { REAL_BROWSER_TEST_MATCH } from './tests/bellweather-surface-contract.mjs';

const browserOrigin = 'https://127.0.0.1:4181';
const preflight = process.env.REAL_SURFACE_PREFLIGHT === '1';

export default defineConfig({
	testDir: './tests',
	testMatch: preflight ? '**/*.preflight.ts' : REAL_BROWSER_TEST_MATCH,
	fullyParallel: false,
	workers: 1,
	timeout: preflight ? 30_000 : 390_000,
	expect: { timeout: preflight ? 15_000 : 180_000 },
	use: {
		baseURL: browserOrigin,
		ignoreHTTPSErrors: true
	}
});
