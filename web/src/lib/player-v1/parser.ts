const PLAYER_PROTOCOL = 'player/v1' as const;
const MAX_ENTITY_ID_BYTES = 256;
const MAX_ID_SEGMENT_BYTES = 128;
const MAX_ACTION_ID_BYTES = 123;
export const MAX_ACTION_TEXT_BYTES = 4 * 1024;
const MAX_IDEMPOTENCY_KEY_BYTES = 128;
const MAX_PROSE_BYTES = 16 * 1024;
const MAX_MODIFIER_NOTE_BYTES = 256;
const utf8 = new TextEncoder();

export type ActionTextViolation = 'blank' | 'too_long';

export function actionTextViolation(text: string): ActionTextViolation | undefined {
	if (text.trim() === '') return 'blank';
	if (byteLength(text) > MAX_ACTION_TEXT_BYTES) return 'too_long';
	return undefined;
}

export type ProtocolFailureKind =
	| 'invalid_json'
	| 'invalid_document'
	| 'unsupported_protocol'
	| 'unsupported_discriminator'
	| 'invalid_field';

export interface ProtocolParseFailure {
	kind: ProtocolFailureKind;
	path: string;
	message: string;
}

export type ProtocolParseResult<T> =
	{ ok: true; value: T } | { ok: false; error: ProtocolParseFailure };

export type SubmitRefusalCode =
	| 'unauthenticated'
	| 'unsupported_protocol'
	| 'server_owned_field'
	| 'unknown_field'
	| 'malformed_request'
	| 'invalid_field'
	| 'turn_in_progress'
	| 'unavailable';

export interface SubmitRefusal {
	code: SubmitRefusalCode;
	message: string;
	field?: string;
	active_turn_id?: string;
}

export type SubmitResponse =
	| {
			protocol: typeof PLAYER_PROTOCOL;
			status: 'accepted';
			idempotency_key: string;
			action_id: string;
			turn_id: string;
			arrived_at: string;
	  }
	| {
			protocol: typeof PLAYER_PROTOCOL;
			status: 'refused';
			idempotency_key?: string;
			refusal: SubmitRefusal;
	  };

export type Plausibility = 'impossible' | 'unlikely' | 'plausible' | 'certain';
export type Risk = 'none' | 'low' | 'moderate' | 'high';
export type Consequence = 'none' | 'setback' | 'harm' | 'cost' | 'complication' | 'escalation';
export type OutcomeBand = 'auto' | 'miss' | 'partial' | 'full';
export type ModifierSource = 'trait' | 'equipment' | 'position' | 'assistance' | 'condition';

export interface VerdictScalars {
	plausibility: Plausibility;
	risk: Risk;
	consequence: Consequence;
	requires_roll: boolean;
}

export interface Modifier {
	source: ModifierSource;
	value: number;
	note?: string;
}

export interface TurnRoll {
	mechanic: '2d6-pbta/v1';
	dice: [number, number];
	modifiers?: Modifier[];
	modifier_total: number;
	total: number;
}

export interface UnresolvedTurnResolution {
	verdict: VerdictScalars;
	band?: never;
	roll?: never;
}

export interface AutomaticTurnResolution {
	verdict: VerdictScalars & { requires_roll: false };
	band: 'auto';
	roll?: never;
}

export type RolledOutcomeBand = Exclude<OutcomeBand, 'auto'>;

export interface RolledTurnResolution {
	verdict: VerdictScalars & { requires_roll: true };
	band: RolledOutcomeBand;
	roll: TurnRoll;
}

export type CompletedTurnResolution = AutomaticTurnResolution | RolledTurnResolution;
export type TurnResolution = UnresolvedTurnResolution | CompletedTurnResolution;

export type CompanionDecisionKind = 'silent' | 'quip' | 'question' | 'warning' | 'recall' | 'hint';
export type HintLevel = 'nudge' | 'connect' | 'next-step';

export interface CompanionResolution {
	companion_id: string;
	kind: CompanionDecisionKind;
	hint_level?: HintLevel;
}

export type FailureReason =
	| 'effect-invalid'
	| 'effect-entity-missing'
	| 'effect-entity-kind'
	| 'effect-commit-incomplete'
	| 'persona-cap-exhausted'
	| 'persona-loop-failed'
	| 'turn-stranded'
	| 'knowledge-unauthorized'
	| 'accusation-invalid'
	| 'case-progress-invalid';

interface TurnResultBase {
	protocol: typeof PLAYER_PROTOCOL;
	turn_id: string;
	action_id: string;
	player_id: string;
	companion_resolution?: CompanionResolution;
	narration_ref?: string;
	resolved_at: string;
}

export type CompleteTurnResult = TurnResultBase & {
	phase: 'complete';
	resolution: CompletedTurnResolution;
	narration_ref: string;
};

export type FailedTurnResult = TurnResultBase & {
	phase: 'failed';
	failure_reason: FailureReason;
	resolution?: TurnResolution;
};

