import { defineConfig } from '@playwright/test';

export default defineConfig({
	webServer: [
		{ command: 'node tests/player-upstream.mjs', url: 'http://127.0.0.1:4180/health' },
		{
			command: 'npm run build && npm run start',
			url: 'http://127.0.0.1:4173',
			env: {
				HOST: '127.0.0.1',
				PORT: '4173',
				ORIGIN: 'https://play.example.test',
				SEMMACHINA_GRAPHQL_URL: 'http://127.0.0.1:8080/graphql',
				SEMMACHINA_GRAPHQL_POSTURE: 'loopback',
				SEMMACHINA_WORLD_ORG: 'c360',
				SEMMACHINA_WORLD_NAMESPACE: 'bellweather',
				SEMMACHINA_WORLD_TEMPLATE: 'bellweather-maze',
				SEMMACHINA_PUBLIC_ORIGIN: 'https://play.example.test',
				SEMMACHINA_TLS_POSTURE: 'trusted_loopback_proxy',
				SEMMACHINA_CREATOR_CREDENTIAL: 'creator-secret-that-is-long',
				SEMMACHINA_PLAYER_BEARER: 'player-bearer-that-is-distinct',
				SEMMACHINA_PLAYER_WS_URL: 'ws://127.0.0.1:4180/play',
				SEMMACHINA_PLAYER_ID: 'c360.semmachina.bellweather.bellweather-maze.player.detective'
			}
		}
	],
	testMatch: '**/*.e2e.{ts,js}'
});
