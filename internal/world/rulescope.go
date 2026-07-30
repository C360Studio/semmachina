package world

import (
	"fmt"
	"strings"

	gtypes "github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/processor/rule/expression"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/persona"
	"github.com/c360studio/semmachina/internal/rulepack"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// The world-rule scope boundary: what a DOWNLOADED world may express in JSON.
//
// A world package ships rules, and the standing invariant is that players and
// GMs author entities and rules while only developers author components. That
// is a real authoring surface — world reactions are the point of it — but it is
// an authoring surface over the WORLD, and the turn loop is not world content.
// Turn sequencing lives in internal/rulepack because it is the ENGINE's state
// machine: each hop is guarded on the previous stage's artifact, each
// LLM-triggering hop is capped, and the phase is single-valued state written by
// one recorder. A world rule that reads or writes those facts is turn
// sequencing authored by a stranger; a world rule that publishes a stage
// trigger — or a persona task — is a stranger spending a billed model call on a
// turn it does not own.
//
// So the refusal is at LOAD, before anything is materialized, and it names the
// rule, the position, and the reason — an author has to be able to tell a
// boundary from a bug.
//
// # What this checks, and what it deliberately does not
//
// Four positions carry a name that reaches the engine, and each is checked:
//
//   - condition fields, including the per-action `when` guards upstream's own
//     validator does not walk (processor/rule/config_validation.go
//     validateConditionFields covers Definition.Conditions only);
//   - the `predicate` of every triple-mutating action, which is the direct
//     write path onto a turn entity's facts;
//   - the `subject` of every action that publishes to NATS;
//   - the `bucket` of every update_kv action, whose runtime backstop upstream
//     covers only for graph buckets (see reservedBuckets).
//
// The ENTITY PATTERN is deliberately not refused, and the reason is structural
// rather than a claim that the position list above is exhaustive: a world rule
// cannot MATCH a turn entity in the first place without naming a `turn.*`
// predicate. A turn entity carries turn predicates and nothing else, and
// expression.Evaluator answers false for an absent field whatever the operator,
// so a pattern that selects turn entities selects them for a rule that can never
// fire — unless it names a reserved predicate, which is refused one position up.
// Refusing the pattern too would refuse a shape that cannot do harm.
//
// # Why an unclassified action type fails closed
//
// The gate decides per action TYPE which of its fields can reach the engine, so
// a type it has never seen is a type whose reach is unknown. Upstream adds
// action types (this file covers the twelve in processor/rule/actions.go at the
// pinned version); the next one arrives with the loader silently admitting it.
// An unknown type is therefore refused by name — a typo an author fixes at
// import instead of at fire time, and a new capability someone has to look at
// before a world may use it.
//
// # The half that is NOT here, stated so nobody reads this as complete
//
// The invariant also says caps are mandatory, and this file does not enforce
// them. Every subject that reaches a model TODAY is reserved — the agentic root
// and the tool-result lane cover all five of the loop's declared inputs, so no
// action type dispatches, retries, or feeds a persona from world content — but
// "you may not publish here at all" is a narrower claim than "you may publish
// here if you bound it". What remains open is a world spending through a path
// nobody has enumerated: some future component consuming a subject a world rule
// may legitimately name.
//
// Whoever builds the cap check inherits one PRECONDITION that has already cost
// this file a defect: WHICH FIELD IS THE CAP. `Definition.MaxIterations` is not
// enforced for expression rules — the stateful evaluator records it into
// MatchState and exposes it as `$state.max_iterations` for a `when` guard to
// read, and cron rules reject the field outright — while the enforced cap is
// per-ACTION (`Action.MaxIterations`, defaulting to
// rule.DefaultActionMaxIterations when omitted). A check written against the
// definition-level field would validate a field that does nothing and pass every
// uncapped world.
//
// The rest is left undone deliberately rather than approximated, because "a rule
// path that reaches an LLM" needs a definition first and a check that guessed
// would either miss the path that spends or refuse the rules that cannot.
// Reserving a subject was decidable today from a constant; deciding which paths
// need caps is not. See the change's tasks file, 8.1b(a).

// reservedDomain is a predicate namespace a world rule may not name, carried
// with the reason so a refusal can say what breaks rather than only that
// something does. The prefix always ends in a dot, so it matches whole segments
// and never a name that merely starts with the same letters.
type reservedDomain struct {
	prefix string
	why    string
}

// namespaceWidth is how much of a predicate is reserved.
//
// It exists because the right width is a JUDGEMENT about where the harm stops,
// and the two engine namespaces answer it differently — so the answer has to be
// stated per namespace rather than assumed.
type namespaceWidth uint8

const (
	// wholeDomain reserves `domain.*`. Correct only when EVERY predicate that
	// domain can ever hold is engine state.
	wholeDomain namespaceWidth = iota + 1
	// domainAndCategory reserves `domain.category.*`, leaving the rest of the
	// domain authorable.
	domainAndCategory
)

// enginePredicateNamespaces derives the reserved predicate namespaces from the
// engine constants that define them.
//
// DERIVED rather than listed: each prefix is cut from a predicate
// internal/vocabulary already declares, so a rename moves the reservation with
// it. Restating them as string literals would create a second spelling that a
// rename could silently leave behind — the gate would then admit exactly the
// predicates it exists to refuse, with every test still green.
//
// The two WIDTHS are different, deliberately, and the contrast is the point:
//
//   - `turn.` is reserved whole, because every predicate in that domain is turn
//     state written by a guarded stage. There the domain and the harm coincide.
//   - `campaign.` is NOT, because they do not. The harm is specific to the seed,
//     and the campaign CLOCK — which the project pins as a world fact whose
//     deadlines are threshold rules, i.e. the canonical world reaction — would
//     land in the same domain. Reserving `campaign.` whole would refuse a world
//     rule for so much as reading the clock, and it would do it before any world
//     shipped and made the naming expensive to change.
//   - `player.` is NOT, for the campaign's reason and with a sharper harm.
//     `player.turn.` is the ingress admission gate's pointer, so a world rule
//     that could write it could repoint a player at a terminal turn (granting
//     them a second live turn) or at a live one (locking them out of their own
//     campaign) — with no player-visible refusal, because the gate would answer
//     from data the world authored. `player.character.` is the played-character
//     binding and is exactly the kind of world fact a world may react to, so the
//     reservation stops at the category.
func enginePredicateNamespaces() ([]reservedDomain, error) {
	sources := []struct {
		predicate vocabulary.Predicate
		width     namespaceWidth
		why       string
	}{
		{
			predicate: vocabulary.TurnPhaseCurrent,
			width:     wholeDomain,
			why: "the turn loop is the engine's own state machine — its phase, verdict, roll, effect and " +
				"narration facts are written by guarded stages — so a world rule matching or writing one " +
				"is authoring turn sequencing",
		},
		{
			predicate: vocabulary.CampaignSeedValue,
			width:     domainAndCategory,
			why: "the campaign seed is minted once at instantiation and every roll derives from it, so a " +
				"world rule that could touch it could make replay stop reproducing (the rest of the " +
				"campaign domain, the world clock included, is world content and stays authorable)",
		},
		{
			predicate: vocabulary.CampaignImportCompleted,
			width:     domainAndCategory,
			why: "this is the marker that says the world import FINISHED, and boot gates ingress on it, so a " +
				"world rule that could write it could declare a half-imported world ready to play, and one " +
				"that could clear it would make the next boot re-import a template over a living campaign — " +
				"resetting every fact the template declares and dropping the relationships play created",
		},
		{
			predicate: vocabulary.PlayerTurnCurrent,
			width:     domainAndCategory,
			why: "this is the ingress admission gate's pointer at the turn a player currently holds, so a " +
				"world rule that could write it could hand a player a second live turn or lock them out " +
				"of their own campaign (the rest of the player domain, the played-character binding " +
				"included, is world content and stays authorable)",
		},
	}

	domains := make([]reservedDomain, 0, len(sources))
	for _, source := range sources {
		parts, err := ssvocab.ParsePredicate(string(source.predicate))
		if err != nil {
			return nil, fmt.Errorf(
				"cannot derive the reserved predicate namespace from %q: %w", source.predicate, err)
		}
		prefix := parts.Domain + "."
		if source.width == domainAndCategory {
			prefix += parts.Category + "."
		}
		domains = append(domains, reservedDomain{prefix: prefix, why: source.why})
	}
	return domains, nil
}

// reservedSubject is a NATS subject space a world rule may not publish into,
// carried with the reason, exactly as reservedDomain is.
type reservedSubject struct {
	root string
	why  string
}

// reservedBucket is a KV bucket a world rule may not write, in the same idiom.
type reservedBucket struct {
	name string
	why  string
}

// ruleStateBucket is the rule engine's own state bucket.
//
// Restated, like toolResultRoot and for the same kind of reason: upstream
// declares it as a function-local unexported constant
// (processor/rule/processor.go initializeStateTracker), so there is nothing to
// derive from. Exporting it upstream would delete this line.
const ruleStateBucket = "RULE_STATE"

// reservedBuckets returns the KV buckets a world rule may not write through
// update_kv.
//
// The framework-owned GRAPH buckets are taken from upstream's own list rather
// than named here, so the set cannot fall behind it. Upstream already refuses
// those at fire time; refusing at load only moves the answer to where the author
// can act on it.
//
// RULE_STATE is the one that MATTERS, because upstream does NOT refuse it:
// FrameworkOwnedBuckets() is graph buckets only, while RULE_STATE is created as
// an ordinary KV bucket and holds MatchState — the per-action firing counters
// (`ActionIterations`), the configured cap, and the `SourceRevision` the
// bootstrap stale-replay guard reads. A world rule doing `update_kv` with
// `merge: true` and an empty `action_iterations` map resets the caps on the
// ENGINE's own hops, which is the cap discipline being switched off from world
// content.
//
// Reaching a specific turn's key needs that turn's entity ID, whose instance
// segment is a player-submitted action id an author cannot know — and that is
// exactly why this is closed structurally instead. "You cannot guess the key" is
// a secrecy argument, and it stops being true the day a key becomes derivable.
func reservedBuckets() []reservedBucket {
	owned := gtypes.FrameworkOwnedBuckets()
	buckets := make([]reservedBucket, 0, len(owned)+1)
	for _, name := range owned {
		buckets = append(buckets, reservedBucket{
			name: name,
			why: "it is a framework-owned graph bucket whose writes belong to graph-ingest alone, so a " +
				"world rule writing it would bypass the single-writer contract the whole graph rests on",
		})
	}
	return append(buckets, reservedBucket{
		name: ruleStateBucket,
		why: "it holds the rule engine's own match state — the per-action firing counters, the configured " +
			"caps, and the revision the bootstrap replay guard reads — so a world rule writing it could " +
			"switch off the cap discipline on the engine's own rules",
	})
}

// toolResultRoot is the agentic tool-result lane.
//
// The ONE restated root in this file, because nothing exports it: upstream
// declares it inline as an input port subject (processor/agentic-loop/config.go
// `tool.result.>`, and agentic-tools publishes onto it), and this repo has no
// constant for it either. Restating a string is exactly what the rest of this
// file avoids, so the drift risk is answered where it can be:
// TestReservedSubjectRoots_CoverEveryAgenticLoopInput reads the loop's own
// declared ports and fails if any input stops being covered. Upstream exporting
// the lane is a small engine ask that would delete this constant.
const toolResultRoot = "tool.result."

// reservedSubjectRoots returns the subject spaces a world rule may not publish
// into.
//
// THREE roots, reserved for different harms — which is why each carries its own
// reason rather than sharing one:
//
//   - This engine's own root. Derived from the stage-trigger prefix's first
//     segment rather than restated, and the ROOT rather than the stage prefix
//     itself, because every subject this engine owns lives under it: stage
//     triggers, the resolved notification, the campaign ledger, and the player
//     intake task 9 has not written yet. A list of engine prefixes would be a
//     list somebody forgets to extend on the day it starts mattering. A world
//     rule has no legitimate business there — it owns no ports, so anything it
//     publishes into that space is either a forgery or a message nothing reads.
//
//   - The whole AGENTIC root, not merely its task lane. This one is UPSTREAM's
//     namespace, and the loop declares four inputs under it, each of which a
//     published message reaches: `agent.task.*` dispatches a loop (spends),
//     `agent.signal.*` carries `retry` (spends again) and `feedback` (injects
//     author text into a running persona's context — world content steering the
//     adjudicator), `agent.response.>` is model output a forgery could
//     impersonate, and `agent.approval_response.*` answers a gated tool call, so
//     a world could approve one. Reserving the task lane alone would leave three
//     of the four open behind the weaker argument that an author cannot guess a
//     runtime loop id — a secrecy argument, in a boundary that is otherwise
//     structural.
//
//   - The tool-result lane, the loop's fifth input, which sits outside the
//     agentic root. A forged tool result is fabricated evidence fed to a running
//     adjudicator, and the iteration it triggers is billed.
//
// Reserving these subjects is not the deferred caps work and does not narrow
// it. Caps answer "you may publish here, but bound it"; this answers "you may
// not publish here at all". The second is decidable today from a constant; the
// first still needs "reaching an LLM path" defined.
func reservedSubjectRoots() ([]reservedSubject, error) {
	engineRoot, _, found := strings.Cut(rulepack.StageSubjectPrefix, ".")
	if !found || engineRoot == "" {
		return nil, fmt.Errorf(
			"cannot derive the engine subject root from the stage-trigger prefix %q: it has no leading segment",
			rulepack.StageSubjectPrefix)
	}

	// The agentic root is derived from the filter that names the whole agentic
	// subject space rather than from the task prefix, because the task lane is
	// one of four and deriving from it would re-narrow the reservation the next
	// time somebody reads this.
	agenticRoot, hadWildcard := strings.CutSuffix(persona.AgentSubjectFilter, ">")
	if !hadWildcard || !strings.HasSuffix(agenticRoot, ".") {
		return nil, fmt.Errorf(
			"cannot derive the agentic subject root from %q: it is not a `<root>.>` filter",
			persona.AgentSubjectFilter)
	}

	return []reservedSubject{
		{
			root: engineRoot + ".",
			why: "those subjects carry the engine's own work — the turn loop's stage triggers are " +
				rulepack.StageSubjectFilter + ", and the campaign ledger and player intake share the root — " +
				"so a world package publishing there could trigger, replay or forge a turn it does not own",
		},
		{
			root: agenticRoot,
			why: "those subjects are the agentic loop's own inputs — a task there is answered by " +
				"calling a MODEL, a signal retries or feeds text into a running persona, a response " +
				"impersonates model output, and an approval answers a gated tool call — so a world " +
				"package publishing there spends money and steers the adjudicator; personas are " +
				"dispatched by the engine's own stages, never by world content",
		},
		{
			root: toolResultRoot,
			why: "that subject is the agentic loop's tool-result input, so a world package publishing " +
				"there feeds a running persona fabricated evidence, and the loop iteration it " +
				"provokes is answered by calling a MODEL",
		},
	}, nil
}

// checkRuleScope refuses a world rule that reaches the engine.
func checkRuleScope(definition rule.Definition) error {
	domains, err := enginePredicateNamespaces()
	if err != nil {
		return err
	}
	roots, err := reservedSubjectRoots()
	if err != nil {
		return err
	}
	buckets := reservedBuckets()

	for index, condition := range definition.Conditions {
		where := fmt.Sprintf("condition[%d]", index)
		if err := checkConditionScope(definition.ID, where, condition, domains); err != nil {
			return err
		}
	}

	// Every action list, including cron's. A world rule that could reach the
	// engine from `actions` while being refused from `on_enter` would have the
	// boundary depend on which list an author typed.
	lists := []struct {
		label   string
		actions []rule.Action
	}{
		{"on_enter", definition.OnEnter},
		{"on_exit", definition.OnExit},
		{"while_true", definition.WhileTrue},
		{"on_recovery", definition.OnRecovery},
		{"actions", definition.Actions},
	}
	for _, list := range lists {
		for index, action := range list.actions {
			where := fmt.Sprintf("%s[%d]", list.label, index)
			if err := checkActionScope(definition.ID, where, action, domains, roots, buckets); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkActionScope(
	ruleID, where string, action rule.Action,
	domains []reservedDomain, roots []reservedSubject, buckets []reservedBucket,
) error {
	for index, guard := range action.When {
		guardWhere := fmt.Sprintf("%s when[%d]", where, index)
		if err := checkConditionScope(ruleID, guardWhere, guard, domains); err != nil {
			return err
		}
	}

	switch action.Type {
	case rule.ActionTypeAddTriple, rule.ActionTypeRemoveTriple,
		rule.ActionTypeUpdateTriple, rule.ActionTypeReplaceOwned:
		// These write graph facts. Their `subject` is an ENTITY ID override,
		// not a NATS subject, and substitution there is ordinary authoring —
		// it is how a rule writes onto a related entity — so only the
		// predicate is a reserved position.
		return checkReservedPredicate(ruleID, where, action.Predicate, domains)

	case rule.ActionTypePublish, rule.ActionTypePublishAgent, rule.ActionTypeApprove:
		// These put a message on a NATS subject, and the SUBJECT is what is
		// checked rather than the action type — which is what makes the check
		// worth having. `publish` cannot forge an engine PAYLOAD (the rule
		// engine's publish body is a fixed shape), but payload shape is not the
		// harm on either reserved root: a stream captures core publishes on its
		// own subject filter, so a publish into the stage space lands on
		// TURN_STAGES as a malformed trigger for a stage runner to choke on,
		// and a publish into the agent task space lands on AGENT for a loop to
		// pick up. Refusing `publish_agent` alone would leave the plain-publish
		// half of both open.
		return checkReservedSubject(ruleID, where, action.Subject, roots)

	case rule.ActionTypeUpdateKV:
		// A fourth reserved position, and the one whose runtime backstop is
		// incomplete. Upstream refuses update_kv against the framework-owned
		// GRAPH buckets after substitution (executeUpdateKV →
		// graph.IsFrameworkOwnedBucket), which is why ENTITY_STATES is safe —
		// but RULE_STATE is not on that list, and it holds the rule engine's own
		// MatchState. See reservedBuckets.
		return checkReservedBucket(ruleID, where, action.Bucket, buckets)

	case rule.ActionTypeDeny:
		// Cannot reach the turn loop: deny returns a verdict to its caller and
		// emits a governance audit event. It publishes to no author-named
		// subject and writes no triple.
		return nil

	case rule.ActionTypeLifecycleTransition, rule.ActionTypeLifecycleComplete, rule.ActionTypeLifecycleFail:
		// Cannot reach the turn loop: these move a lifecycle-managed
		// Participant through a registered workflow, and SemMachina registers
		// none — the turn's phase is a graph triple written by the turn
		// recorder (design D1), not a lifecycle phase. If a campaign or scene
		// lifecycle is registered later, this arm is where the boundary has to
		// be re-decided rather than rediscovered.
		return nil

	default:
		return fmt.Errorf(
			"rule %q %s has action type %q, which this loader cannot bound; a world rule may only use action "+
				"types whose reach into the engine has been decided, so an unrecognized type is refused rather "+
				"than admitted unchecked",
			ruleID, where, action.Type)
	}
}

// checkConditionScope refuses a condition that branches on an engine fact.
func checkConditionScope(
	ruleID, where string, condition expression.ConditionExpression, domains []reservedDomain,
) error {
	domain, reserved := reservedDomainFor(condition.Field, domains)
	if !reserved {
		return nil
	}
	return fmt.Errorf(
		"rule %q %s matches %q, which is in the reserved %q predicate namespace: %s. A world rule reacts to "+
			"world facts; the engine's own rules live in internal/rulepack",
		ruleID, where, condition.Field, domain.prefix+"*", domain.why)
}

// checkReservedPredicate refuses a triple write into an engine namespace.
func checkReservedPredicate(ruleID, where, predicate string, domains []reservedDomain) error {
	if err := checkNameIsLiteral(ruleID, where, "predicate", predicate); err != nil {
		return err
	}
	domain, reserved := reservedDomainFor(predicate, domains)
	if !reserved {
		return nil
	}
	return fmt.Errorf(
		"rule %q %s writes %q, which is in the reserved %q predicate namespace: %s. A world rule writes world "+
			"facts; the engine's own rules live in internal/rulepack",
		ruleID, where, predicate, domain.prefix+"*", domain.why)
}

// checkReservedBucket refuses an update_kv write into a reserved KV bucket.
func checkReservedBucket(ruleID, where, bucket string, buckets []reservedBucket) error {
	if err := checkNameIsLiteral(ruleID, where, "bucket", bucket); err != nil {
		return err
	}
	if bucket == "" {
		return nil
	}
	for _, reserved := range buckets {
		if bucket != reserved.name {
			continue
		}
		return fmt.Errorf(
			"rule %q %s writes the reserved KV bucket %q: %s. A world rule may keep its own state in its "+
				"own bucket; the engine's is not world content",
			ruleID, where, bucket, reserved.why)
	}
	return nil
}

// checkReservedSubject refuses a publish into a reserved subject space.
func checkReservedSubject(ruleID, where, subject string, roots []reservedSubject) error {
	if err := checkNameIsLiteral(ruleID, where, "subject", subject); err != nil {
		return err
	}
	if subject == "" {
		return nil
	}
	for _, reserved := range roots {
		if !strings.HasPrefix(subject, reserved.root) {
			continue
		}
		return fmt.Errorf(
			"rule %q %s publishes to %q, which is inside the reserved %q subject space: %s. A world rule "+
				"reacts inside the world; it does not hand work to the engine",
			ruleID, where, subject, reserved.root+">", reserved.why)
	}
	return nil
}

// checkNameIsLiteral refuses a reserved-position name assembled at fire time.
//
// The rule engine substitutes `$...` tokens in Action.Predicate and
// Action.Subject before it uses them (processor/rule/actions.go), so a template
// resolves into whatever the trigger entity happened to carry — including a
// name inside a reserved namespace. A load-time gate cannot read that, and a
// gate that checked only literals would report a package clean while admitting
// the evasion. Values are not reserved positions and stay freely templated;
// this is about NAMES.
func checkNameIsLiteral(ruleID, where, field, value string) error {
	if !strings.Contains(value, "$") {
		return nil
	}
	return fmt.Errorf(
		"rule %q %s %s %q is assembled by substitution at fire time, so the loader cannot tell which namespace "+
			"it resolves into; a world rule must name a literal %s",
		ruleID, where, field, value, field)
}

// reservedDomainFor reports whether a field or predicate name sits in a
// reserved predicate namespace.
//
// It matches a dot-terminated PREFIX rather than requiring a successful
// predicate parse, because this same check runs over action `when` guards,
// whose fields are not held to the predicate alphabet at all. The trailing dot
// is what keeps the match on segment boundaries: `campaign.seed.` cannot match
// `campaign.seedling.growth`.
//
// Names beginning with `$` are skipped, and that is safe rather than merely
// conventional: a condition field resolves through exactly four namespaces
// (processor/rule/expression/evaluator.go evaluateConditionWithStateAndMessage)
// — `$state.*`/`$prev.*` are the rule's own match bookkeeping,
// `$entity.lifecycle.*` reads the lifecycle harness this engine registers no
// workflows with, `$message.*` walks the triggering message payload, and a BARE
// name is the only one that reads graph triples. So a `$` field cannot name an
// engine fact, and skipping it does not leave a hole for one.
func reservedDomainFor(field string, domains []reservedDomain) (reservedDomain, bool) {
	if field == "" || strings.HasPrefix(field, "$") {
		return reservedDomain{}, false
	}
	for _, reserved := range domains {
		if strings.HasPrefix(field, reserved.prefix) {
			return reserved, true
		}
	}
	return reservedDomain{}, false
}