export type TurnResult = CompleteTurnResult | FailedTurnResult;

export interface DeliveredNarration {
	turn_id: string;
	band: OutcomeBand;
	prose: string;
}

type CompletedResolutionFor<Band extends OutcomeBand> = Band extends 'auto'
	? AutomaticTurnResolution
	: RolledTurnResolution & { band: Band };

type CompleteTurnDeliveryFor<Band extends OutcomeBand> = {
	protocol: typeof PLAYER_PROTOCOL;
	result: Omit<CompleteTurnResult, 'resolution'> & {
		resolution: CompletedResolutionFor<Band>;
	};
	narration: DeliveredNarration & { band: Band };
};

export type CompleteTurnDelivery = {
	[Band in OutcomeBand]: CompleteTurnDeliveryFor<Band>;
}[OutcomeBand];

export interface FailedTurnDelivery {
	protocol: typeof PLAYER_PROTOCOL;
	result: FailedTurnResult;
	narration?: DeliveredNarration;
}

export type TurnDelivery = CompleteTurnDelivery | FailedTurnDelivery;

export type RetrieveBy = 'turn' | 'action' | 'latest';
export type RetrieveRefusalCode = 'malformed_request' | 'not_found' | 'not_ready' | 'unavailable';

export type RetrieveResponse =
	| {
			protocol: typeof PLAYER_PROTOCOL;
			status: 'found';
			by: RetrieveBy;
			id?: string;
			delivery: TurnDelivery;
	  }
	| {
			protocol: typeof PLAYER_PROTOCOL;
			status: 'refused';
			by: RetrieveBy;
			id?: string;
			refusal: { code: RetrieveRefusalCode; message: string };
	  };

export type OperationResponse = {
	protocol: typeof PLAYER_PROTOCOL;
	status: 'refused';
	refusal:
		| { code: 'malformed_operation'; message: string }
		| { code: 'unsupported_operation'; operation: string; message: string };
};

export type PlayerFrame =
	| { protocol: typeof PLAYER_PROTOCOL; type: 'submit_response'; response: SubmitResponse }
	| { protocol: typeof PLAYER_PROTOCOL; type: 'turn_delivery'; delivery: TurnDelivery }
	| { protocol: typeof PLAYER_PROTOCOL; type: 'retrieve_response'; retrieval: RetrieveResponse }
	| { protocol: typeof PLAYER_PROTOCOL; type: 'operation_response'; operation: OperationResponse };

export interface SubmitAction {
	protocol: typeof PLAYER_PROTOCOL;
	text: string;
	idempotency_key: string;
}

export type RetrieveRequest =
	| { protocol: typeof PLAYER_PROTOCOL; type: 'retrieve_result'; by: 'latest' }
	| {
			protocol: typeof PLAYER_PROTOCOL;
			type: 'retrieve_result';
			by: 'turn' | 'action';
			id: string;
	  };

class ValidationFailure extends Error {
	constructor(
		readonly kind: ProtocolFailureKind,
		readonly path: string,
		message: string
	) {
		super(message);
	}
}

function failure<T>(error: ValidationFailure): ProtocolParseResult<T> {
	return {
		ok: false,
		error: { kind: error.kind, path: error.path, message: error.message }
	};
}

function invalid(path: string, message: string): never {
	throw new ValidationFailure('invalid_field', path, message);
}

function asRecord(value: unknown, path: string): Record<string, unknown> {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) {
		throw new ValidationFailure('invalid_document', path, 'must be a JSON object');
	}
	return value as Record<string, unknown>;
}

function stringField(record: Record<string, unknown>, key: string, path: string): string {
	const value = record[key];
	if (typeof value !== 'string') invalid(`${path}.${key}`, 'must be a string');
	return value;
}

function hasOwn(record: Record<string, unknown>, key: string): boolean {
	return Object.prototype.hasOwnProperty.call(record, key);
}

function optionalString(
	record: Record<string, unknown>,
	key: string,
	path: string
): string | undefined {
	if (!hasOwn(record, key)) return undefined;
	const value = record[key];
	if (typeof value !== 'string') invalid(`${path}.${key}`, 'must be a string when present');
	if (value === '') invalid(`${path}.${key}`, 'must be nonempty when present');
	return value;
}

function booleanField(record: Record<string, unknown>, key: string, path: string): boolean {
	const value = record[key];
	if (typeof value !== 'boolean') invalid(`${path}.${key}`, 'must be a boolean');
	return value;
}

function integerField(record: Record<string, unknown>, key: string, path: string): number {
	const value = record[key];
	if (typeof value !== 'number' || !Number.isSafeInteger(value))
		invalid(`${path}.${key}`, 'must be a safe integer');
	return value;
}

function closed<T extends string>(value: string, allowed: readonly T[], path: string): T {
	if (!allowed.includes(value as T)) invalid(path, `must be one of ${allowed.join(', ')}`);
	return value as T;
}

