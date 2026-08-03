import { defineConfig } from '@playwright/test';

export default defineConfig({
	webServer: {
		command: 'npm run build && npm run start',
		url: 'http://127.0.0.1:4173',
		env: {
			HOST: '127.0.0.1',
			PORT: '4173',
			SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:8080/graphql',
			SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
			SEMMACHINA_WORLD_ORG: 'c360',
			SEMMACHINA_WORLD_NAMESPACE: 'bellweather',
			SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze'
		}
	},
	testMatch: '**/*.e2e.{ts,js}'
});
