import { startCustomServer } from '../src/lib/server/custom-bootstrap';

try {
	await startCustomServer();
} catch {
	console.error('SemMachina web startup failed before listen');
	process.exitCode = 1;
}
