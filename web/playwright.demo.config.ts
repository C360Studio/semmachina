import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: './tests',
	testMatch: '**/bellweather-surface.demo.ts',
	fullyParallel: false,
	workers: 1,
	timeout: 0,
	expect: { timeout: 30_000 },
	preserveOutput: 'never',
	reporter: 'line',
	use: {
		baseURL: 'https://127.0.0.1:4181',
		browserName: 'chromium',
		headless: false,
		ignoreHTTPSErrors: true,
		trace: 'off',
		screenshot: 'off',
		video: 'off'
	}
});
