// Package egress is the answer half of player I/O: composing a turn's canonical
// result from durable state, resolving the prose it references, and delivering
// it to the player that turn belongs to — and to nobody else.
//
// # Why this is SemMachina's and not SemStreams'
//
// The ingress half's reason is stated in internal/gateway: session identity is a
// game concern. The egress half's reason is narrower and harder. Upstream's
// `output/websocket` BROADCASTS — `broadcastToClients` iterates the whole client
// map, and its own doc lists "no per-client subject filtering" as a documented
// limitation — so composing it would satisfy "the result was delivered" while
// showing one player's narration to every player in the campaign. That is a
// DISCLOSURE defect, not a performance one, which is why a version bump would not
// fix it and why per-recipient delivery is filed upstream as a generic ask rather
// than pretended to be a game concern.
//
// # Targeting is structural, not careful
//
// Router is handed a Directory whose only method resolves ONE player's live
// connections. There is no way to enumerate every session from here, so broadcast
// is not a thing this package chooses against — it is a thing it cannot express.
// The lookup happens at DELIVERY time and is keyed on the player ENTITY ID the
// result carries, never on a connection identifier captured when the action was
// submitted: a reconnect between submission and resolution is therefore invisible,
// and TurnResult deliberately carries no channel binding for exactly this reason.
//
// # Retrieval reads durable state, never adapter memory
//
// Results answers by action id, by turn id, and with a player's most recent
// TERMINAL result, and every one of those is composed from the turn entity in
// ENTITY_STATES plus the artifacts it references. Nothing is remembered between
// deliveries. A player who was disconnected when their turn resolved is answered
// by the same code path that answers one who was watching.
//
// The composition runs through ledger.Compose — the same pure function the
// campaign archive uses — so the result a player is shown and the manifest the
// archive holds are two readings of one record rather than two accounts of one
// turn. Reading the graph rather than the archive is deliberate on top of that:
// the archive's writer and this package are two independent consumers of the same
// resolved-turn notification, so composing from the archive would race the writer
// for turns that had just ended.
//
// # What travels, and what does not
//
// A delivery carries the reference-only TurnResult plus ONE turn's prose, bounded
// by payload.MaxProseBytes. The verdict's banded intents, the effect batch and
// the stored action stay behind their references and never travel. Nothing
// accumulates: a delivery carries this turn's prose and no previous turn's, so
// per-turn wire cost is flat for the same reason per-turn token cost is.
//
// The server dereferences rather than handing a client an `obj://` to fetch,
// because a fetch-back protocol is one only an interactive adapter can speak, and
// no step here may assume interactive pacing. See payload.TurnDelivery.
//
// # No idle timeout, no pacing assumption
//
// Zero connected recipients is an ordinary outcome and not an error: at email
// cadence the usual state of a player is "away". A delivery with no recipients is
// acknowledged, because the durable answer is retrieval and re-pushing it later
// would be adapter memory wearing a queue's clothes. Nothing here measures the gap
// between a turn resolving and a player reading it.
package egress
