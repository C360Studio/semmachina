import type { ClockProjection } from '../../server/world-projection';

export type ClockStatusView =
	| ClockProjection
	| Readonly<{
			state: 'error';
			message: string;
	  }>;
