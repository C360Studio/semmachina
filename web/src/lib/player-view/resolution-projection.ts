import type {
	CompanionResolution,
	DeliveredNarration,
	FailureReason,
	Modifier,
	OutcomeBand,
	TurnDelivery,
	TurnRoll,
	VerdictScalars
} from '../player-v1/parser';

export interface ResolutionIdentity {
	readonly turn_id: string;
	readonly action_id: string;
}

export interface NotRequiredRollView {
	readonly kind: 'not_required';
}

export interface RolledRollView {
	readonly kind: 'rolled';
	readonly mechanic: '2d6-pbta/v1';
	readonly dice: readonly [number, number];
	readonly modifiers?: readonly Readonly<Modifier>[];
	readonly modifier_total: number;
	readonly total: number;
}

export type RollView = NotRequiredRollView | RolledRollView;

interface ResolutionViewBase extends ResolutionIdentity {
	readonly player_id: string;
	readonly resolved_at: string;
	readonly companion_resolution?: Readonly<CompanionResolution>;
	readonly narration_ref?: string;
	readonly narration?: Readonly<DeliveredNarration>;
}

export interface CompleteResolutionView extends ResolutionViewBase {
	readonly phase: 'complete';
	readonly narration_ref: string;
	readonly narration: Readonly<DeliveredNarration>;
	readonly verdict: Readonly<VerdictScalars>;
	readonly band: OutcomeBand;
	readonly roll: RollView;
}

export interface FailedResolutionView extends ResolutionViewBase {
	readonly phase: 'failed';
	readonly failure_reason: FailureReason;
	readonly verdict?: Readonly<VerdictScalars>;
	readonly band?: OutcomeBand;
	readonly roll?: RollView;
}

export type ResolutionView = CompleteResolutionView | FailedResolutionView;

export interface ResolutionLedger {
	readonly entries: readonly ResolutionView[];
}

export type ProjectTerminalResult =
	| { readonly status: 'added'; readonly ledger: ResolutionLedger }
	| { readonly status: 'duplicate'; readonly ledger: ResolutionLedger }
	| {
			readonly status: 'conflict';
			readonly ledger: ResolutionLedger;
			readonly error: { readonly code: 'terminal_conflict' };
	  };

export function createResolutionLedger(): ResolutionLedger {
	return Object.freeze({ entries: Object.freeze([]) });
}

export function projectTerminal(
	ledger: ResolutionLedger,
	delivery: TurnDelivery
): ProjectTerminalResult {
	const incoming = freezeResolutionView(toResolutionView(delivery));
	const existing = ledger.entries.find((entry) => sameIdentity(entry, incoming));

	if (existing !== undefined) {
		if (sameCanonicalContent(existing, incoming)) {
			return Object.freeze({ status: 'duplicate', ledger });
		}
		return Object.freeze({
			status: 'conflict',
			ledger,
			error: Object.freeze({ code: 'terminal_conflict' })
		});
	}

	const entries = [...ledger.entries, incoming].sort(compareIdentity);
	return Object.freeze({
		status: 'added',
		ledger: Object.freeze({ entries: Object.freeze(entries) })
	});
}

function toResolutionView(delivery: TurnDelivery): ResolutionView {
	const { result } = delivery;
	const base: ResolutionViewBase = {
		turn_id: result.turn_id,
		action_id: result.action_id,
		player_id: result.player_id,
		resolved_at: result.resolved_at,
		...(result.companion_resolution === undefined
			? {}
			: { companion_resolution: copyCompanion(result.companion_resolution) }),
		...(result.narration_ref === undefined ? {} : { narration_ref: result.narration_ref }),
		...(delivery.narration === undefined ? {} : { narration: copyNarration(delivery.narration) })
	};

	if (result.phase === 'complete') {
		const narration = delivery.narration;
		if (narration === undefined) {
			throw new Error('validated complete delivery is missing narration');
		}
		return {
			...base,
			phase: result.phase,
			narration_ref: result.narration_ref,
			narration: copyNarration(narration),
			verdict: copyVerdict(result.resolution.verdict),
			band: result.resolution.band,
			roll:
				result.resolution.roll === undefined
					? { kind: 'not_required' }
					: copyRoll(result.resolution.roll)
		};
	}

	return {
		...base,
		phase: result.phase,
		failure_reason: result.failure_reason,
		...(result.resolution === undefined
			? {}
			: {
					verdict: copyVerdict(result.resolution.verdict),
					...(result.resolution.band === undefined ? {} : { band: result.resolution.band }),
					...(result.resolution.band === undefined
						? {}
						: {
								roll:
									result.resolution.roll === undefined
										? ({ kind: 'not_required' } as const)
										: copyRoll(result.resolution.roll)
							})
				})
	};
}

function copyVerdict(verdict: VerdictScalars): VerdictScalars {
	return {
		plausibility: verdict.plausibility,
		risk: verdict.risk,
		consequence: verdict.consequence,
		requires_roll: verdict.requires_roll
	};
}

function copyRoll(roll: TurnRoll): RolledRollView {
	return {
		kind: 'rolled',
		mechanic: roll.mechanic,
		dice: [roll.dice[0], roll.dice[1]],
		...(roll.modifiers === undefined
			? {}
			: {
					modifiers: roll.modifiers.map((modifier) => ({
						source: modifier.source,
						value: modifier.value,
						...(modifier.note === undefined ? {} : { note: modifier.note })
					}))
				}),
		modifier_total: roll.modifier_total,
		total: roll.total
	};
}

function copyCompanion(companion: CompanionResolution): CompanionResolution {
	return {
		companion_id: companion.companion_id,
		kind: companion.kind,
		...(companion.hint_level === undefined ? {} : { hint_level: companion.hint_level })
	};
}

function copyNarration(narration: DeliveredNarration): DeliveredNarration {
	return { turn_id: narration.turn_id, band: narration.band, prose: narration.prose };
}

function sameIdentity(left: ResolutionIdentity, right: ResolutionIdentity): boolean {
	return left.turn_id === right.turn_id && left.action_id === right.action_id;
}

function compareIdentity(left: ResolutionIdentity, right: ResolutionIdentity): number {
	const byTurn = compareText(left.turn_id, right.turn_id);
	return byTurn === 0 ? compareText(left.action_id, right.action_id) : byTurn;
}

function compareText(left: string, right: string): number {
	return left < right ? -1 : left > right ? 1 : 0;
}

function sameCanonicalContent(left: ResolutionView, right: ResolutionView): boolean {
	return JSON.stringify(left) === JSON.stringify(right);
}

function freezeResolutionView(view: ResolutionView): ResolutionView {
	return deepFreeze(view);
}

function deepFreeze<T>(value: T): T {
	if (typeof value !== 'object' || value === null || Object.isFrozen(value)) return value;
	for (const nested of Object.values(value)) deepFreeze(nested);
	return Object.freeze(value);
}