function requireProtocol(record: Record<string, unknown>, path: string): typeof PLAYER_PROTOCOL {
	const protocol = stringField(record, 'protocol', path);
	if (protocol !== PLAYER_PROTOCOL) {
		throw new ValidationFailure(
			'unsupported_protocol',
			`${path}.protocol`,
			`must be ${PLAYER_PROTOCOL}`
		);
	}
	return PLAYER_PROTOCOL;
}

function byteLength(value: string): number {
	return utf8.encode(value).byteLength;
}

function requireNonempty(value: string, path: string): string {
	if (value === '') invalid(path, 'must not be empty');
	return value;
}

function requireTimestamp(value: string, path: string): string {
	const match =
		/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(Z|([+-])(\d{2}):(\d{2}))$/.exec(
			value
		);
	if (match === null) invalid(path, 'must be a nonzero RFC3339 timestamp');

	const year = Number(match[1]);
	const month = Number(match[2]);
	const day = Number(match[3]);
	const hour = Number(match[4]);
	const minute = Number(match[5]);
	const second = Number(match[6]);
	const fraction = match[7] ?? '';
	const zoneHour = match[8] === 'Z' ? 0 : Number(match[10]);
	const zoneMinute = match[8] === 'Z' ? 0 : Number(match[11]);

	if (
		month < 1 ||
		month > 12 ||
		day < 1 ||
		day > daysInMonth(year, month) ||
		hour > 23 ||
		minute > 59 ||
		second > 59 ||
		zoneHour > 23 ||
		zoneMinute > 59
	) {
		invalid(path, 'must be a nonzero RFC3339 timestamp');
	}

	const zoneDirection = match[9] === '-' ? -1 : 1;
	const zoneOffsetSeconds = zoneDirection * (zoneHour * 60 + zoneMinute) * 60;
	const utcSecond =
		daysFromCivil(year, month, day) * 86_400 +
		hour * 3_600 +
		minute * 60 +
		second -
		zoneOffsetSeconds;
	const zeroSecond = daysFromCivil(1, 1, 1) * 86_400;
	const nanoseconds = Number((fraction.slice(0, 9) + '000000000').slice(0, 9));
	if (utcSecond === zeroSecond && nanoseconds === 0) {
		invalid(path, 'must not be Go zero time');
	}
	return value;
}

function daysInMonth(year: number, month: number): number {
	if (month === 2) return isLeapYear(year) ? 29 : 28;
	return [4, 6, 9, 11].includes(month) ? 30 : 31;
}

function isLeapYear(year: number): boolean {
	return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
}

// Proleptic Gregorian civil date to whole days. Go's time package uses the
// same calendar, including year zero, while JavaScript Date does not parse it
// consistently and rewrites years 0..99 in several constructors.
function daysFromCivil(year: number, month: number, day: number): number {
	const adjustedYear = year - (month <= 2 ? 1 : 0);
	const era = Math.floor(adjustedYear / 400);
	const yearOfEra = adjustedYear - era * 400;
	const shiftedMonth = month + (month > 2 ? -3 : 9);
	const dayOfYear = Math.floor((153 * shiftedMonth + 2) / 5) + day - 1;
	const dayOfEra =
		yearOfEra * 365 + Math.floor(yearOfEra / 4) - Math.floor(yearOfEra / 100) + dayOfYear;
	return era * 146_097 + dayOfEra - 719_468;
}

function requireIDSegment(value: string, path: string, maxBytes = MAX_ID_SEGMENT_BYTES): string {
	if (byteLength(value) > maxBytes || !/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(value)) {
		invalid(path, `must be one canonical entity-ID segment of at most ${maxBytes} bytes`);
	}
	return value;
}

function requireActionID(value: string, path: string): string {
	return requireIDSegment(value, path, MAX_ACTION_ID_BYTES);
}

function requireTurnID(value: string, path: string): string {
	requireIDSegment(value, path);
	if (!value.startsWith('turn-') || value === 'turn-')
		invalid(path, 'must derive from an action ID');
	requireActionID(value.slice('turn-'.length), path);
	return value;
}

function requireEntityID(value: string, path: string): string {
	if (byteLength(value) > MAX_ENTITY_ID_BYTES)
		invalid(path, 'exceeds the 256-byte entity-ID budget');
	const parts = value.split('.');
	if (parts.length !== 6 || parts.some((part) => !/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(part))) {
		invalid(path, 'must be a canonical six-part entity ID');
	}
	return value;
}

function requireStorageRef(value: string, path: string): string {
	if (!value.startsWith('obj://')) invalid(path, 'must use the obj:// storage-reference scheme');
	const rest = value.slice('obj://'.length);
	const separator = rest.indexOf('/');
	if (separator <= 0 || separator === rest.length - 1)
		invalid(path, 'must name a storage instance and key');
	return value;
}

