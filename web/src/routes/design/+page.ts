import { dev } from '$app/environment';
import { error } from '@sveltejs/kit';

export const ssr = false;

export const load = (): void => {
	if (!dev) error(404, 'Not found');
};
