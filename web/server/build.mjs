import { build } from 'esbuild';

await build({
	entryPoints: ['server/index.ts'],
	outfile: '.server-build/server.js',
	bundle: true,
	platform: 'node',
	format: 'esm',
	target: 'node22',
	sourcemap: true,
	logLevel: 'info'
});