function requireIdempotencyKey(value: string, path: string): string {
	const bytes = byteLength(value);
	if (bytes === 0 || bytes > MAX_IDEMPOTENCY_KEY_BYTES) {
		invalid(path, `must be 1 to ${MAX_IDEMPOTENCY_KEY_BYTES} bytes`);
	}
	for (const character of value) {
		const code = character.codePointAt(0) ?? 0;
		if (code < 0x20 || code === 0x7f) invalid(path, 'must not contain control characters');
	}
	return value;
}

function forbidPresent(
	record: Record<string, unknown>,
	keys: readonly string[],
	path: string
): void {
	for (const key of keys) {
		if (hasOwn(record, key)) invalid(`${path}.${key}`, 'must be absent');
	}
}

function parseSubmitRefusal(value: unknown, path: string): SubmitRefusal {
	const record = asRecord(value, path);
	const code = closed(stringField(record, 'code', path), SUBMIT_REFUSAL_CODES, `${path}.code`);
	const message = requireNonempty(stringField(record, 'message', path), `${path}.message`);
	const field = optionalString(record, 'field', path);
	const activeTurnID = optionalString(record, 'active_turn_id', path);
	const activeTurnPresent = hasOwn(record, 'active_turn_id');
	if (code === 'turn_in_progress') {
		if (activeTurnID === undefined)
			invalid(`${path}.active_turn_id`, 'is required for turn_in_progress');
		requireIDSegment(activeTurnID, `${path}.active_turn_id`);
	} else if (activeTurnPresent) {
		invalid(`${path}.active_turn_id`, 'is allowed only for turn_in_progress');
	}
	return {
		code,
		message,
		...(field === undefined ? {} : { field }),
		...(activeTurnID === undefined ? {} : { active_turn_id: activeTurnID })
	};
}

function parseSubmitResponse(value: unknown, path: string): SubmitResponse {
	const record = asRecord(value, path);
	requireProtocol(record, path);
	const status = closed(
		stringField(record, 'status', path),
		['accepted', 'refused'] as const,
		`${path}.status`
	);
	if (status === 'accepted') {
		if (hasOwn(record, 'refusal')) invalid(`${path}.refusal`, 'must be absent');
		const idempotencyKey = requireIdempotencyKey(
			stringField(record, 'idempotency_key', path),
			`${path}.idempotency_key`
		);
		const actionID = requireActionID(stringField(record, 'action_id', path), `${path}.action_id`);
		const turnID = requireTurnID(stringField(record, 'turn_id', path), `${path}.turn_id`);
		if (turnID !== `turn-${actionID}`) invalid(`${path}.turn_id`, 'does not derive from action_id');
		const arrivedAt = requireTimestamp(
			stringField(record, 'arrived_at', path),
			`${path}.arrived_at`
		);
		return {
			protocol: PLAYER_PROTOCOL,
			status,
			idempotency_key: idempotencyKey,
			action_id: actionID,
			turn_id: turnID,
			arrived_at: arrivedAt
		};
	}

	forbidPresent(record, ['action_id', 'turn_id', 'arrived_at'], path);
	if (record.refusal === undefined || record.refusal === null)
		invalid(`${path}.refusal`, 'is required');
	const idempotencyKey = optionalString(record, 'idempotency_key', path);
	if (idempotencyKey !== undefined)
		requireIdempotencyKey(idempotencyKey, `${path}.idempotency_key`);
	return {
		protocol: PLAYER_PROTOCOL,
		status,
		...(idempotencyKey === undefined ? {} : { idempotency_key: idempotencyKey }),
		refusal: parseSubmitRefusal(record.refusal, `${path}.refusal`)
	};
}

const SUBMIT_REFUSAL_CODES = [
	'unauthenticated',
	'unsupported_protocol',
	'server_owned_field',
	'unknown_field',
	'malformed_request',
	'invalid_field',
	'turn_in_progress',
	'unavailable'
] as const;
const PLAUSIBILITIES = ['impossible', 'unlikely', 'plausible', 'certain'] as const;
const RISKS = ['none', 'low', 'moderate', 'high'] as const;
const CONSEQUENCES = ['none', 'setback', 'harm', 'cost', 'complication', 'escalation'] as const;
const OUTCOME_BANDS = ['auto', 'miss', 'partial', 'full'] as const;
const MODIFIER_SOURCES = ['trait', 'equipment', 'position', 'assistance', 'condition'] as const;
const FAILURE_REASONS = [
	'effect-invalid',
	'effect-entity-missing',
	'effect-entity-kind',
	'effect-commit-incomplete',
	'persona-cap-exhausted',
	'persona-loop-failed',
	'turn-stranded',
	'knowledge-unauthorized',
	'accusation-invalid',
	'case-progress-invalid'
] as const;
const COMPANION_KINDS = ['silent', 'quip', 'question', 'warning', 'recall', 'hint'] as const;
const HINT_LEVELS = ['nudge', 'connect', 'next-step'] as const;

