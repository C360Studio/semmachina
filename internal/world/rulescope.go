package world

import (
	"fmt"
	"strings"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/processor/rule"
	"github.com/c360studio/semstreams/processor/rule/expression"
	ssvocab "github.com/c360studio/semstreams/vocabulary"

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
// Four positions can reach protected graph state, and each is checked:
//
//   - condition fields, including the per-action `when` guards upstream's own
//     validator does not walk (processor/rule/config_validation.go
//     validateConditionFields covers Definition.Conditions only);
//   - the `predicate` of every triple-mutating action, which is the direct
//     write path onto an entity's facts;
//   - the `subject` of every triple-mutating action, which must remain the
//     narrowed trigger entity;
//   - reference-shaped `object` values, which may name only a narrowed entity.
//
// Actions also face a categorical capability allowlist and a mandatory bound.
// Downloaded packages may mutate their narrowed graph entities or return a
// deny verdict. They may not publish messages, dispatch personas, approve tool
// calls, write arbitrary KV buckets, or drive lifecycle workflows. Those
// capabilities require ownership contracts Stage 3 does not assign to world
// packages. Every admitted action is bounded: omission uses upstream's default
// of three, while an explicit value must be between one and four.
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
// The cap is deliberately Action.MaxIterations, not Definition.MaxIterations.
// The latter is not the execution bound for expression-rule actions. A nil
// action pointer is safe while upstream resolves it to a value inside this
// loader's ceiling (currently three); that default is checked at load. Explicit
// zero means unlimited upstream and is therefore refused here.

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
			predicate: vocabulary.CampaignExperiencePersonaPack,
			width:     domainAndCategory,
			why: "the selected persona and mechanics packs are immutable campaign-instantiation provenance, so a " +
				"world rule may neither branch on nor rewrite the campaign's sealed experience",
		},
		{
			predicate: vocabulary.PlayerTurnCurrent,
			width:     domainAndCategory,
			why: "this is the ingress admission gate's pointer at the turn a player currently holds, so a " +
				"world rule that could write it could hand a player a second live turn or lock them out " +
				"of their own campaign (the rest of the player domain, the played-character binding " +
				"included, is world content and stays authorable)",
		},
		{
			predicate: vocabulary.CaseSolutionCulprit,
			width:     domainAndCategory,
			why: "the canonical culprit, method, and motive are immutable authored seed truth; a world rule " +
				"that reads them bypasses epistemic projection, and one that writes them changes the answer",
		},
		{
			predicate: vocabulary.EvidenceTruthStatusCurrent,
			width:     domainAndCategory,
			why: "evidence truth status is immutable authored seed truth; a world rule that reads it reveals " +
				"whether evidence is a clue or red herring, and one that writes it rewrites the case",
		},
		{
			predicate: vocabulary.CaseLifecyclePhase,
			width:     domainAndCategory,
			why: "case lifecycle phase and event receipts are structural engine state; downloadable world rules " +
				"must not spoof or branch on the case state machine",
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

// checkRuleScope refuses a world rule that reaches the engine.
func checkRuleScope(definition rule.Definition) error {
	domains, err := enginePredicateNamespaces()
	if err != nil {
		return err
	}
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
			if err := checkActionScope(
				definition.ID, where, action, len(definition.RelatedPatterns) > 0, domains,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkActionScope(
	ruleID, where string, action rule.Action, hasRelatedPattern bool,
	domains []reservedDomain,
) error {
	for index, guard := range action.When {
		guardWhere := fmt.Sprintf("%s when[%d]", where, index)
		if err := checkConditionScope(ruleID, guardWhere, guard, domains); err != nil {
			return err
		}
	}
	if err := checkActionBound(ruleID, where, action); err != nil {
		return err
	}

	switch action.Type {
	case rule.ActionTypeAddTriple, rule.ActionTypeRemoveTriple,
		rule.ActionTypeUpdateTriple, rule.ActionTypeReplaceOwned:
		return checkGraphActionScope(ruleID, where, action, hasRelatedPattern, domains)

	case rule.ActionTypeDeny:
		// Cannot reach the turn loop: deny returns a verdict to its caller and
		// emits a governance audit event. It publishes to no author-named
		// subject and writes no triple.
		return nil

	case rule.ActionTypePublish, rule.ActionTypePublishAgent, rule.ActionTypeApprove,
		rule.ActionTypeUpdateKV, rule.ActionTypeLifecycleTransition,
		rule.ActionTypeLifecycleComplete, rule.ActionTypeLifecycleFail:
		return fmt.Errorf(
			"rule %q %s uses action capability %q, which is not assigned to downloadable world packages: %s",
			ruleID, where, action.Type, unassignedCapabilityReason(action.Type))

	default:
		return fmt.Errorf(
			"rule %q %s has action capability %q, which this loader cannot bound; a world rule may only use action "+
				"types whose reach into the engine has been decided, so an unrecognized type is refused rather "+
				"than admitted unchecked",
			ruleID, where, action.Type)
	}
}

// maxWorldPackageActionIterations is the package ceiling. Four preserves the
// shipped spent-supplies reaction, whose fourth bounded pass is intentional,
// while still keeping downloaded work inside a small, reviewable envelope.
const maxWorldPackageActionIterations = 4

func checkActionBound(ruleID, where string, action rule.Action) error {
	if action.MaxIterations == nil {
		// Omission is safe only while upstream's default remains inside this
		// loader's contract. Fail closed if the pinned dependency ever drifts.
		if rule.DefaultActionMaxIterations >= 1 &&
			rule.DefaultActionMaxIterations <= maxWorldPackageActionIterations {
			return nil
		}
		return fmt.Errorf(
			"rule %q %s omits max_iterations, but upstream's default %d is outside the downloadable-world "+
				"range 1..%d",
			ruleID, where, rule.DefaultActionMaxIterations, maxWorldPackageActionIterations)
	}
	if *action.MaxIterations > 0 && *action.MaxIterations <= maxWorldPackageActionIterations {
		return nil
	}
	return fmt.Errorf(
		"rule %q %s action max_iterations must be between 1 and %d when explicit; 0 is unlimited upstream "+
			"(omit the field to use the bounded default of %d)",
		ruleID, where, maxWorldPackageActionIterations, rule.DefaultActionMaxIterations)
}

func unassignedCapabilityReason(actionType string) string {
	switch actionType {
	case rule.ActionTypePublish:
		return "no world-owned NATS subject contract exists at Stage 3"
	case rule.ActionTypePublishAgent:
		return "persona dispatch and paid model work are owned by engine stages"
	case rule.ActionTypeApprove:
		return "tool approval is an engine and operator authority"
	case rule.ActionTypeUpdateKV:
		return "no package-owned KV bucket namespace exists at Stage 3"
	default:
		return "lifecycle workflows are owned by the engine's built-in receipt rules"
	}
}

// checkGraphActionScope closes both coordinates by which a downloaded rule can
// escape the instance that boot narrows its entity patterns onto.
//
// Subject is the mutation target. The trigger entity is provably in-instance;
// an arbitrary literal or graph-derived template is not. Every string Object
// also faces the relationship rule used by message.Triple.IsRelationship:
// canonical IDs become edges even when the predicate is scalar, because rule
// actions emit no datatype. Related IDs are safe when a related pattern exists
// because boot narrows every related pattern through the same four instance
// positions as the primary.
func checkGraphActionScope(
	ruleID, where string,
	action rule.Action,
	hasRelatedPattern bool,
	domains []reservedDomain,
) error {
	if err := checkReservedPredicate(ruleID, where, action.Predicate, domains); err != nil {
		return err
	}
	if action.Subject != "" && action.Subject != "$entity.id" {
		return fmt.Errorf(
			"rule %q %s graph action subject %q is not provably in the same instance as its narrowed entity "+
				"pattern; omit subject or use exactly $entity.id",
			ruleID, where, action.Subject)
	}

	// remove_triple clears a predicate and upstream ignores Action.Object.
	if action.Type == rule.ActionTypeRemoveTriple {
		return nil
	}
	// Empty replace_owned is the explicit "clear this owned group" operation,
	// so it creates no entity link and is safe.
	if action.Type == rule.ActionTypeReplaceOwned && action.Object == "" {
		return nil
	}
	switch action.Object {
	case "$entity.id":
		return nil
	case "$related.id":
		if hasRelatedPattern {
			return nil
		}
	}
	if strings.Contains(action.Object, "$") || message.IsValidEntityID(action.Object) ||
		ObjectShapeFor(vocabulary.Predicate(action.Predicate)) == ShapeReference {
		return fmt.Errorf(
			"rule %q %s graph action object %q is an entity reference that is not provably in the same instance "+
				"as its narrowed entity patterns; use $entity.id, or $related.id with a declared related pattern",
			ruleID, where, action.Object)
	}
	// A non-ID literal on a non-reference predicate cannot become a graph edge.
	return nil
}

// checkConditionScope refuses a condition that branches on an engine fact.
func checkConditionScope(
	ruleID, where string, condition expression.ConditionExpression, domains []reservedDomain,
) error {
	if strings.HasPrefix(condition.Field, "$entity.lifecycle.") {
		return fmt.Errorf(
			"rule %q %s matches %q, which reads lifecycle-managed state; downloadable world rules may use "+
				"$state.*, $prev.*, and $message.* runtime projections, but the engine's built-in rules alone "+
				"may branch on $entity.lifecycle.*",
			ruleID, where, condition.Field)
	}
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

// checkNameIsLiteral refuses a reserved-position name assembled at fire time.
//
// The rule engine substitutes `$...` tokens in Action.Predicate before it uses
// it, so a template resolves into whatever the trigger entity happened to
// carry — including a name inside a reserved namespace. A load-time gate cannot
// read that. Values stay freely templated where their vocabulary shape permits;
// this is about predicate names.
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
// Names beginning with `$` are skipped here because they are projections, not
// graph predicate names. checkConditionScope handles the one protected
// projection first: `$entity.lifecycle.*` reads lifecycle-managed state and is
// refused for downloaded worlds. `$state.*` and `$prev.*` are rule match
// bookkeeping, while `$message.*` walks the triggering payload; those remain
// legitimate world-rule inputs and do not belong in this predicate-prefix gate.
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