function parseVerdict(value: unknown, path: string): VerdictScalars {
	const record = asRecord(value, path);
	return {
		plausibility: closed(
			stringField(record, 'plausibility', path),
			PLAUSIBILITIES,
			`${path}.plausibility`
		),
		risk: closed(stringField(record, 'risk', path), RISKS, `${path}.risk`),
		consequence: closed(
			stringField(record, 'consequence', path),
			CONSEQUENCES,
			`${path}.consequence`
		),
		requires_roll: booleanField(record, 'requires_roll', path)
	};
}

function parseModifier(value: unknown, path: string): Modifier {
	const record = asRecord(value, path);
	const source = closed(stringField(record, 'source', path), MODIFIER_SOURCES, `${path}.source`);
	const modifierValue = integerField(record, 'value', path);
	if (modifierValue < -3 || modifierValue > 3) invalid(`${path}.value`, 'must be between -3 and 3');
	const note = optionalString(record, 'note', path);
	if (note !== undefined && byteLength(note) > MAX_MODIFIER_NOTE_BYTES) {
		invalid(`${path}.note`, `exceeds ${MAX_MODIFIER_NOTE_BYTES} bytes`);
	}
	return { source, value: modifierValue, ...(note === undefined ? {} : { note }) };
}

function parseRoll(value: unknown, path: string): TurnRoll {
	const record = asRecord(value, path);
	const mechanic = closed(
		stringField(record, 'mechanic', path),
		['2d6-pbta/v1'] as const,
		`${path}.mechanic`
	);
	if (!Array.isArray(record.dice) || record.dice.length !== 2)
		invalid(`${path}.dice`, 'must contain exactly two dice');
	const dice = record.dice.map((die, index) => {
		if (typeof die !== 'number' || !Number.isInteger(die) || die < 1 || die > 6) {
			invalid(`${path}.dice[${index}]`, 'must be an integer from 1 to 6');
		}
		return die;
	}) as [number, number];
	let modifiers: Modifier[] | undefined;
	if (hasOwn(record, 'modifiers')) {
		if (!Array.isArray(record.modifiers)) invalid(`${path}.modifiers`, 'must be an array');
		if (record.modifiers.length > 4)
			invalid(`${path}.modifiers`, 'must contain at most four modifiers');
		modifiers = record.modifiers.map((modifier, index) =>
			parseModifier(modifier, `${path}.modifiers[${index}]`)
		);
	}
	const modifierTotal = integerField(record, 'modifier_total', path);
	const total = integerField(record, 'total', path);
	return {
		mechanic,
		dice,
		...(modifiers === undefined ? {} : { modifiers }),
		modifier_total: modifierTotal,
		total
	};
}

function parseResolution(
	value: unknown,
	path: string,
	outcomeRequired: true
): CompletedTurnResolution;
function parseResolution(value: unknown, path: string, outcomeRequired: false): TurnResolution;
function parseResolution(value: unknown, path: string, outcomeRequired: boolean): TurnResolution {
	const record = asRecord(value, path);
	const verdict = parseVerdict(record.verdict, `${path}.verdict`);
	const rawBand = optionalString(record, 'band', path);
	const band = rawBand === undefined ? undefined : closed(rawBand, OUTCOME_BANDS, `${path}.band`);
	const roll = hasOwn(record, 'roll') ? parseRoll(record.roll, `${path}.roll`) : undefined;
	if (band === undefined) {
		if (outcomeRequired) invalid(`${path}.band`, 'is required for a completed turn');
		if (roll !== undefined) invalid(`${path}.roll`, 'is forbidden without a band');
		return { verdict };
	}
	if (roll === undefined) {
		if (band !== 'auto') invalid(`${path}.band`, 'only auto resolves without a roll');
		if (verdict.requires_roll)
			invalid(`${path}.verdict.requires_roll`, 'must be false for an automatic resolution');
		return { verdict: { ...verdict, requires_roll: false }, band };
	}
	if (!verdict.requires_roll)
		invalid(`${path}.verdict.requires_roll`, 'must be true when a roll is present');
	if (band === 'auto') invalid(`${path}.band`, 'a roll cannot select auto');
	return { verdict: { ...verdict, requires_roll: true }, band, roll };
}

function parseCompanionResolution(value: unknown, path: string): CompanionResolution {
	const record = asRecord(value, path);
	const companionID = requireEntityID(
		stringField(record, 'companion_id', path),
		`${path}.companion_id`
	);
	const kind = closed(stringField(record, 'kind', path), COMPANION_KINDS, `${path}.kind`);
	const rawHintLevel = optionalString(record, 'hint_level', path);
	if (kind === 'hint') {
		if (rawHintLevel === undefined) invalid(`${path}.hint_level`, 'is required for a hint');
		return {
			companion_id: companionID,
			kind,
			hint_level: closed(rawHintLevel, HINT_LEVELS, `${path}.hint_level`)
		};
	}
	if (hasOwn(record, 'hint_level'))
		invalid(`${path}.hint_level`, 'is forbidden for a non-hint decision');
	return { companion_id: companionID, kind };
}

function parseTurnResult(value: unknown, path: string): TurnResult {
	const record = asRecord(value, path);
	requireProtocol(record, path);
	const turnID = requireTurnID(stringField(record, 'turn_id', path), `${path}.turn_id`);
	const actionID = requireActionID(stringField(record, 'action_id', path), `${path}.action_id`);
	if (turnID !== `turn-${actionID}`) invalid(`${path}.action_id`, 'does not pair with turn_id');
	const playerID = requireEntityID(stringField(record, 'player_id', path), `${path}.player_id`);
	const phase = closed(
		stringField(record, 'phase', path),
		['complete', 'failed'] as const,
		`${path}.phase`
	);
	let completeResolution: CompletedTurnResolution | undefined;
	let failedResolution: TurnResolution | undefined;
	if (!hasOwn(record, 'resolution')) {
		if (phase === 'complete') invalid(`${path}.resolution`, 'is required for a complete turn');
	} else if (phase === 'complete') {
		completeResolution = parseResolution(record.resolution, `${path}.resolution`, true);
	} else {
		failedResolution = parseResolution(record.resolution, `${path}.resolution`, false);
	}
	const companionResolution = hasOwn(record, 'companion_resolution')
		? parseCompanionResolution(record.companion_resolution, `${path}.companion_resolution`)
		: undefined;
	const narrationRef = optionalString(record, 'narration_ref', path);
	if (narrationRef !== undefined && narrationRef !== '')
		requireStorageRef(narrationRef, `${path}.narration_ref`);
	const resolvedAt = requireTimestamp(
		stringField(record, 'resolved_at', path),
		`${path}.resolved_at`
	);
	const base = {
		protocol: PLAYER_PROTOCOL,
		turn_id: turnID,
		action_id: actionID,
		player_id: playerID,
		...(companionResolution === undefined ? {} : { companion_resolution: companionResolution }),
		...(narrationRef === undefined || narrationRef === '' ? {} : { narration_ref: narrationRef }),
		resolved_at: resolvedAt
	};
	if (phase === 'complete') {
		if (hasOwn(record, 'failure_reason')) {
			invalid(`${path}.failure_reason`, 'is forbidden for a complete turn');
		}
		if (narrationRef === undefined || narrationRef === '')
			invalid(`${path}.narration_ref`, 'is required for a complete turn');
		if (completeResolution === undefined)
			invalid(`${path}.resolution`, 'is required for a complete turn');
		return { ...base, phase, resolution: completeResolution, narration_ref: narrationRef };
	}
	const failureReason = closed(
		stringField(record, 'failure_reason', path),
		FAILURE_REASONS,
		`${path}.failure_reason`
	);
	return {
		...base,
		phase,
		failure_reason: failureReason,
		...(failedResolution === undefined ? {} : { resolution: failedResolution })
	};
}

function parseNarration(value: unknown, path: string): DeliveredNarration {
	const record = asRecord(value, path);
	const turnID = requireIDSegment(stringField(record, 'turn_id', path), `${path}.turn_id`);
	const band = closed(stringField(record, 'band', path), OUTCOME_BANDS, `${path}.band`);
	const prose = requireNonempty(stringField(record, 'prose', path), `${path}.prose`);
	if (byteLength(prose) > MAX_PROSE_BYTES)
		invalid(`${path}.prose`, `exceeds ${MAX_PROSE_BYTES} bytes`);
	return { turn_id: turnID, band, prose };
}

function pairCompletedDelivery(
	result: CompleteTurnResult,
	narration: DeliveredNarration,
	path: string
): CompleteTurnDelivery {
	switch (result.resolution.band) {
		case 'auto':
			if (narration.band !== 'auto')
				invalid(`${path}.narration.band`, 'does not match result.resolution.band');
			return {
				protocol: PLAYER_PROTOCOL,
				result: { ...result, resolution: result.resolution },
				narration: { ...narration, band: narration.band }
			};
		case 'miss':
			if (narration.band !== 'miss')
				invalid(`${path}.narration.band`, 'does not match result.resolution.band');
			return {
				protocol: PLAYER_PROTOCOL,
				result: { ...result, resolution: { ...result.resolution, band: 'miss' } },
				narration: { ...narration, band: narration.band }
			};
		case 'partial':
			if (narration.band !== 'partial')
				invalid(`${path}.narration.band`, 'does not match result.resolution.band');
			return {
				protocol: PLAYER_PROTOCOL,
				result: { ...result, resolution: { ...result.resolution, band: 'partial' } },
				narration: { ...narration, band: narration.band }
			};
		case 'full':
			if (narration.band !== 'full')
				invalid(`${path}.narration.band`, 'does not match result.resolution.band');
			return {
				protocol: PLAYER_PROTOCOL,
				result: { ...result, resolution: { ...result.resolution, band: 'full' } },
				narration: { ...narration, band: narration.band }
			};
	}
}

function parseDelivery(value: unknown, path: string): TurnDelivery {
	const record = asRecord(value, path);
	requireProtocol(record, path);
	if (record.result === undefined || record.result === null)
		invalid(`${path}.result`, 'is required');
	const result = parseTurnResult(record.result, `${path}.result`);
	const narration = hasOwn(record, 'narration')
		? parseNarration(record.narration, `${path}.narration`)
		: undefined;
	if (result.narration_ref !== undefined && narration === undefined)
		invalid(`${path}.narration`, 'is required by narration_ref');
	if (result.narration_ref === undefined && narration !== undefined)
		invalid(`${path}.narration`, 'is forbidden without narration_ref');
	if (narration !== undefined) {
		if (narration.turn_id !== result.turn_id)
			invalid(`${path}.narration.turn_id`, 'does not match result.turn_id');
		if (result.resolution?.band !== undefined && narration.band !== result.resolution.band) {
			invalid(`${path}.narration.band`, 'does not match result.resolution.band');
		}
	}
	if (result.phase === 'complete') {
		if (narration === undefined) invalid(`${path}.narration`, 'is required by narration_ref');
		return pairCompletedDelivery(result, narration, path);
	}
	return { protocol: PLAYER_PROTOCOL, result, ...(narration === undefined ? {} : { narration }) };
}

function parseRetrieveResponse(value: unknown, path: string): RetrieveResponse {
	const record = asRecord(value, path);
	requireProtocol(record, path);
	const status = closed(
		stringField(record, 'status', path),
		['found', 'refused'] as const,
		`${path}.status`
	);
	const by = closed(
		stringField(record, 'by', path),
		['turn', 'action', 'latest'] as const,
		`${path}.by`
	);
	const id = optionalString(record, 'id', path);
	validateRetrieveIdentity(by, id, hasOwn(record, 'id'), `${path}.id`);
	if (status === 'found') {
		if (record.delivery === undefined || record.delivery === null)
			invalid(`${path}.delivery`, 'is required');
		if (hasOwn(record, 'refusal')) invalid(`${path}.refusal`, 'must be absent');
		const delivery = parseDelivery(record.delivery, `${path}.delivery`);
		if (by === 'turn' && delivery.result.turn_id !== id)
			invalid(`${path}.delivery.result.turn_id`, 'does not match the lookup');
		if (by === 'action' && delivery.result.action_id !== id)
			invalid(`${path}.delivery.result.action_id`, 'does not match the lookup');
		return { protocol: PLAYER_PROTOCOL, status, by, ...(id === undefined ? {} : { id }), delivery };
	}
	if (hasOwn(record, 'delivery')) invalid(`${path}.delivery`, 'must be absent');
	const refusal = asRecord(record.refusal, `${path}.refusal`);
	const code = closed(
		stringField(refusal, 'code', `${path}.refusal`),
		['malformed_request', 'not_found', 'not_ready', 'unavailable'] as const,
		`${path}.refusal.code`
	);
	const message = requireNonempty(
		stringField(refusal, 'message', `${path}.refusal`),
		`${path}.refusal.message`
	);
	return {
		protocol: PLAYER_PROTOCOL,
		status,
		by,
		...(id === undefined ? {} : { id }),
		refusal: { code, message }
	};
}

function validateRetrieveIdentity(
	by: RetrieveBy,
	id: string | undefined,
	present: boolean,
	path: string
): void {
	if (by === 'latest') {
		if (present) invalid(path, 'must be absent for latest');
		return;
	}
	if (id === undefined || id === '') invalid(path, `is required for ${by}`);
	if (by === 'turn') requireTurnID(id, path);
	else requireActionID(id, path);
}

function parseOperationResponse(value: unknown, path: string): OperationResponse {
	const record = asRecord(value, path);
	requireProtocol(record, path);
	closed(stringField(record, 'status', path), ['refused'] as const, `${path}.status`);
	const refusal = asRecord(record.refusal, `${path}.refusal`);
	const code = closed(
		stringField(refusal, 'code', `${path}.refusal`),
		['malformed_operation', 'unsupported_operation'] as const,
		`${path}.refusal.code`
	);
	const message = requireNonempty(
		stringField(refusal, 'message', `${path}.refusal`),
		`${path}.refusal.message`
	);
	const operation = optionalString(refusal, 'operation', `${path}.refusal`);
	if (code === 'malformed_operation') {
		if (operation !== undefined && operation !== '')
			invalid(`${path}.refusal.operation`, 'must be absent');
		return { protocol: PLAYER_PROTOCOL, status: 'refused', refusal: { code, message } };
	}
	if (operation === undefined || operation === '')
		invalid(`${path}.refusal.operation`, 'is required');
	return { protocol: PLAYER_PROTOCOL, status: 'refused', refusal: { code, operation, message } };
}

function parseFrame(value: unknown): PlayerFrame {
	const record = asRecord(value, '$');
	requireProtocol(record, '$');
	const rawType = record.type;
	if (typeof rawType !== 'string') {
		throw new ValidationFailure(
			'unsupported_discriminator',
			'$.type',
			'must be a string discriminator'
		);
	}
	const type = closedFrameType(rawType);
	const selected = FRAME_PAYLOAD[type];
	if (record[selected] === undefined || record[selected] === null)
		invalid(`$.${selected}`, `is required for ${type}`);
	for (const payload of Object.values(FRAME_PAYLOAD)) {
		if (payload !== selected && hasOwn(record, payload)) {
			invalid(`$.${payload}`, `must be absent from ${type}`);
		}
	}
	switch (type) {
		case 'submit_response':
			return {
				protocol: PLAYER_PROTOCOL,
				type,
				response: parseSubmitResponse(record.response, '$.response')
			};
		case 'turn_delivery':
			return {
				protocol: PLAYER_PROTOCOL,
				type,
				delivery: parseDelivery(record.delivery, '$.delivery')
			};
		case 'retrieve_response':
			return {
				protocol: PLAYER_PROTOCOL,
				type,
				retrieval: parseRetrieveResponse(record.retrieval, '$.retrieval')
			};
		case 'operation_response':
			return {
				protocol: PLAYER_PROTOCOL,
				type,
				operation: parseOperationResponse(record.operation, '$.operation')
			};
	}
}

const FRAME_PAYLOAD = {
	submit_response: 'response',
	turn_delivery: 'delivery',
	retrieve_response: 'retrieval',
	operation_response: 'operation'
} as const;

type FrameType = keyof typeof FRAME_PAYLOAD;

function closedFrameType(value: string): FrameType {
	if (!(value in FRAME_PAYLOAD)) {
		throw new ValidationFailure(
			'unsupported_discriminator',
			'$.type',
			`unsupported frame type ${JSON.stringify(value)}`
		);
	}
	return value as FrameType;
}

export function parsePlayerFrame(raw: string): ProtocolParseResult<PlayerFrame> {
	let document: unknown;
	try {
		document = JSON.parse(raw) as unknown;
	} catch {
		return failure(new ValidationFailure('invalid_json', '$', 'message is not valid JSON'));
	}
	try {
		return { ok: true, value: parseFrame(document) };
	} catch (error) {
		if (error instanceof ValidationFailure) return failure(error);
		throw error;
	}
}

function strictKeys(
	record: Record<string, unknown>,
	allowed: readonly string[],
	path: string
): void {
	for (const key of Object.keys(record)) {
		if (!allowed.includes(key)) invalid(`${path}.${key}`, 'is not part of player/v1');
	}
}

function parseObject<T>(
	value: unknown,
	parser: (record: Record<string, unknown>) => T
): ProtocolParseResult<T> {
	try {
		return { ok: true, value: parser(asRecord(value, '$')) };
	} catch (error) {
		if (error instanceof ValidationFailure) return failure(error);
		throw error;
	}
}

export function parseSubmitAction(value: unknown): ProtocolParseResult<SubmitAction> {
	return parseObject(value, (record) => {
		strictKeys(record, ['protocol', 'text', 'idempotency_key'], '$');
		requireProtocol(record, '$');
		const text = stringField(record, 'text', '$');
		const textViolation = actionTextViolation(text);
		if (textViolation === 'blank') invalid('$.text', 'must contain a nonblank action');
		if (textViolation === 'too_long') invalid('$.text', `exceeds ${MAX_ACTION_TEXT_BYTES} bytes`);
		const idempotencyKey = requireIdempotencyKey(
			stringField(record, 'idempotency_key', '$'),
			'$.idempotency_key'
		);
		return { protocol: PLAYER_PROTOCOL, text, idempotency_key: idempotencyKey };
	});
}

export function parseRetrieveRequest(value: unknown): ProtocolParseResult<RetrieveRequest> {
	return parseObject(value, (record) => {
		strictKeys(record, ['protocol', 'type', 'by', 'id'], '$');
		requireProtocol(record, '$');
		closed(stringField(record, 'type', '$'), ['retrieve_result'] as const, '$.type');
		const by = closed(
			stringField(record, 'by', '$'),
			['turn', 'action', 'latest'] as const,
			'$.by'
		);
		const id = optionalString(record, 'id', '$');
		validateRetrieveIdentity(by, id, hasOwn(record, 'id'), '$.id');
		if (by === 'latest') return { protocol: PLAYER_PROTOCOL, type: 'retrieve_result', by };
		return { protocol: PLAYER_PROTOCOL, type: 'retrieve_result', by, id: id as string };
	});
}
