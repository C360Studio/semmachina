# Magic-System Product Brief

**Status:** Research recommendation; no implementation authorized during the SemStreams code hold.
**Date:** 2026-08-05.

## Executive recommendation

Build toward a **seeded-discovery magic system**:

- the world's magical physics are fixed and versioned before play;
- character creation uses a deliberately authored starter catalog;
- a larger latent catalog is generated or authored at world creation and revealed through play;
- advanced campaigns may permit live invention, but only by compiling an ability proposal into a validated,
  declarative `AbilitySpec` consumed by a fixed deterministic resolver at a safe phase boundary; and
- the LLM classifies fictional intent and positioning, explains discoveries, proposes content, and narrates
  results, while the engine owns costs, timing, targeting, factual traces, resolution, state mutation, and replay.

This is not a recommendation to combine the complete rules of several popular games. The research points to a
layered design assembled around one SemMachina thesis:

> The laws existed before the player arrived. The possibilities did not.

The most promising synthesis is:

1. **Technique + Domain** as the player-facing language for magical intent and research;
2. **Trigger + Cost + Target + Delivery + Effect + Modifier + Duration + Limits** as the executable grammar; and
3. **material and condition interactions** as the world's discoverable magical ecology.

The same ability therefore has three representations: fiction for imagination, a card for player trust, and a
validated effect graph for execution.

## Product hypothesis

We hypothesize an addressable RPG niche for whom learning and manipulating the system is a primary pleasure rather
than support for combat or story. Its size, conversion, and willingness to pay are unknown. Relevant motivations
include progression,
mastery, discovery, design, collection, optimization, and expression. Quantic Foundry's motivation model groups
progress and mastery separately from exploration and world customization, supporting the decision to treat
buildcraft and discovery as related but distinct loops
([motivation profile](https://quanticfoundry.com/2015/07/20/how-we-developed-the-gamer-motivation-profile-v2/)).
Research on player-experience needs also ties autonomy and competence to enjoyment and persistence, which supports
build expression plus learnable feedback rather than option count by itself
([PENS](https://selfdeterminationtheory.org/player-experience-of-needs-satisfaction-pens/)).
Research on curiosity in games further suggests that discovery works when uncertainty is paired with anticipated
reward; inscrutability alone is not a feature
([Exploring Curiosity in Games](https://eprints.whiterose.ac.uk/id/eprint/210197/)).

Exploratory LitRPG sources provide directional rather than representative evidence. They describe clear
progression, meaningful skill choice, consistent rules, consolidation after advancement, and clever system use as
genre pleasures. They also object to stat dumps, arbitrary exceptions, unearned power, and systems that stop
mattering to the story
([genre spotlight](https://ninc.com/litrpg-a-genre-spotlight/),
[reader discussion](https://www.reddit.com/r/litrpg/comments/1d8ktzn)). These sources should inform prototypes, not
substitute for player research.

The candidate initial segment to test is the overlap of three working labels, not validated market segments:

- **system explorers**, who want to discover what unfamiliar mechanics do;
- **buildcrafters**, who want to combine known mechanics into personal solutions; and
- **fictional experimenters**, who want the world to understand an intention that no fixed menu could enumerate.

Collectors, tacticians, power-progression players, and story-first players are candidate adjacent audiences. A
single numerical-detail setting is unlikely to serve all of them, so the interface should disclose depth in layers.
Narrative-first and AI-skeptical players are distinct groups with potentially different objections. Clear
provenance, visible human authorship, and mechanical accountability should be tested as product behavior, not used
only as reassurance copy.

## Research method and limits

The comparison used three source classes:

1. publisher rules, SRDs, manuals, and developer explanations for what a system actually does;
2. designer commentary and postmortems for intended tradeoffs; and
3. player discussion and criticism for recurring friction.

Official descriptions are reliable for mechanics but naturally favorable. Community criticism is valuable for
finding failure modes but is anecdotal and selection-biased. Popularity was not treated as proof that a mechanic
fits SemMachina. No market-size or willingness-to-pay claim has been established.

## Comparative findings

Each system description below is sourced. The **Adopt / Adapt / Reject** judgments are SemMachina design
inferences, not claims made by the cited publishers or researchers.

### Ars Magica: a language for invention

Ars Magica combines Techniques with Forms, distinguishes learned formulaic spells from flexible spontaneous
magic, and makes laboratory research part of long-term play. Atlas Games describes it as a benchmark magic system
and exposes spell guidelines and indexes alongside a large catalog
([Ars Magica product line](https://www.atlas-games.com/arsmagica/)).

**Adopt:** A small semantic grammar can generate a large design space. Spell research deserves its own play loop,
not a crafting dialog attached to combat.

**Adapt:** Technique + Domain should compile into a stricter executable grammar. The LLM can interpret intent, but
it cannot assign arbitrary magnitude or bypass costs.

**Reject:** Do not expose the full construction arithmetic during normal character creation. Detailed design tools
belong in an optional laboratory surface.

### Mage: fictional reach needs mechanical anchors

Mage: The Ascension's broad Spheres make magical intention unusually expressive, but paradigm fit, witnesses,
effect requirements, and Paradox create recurring judgment surfaces. Mage: The Awakening answers some of that
ambiguity with Practices, explicit spell factors, Reach, and a calculable Paradox risk. The improvement is
instructive: tighter semantics improve trust but can move the burden into multi-step procedure
([Awakening creative thaumaturgy](https://theonyxpath.com/the-creative-arts-mage-the-awakening/),
[Reach design](https://theonyxpath.com/reaching-the-next-level-mage-the-awakening/)).

**Adopt:** Let players describe what they are trying to accomplish before choosing a canned animation.

**Adapt:** The adjudicator translates that intention into a proposed `ActionPlan` using closed effect factors and
magnitude bands. A Reach-like budget can expose safe, overreaching, ritual-only, or invalid proposals before the
player commits resources.

**Reject:** Do not make consistency depend on whether the current LLM finds a proposal persuasive.

### Pathfinder 2e: action clarity and trait discipline

Pathfinder's encounter mode gives a participant three actions, supports multi-action activities, and normally
allows one reaction. Actions and traits make cost and interaction visible
([Pathfinder encounter rules](https://2e.aonprd.com/Rules.aspx?ID=2007)).

**Adopt:** Use one universal action budget and explicit traits. The player should be able to compare mobility,
setup, defense, and offense without learning several unrelated economies.

**Adapt:** SemMachina need not inherit Pathfinder's exact three-action balance. Prototype two and three action
budgets against natural-language turns before choosing.

**Reject:** Do not make the player search through a large keyword corpus to understand an ordinary turn.

### Dungeons & Dragons: iconic catalogs teach the world

D&D provides a large, explicit spell catalog and a recognizable action vocabulary. The 2024 free rules preserve
improvised actions under DM judgment while defining common actions and the Magic action
([playing the game](https://www.dndbeyond.com/sources/dnd/br-2024/playing-the-game),
[spells](https://www.dndbeyond.com/sources/dnd/br-2024/spells/)).

**Adopt:** A starter catalog is both onboarding and worldbuilding. Named, concrete spells give players reference
points from which to imagine new ones.

**Adapt:** Preserve iconic ability cards and class identity without inheriting long spell lists as the primary
discovery interface.

**Reject:** Avoid exception-heavy natural-language mechanics and choices that are flavorful but predictably
inferior.

### Dungeons & Dragons 4e: typed cards without fictional sameness

D&D 4e presented powers with consistent fields for action, range, target, attack, defense, hit or miss effect,
keywords, and refresh cadence. That shape is close to an executable catalog contract
([core-rules review](https://www.rpg.net/reviews/archive/13/13986.phtml)).

**Adopt:** Shared typed schemas, explicit refresh timing, and tactical role metadata make large catalogs easier to
validate and tool.

**Adapt:** Preserve distinct magical traditions in presentation, resources, and world interactions even when their
artifacts compile to the same schema.

**Reject:** Do not let a universal card format make fictionally different traditions feel mechanically identical,
or let interaction tracking turn every encounter into a grind.

### Lancer: modular identity with a hard tactical contract

Lancer separates freeform narrative play from a precise tactical mech game and makes modular equipment, talents,
and licenses central to character growth. Its companion tool demonstrates that validation, compendium search, and
build preview are part of making a complex data-driven catalog usable
([Lancer overview](https://lancer.wiki.gg/wiki/LANCER),
[COMP/CON source](https://github.com/massif-press/compcon)).

**Adopt:** Separate magical identity from equipped execution. A tradition or license can grant a vocabulary and
catalog without locking every future slot.

**Adapt:** Use a compendium and build preview so modularity remains legible.

**Reject:** Do not let the narrative/tactical split feel like two unrelated characters or two unrelated games.

### Blades in the Dark: freeze the stakes before resolution

Blades turns a fictional judgment into visible position and effect. Effect is classified as limited, standard, or
great, informed by potency, quality, and scale; the player may trade position for effect after the GM announces the
assessment ([Blades effect rules](https://bladesinthedark.com/effect)).

**Adopt:** The adjudicator should expose expected risk, effect, costs, targets, and assumptions before resolution.
This is the model for SemMachina's frozen `ActionPlan`.

**Adapt:** Use the LLM for fictional positioning but calculate all applicable structured modifiers in code.

**Reject:** Do not leave the final effect band as unexplained GM intuition. The resolution card must show why.

### Powered by the Apocalypse: fiction selects mechanics

Apocalypse World moves trigger when the fiction satisfies their conditions; a player cannot invoke the mechanics
without doing the fictional thing. The official reference sheets show the trigger-and-resolution shape
([Apocalypse World reference](https://apocalypse-world.com/ApocalypseWorldBasicRefbook2ndEd.pdf)).

**Adopt:** Natural language should select or parameterize mechanics through established fictional facts.

**Adapt:** The LLM emits a closed verdict or candidate action. Rules match that structure; rules never inspect
prose.

**Reject:** Do not make every possible action a bespoke narrative move. The magic kernel needs reusable primitives.

### GURPS and HERO: effect accounting belongs behind the compiler

Effect-based construction systems demonstrate that many fictional powers can share a small set of mechanical
effects plus advantages and limitations. They are effectively human-operated compilers
([GURPS Powers introduction](https://www.sjgames.com/gurps/books/powers/intro.pdf),
[HERO basic-rules sample](https://d1vzi28wh99zvq.cloudfront.net/pdf_previews/64691-sample.pdf)).

**Adopt:** Normalize apparently different spells into reusable effects, costs, constraints, and modifiers.

**Adapt:** Let authoring tools perform construction accounting and show the player only the consequential tradeoffs.

**Reject:** Do not ask a new player to construct a legal power from a point-buy manual before play.

### Path of Exile: tags make combinations scalable

Path of Exile combines active skill gems with support gems whose compatibility is governed by typed skill
characteristics and support-specific constraints, partially surfaced through tags. Its official feature overview
places the passive tree, gems, and item economy within the same build system
([game overview](https://www.pathofexile.com/game),
[GDC design talk](https://www.gdcvault.com/play/1025784/Designing-Path-of-Exile-to)).

**Adopt:** Every effect and modifier needs machine-readable compatibility tags. Modifiers should change behavior,
not merely add percentages. Approved composition points should distinguish targeting, delivery, repetition,
conversion, trigger, and cost changes.

**Adapt:** Present a small local interaction graph around the current build instead of exposing the entire global
graph.

**Reject:** Avoid dependency opacity, costly respec, gear-socket coupling, and a metagame that expects external
planning tools.

### Noita: the laboratory is part of the game

Noita combines modular wand construction with a world whose materials are simulated. The important result is not
only a large spell space; experimentation produces observable evidence in the same world where the spell will be
used ([official overview](https://noitagame.com/),
[GDC design talk](https://www.gdcvault.com/play/1025695/Exploring-the-Tech-and-Design)).
Non-shuffle wands execute spell tokens in order; modifiers mutate the current cast state, while multicast and
triggered spells create nested cast blocks. Capacity, mana, delay, recharge, spread, and shuffle behavior constrain
the resulting program.

**Adopt:** Provide a safe spell laboratory with deterministic step-through, dummy targets, material fixtures, and
resource traces.

**Adapt:** Represent a composition as an inspectable AST or instruction sequence. The laboratory should expose
token order, nesting, cast-state mutation, resource and projectile cardinality, and termination or budget failure.
Mystery should come from undiscovered interactions, not from an unreadable evaluator.

**Reject:** Do not make accidental self-destruction the normal price of learning the interface.

### Diablo, Last Epoch, and Grim Dawn: stage the depth

ARPGs distribute character power across skills, equipment, passives, and progression systems. Diablo II compiles
an exact ordered rune sequence plus a compatible base into a named authored item
([Rune Words](https://classic.battle.net/DIABLO2EXP/ITEMS/runewords.shtml)). Diablo IV's Loot Reborn moved complexity
away from every dropped item and toward deliberate modification
([Loot Reborn](https://news.blizzard.com/en-gb/article/24077223/galvanize-your-legend-in-season-4-loot-reborn)).
Last Epoch localizes transformation in per-skill trees, gives crafting a visible Forging Potential budget, and
supports bounded fusion between a named unique and inherited affixes
([crafting](https://forum.lastepoch.com/t/crafting-changes-coming-to-eternal-legends-update-0-8-4/45597/1),
[Legendary Potential](https://forum.lastepoch.com/t/legendary-items-and-eternity-cache/45738)). Grim Dawn attaches
secondary Devotion powers to explicit trigger hooks on primary skills
([devotion guide](https://www.grimdawn.com/guide/character/devotion/)).

**Adopt:** Introduce one axis of customization at a time. Use discoverable named formulas, per-ability
transformation, and explicit hooks such as `on_hit`, `on_crit`, `on_kill`, or `on_condition_applied`.

**Adapt:** A SemMachina item should expose one clear identity, one build-changing property, and a small number of
supporting attributes. Recipe validation should be visible before consuming components.

**Reject:** Avoid silent recipe failure, mandatory multi-slot build recipes, conditional-affix soup, and loot whose
only purpose is comparing larger numbers or filtering hundreds of irrelevant drops.

### Magic: The Gathering: precise pieces require a deterministic interaction order

Magic classifies spell, activated, triggered, and static abilities; it defines timing, targets, continuous effects,
and an explicit interaction order. Its comprehensive rules demonstrate both the power and eventual complexity of
allowing content to modify rules
([Magic comprehensive rules](https://magic.wizards.com/en/rules)).
Its color identities constrain which capabilities may combine, while New World Order deliberately moves complexity
away from common cards
([constraints](https://magic.wizards.com/en/news/making-magic/constraints-and-defaults-2019-07-15),
[complexity placement](https://magic.wizards.com/en/news/making-magic/new-world-order-2011-12-05)).

**Adopt:** Define hard capability boundaries plus canonical timing, event, replacement, prevention, stacking, and
expiry semantics before allowing runtime-authored abilities. Place simple effects in the starter catalog and unlock
rarer grammatical forms gradually.

**Adapt:** Compile friendly cards into precise internal instructions and provide an interaction trace on demand.

**Reject:** Never require ordinary players to understand a comprehensive-rules document or a hidden layer system.
Reject legal-looking interactions that create unbounded or unshortenable turns; Magic's Nadu ban is a useful
failure case
([ban rationale](https://magic.wizards.com/en/news/announcements/august-26-2024-banned-and-restricted-announcement)).

### Magicka: a compact grammar beats a large permutation count

Magicka combines a small elemental vocabulary with delivery forms, oppositions, and derived reactions. The same
queued elements can be self-cast, area-cast, cast forward, or applied to a weapon; element precedence determines
whether the forward result becomes a projectile, beam, spray, discharge, or another form
([Magicka postmortem](https://www.gamedeveloper.com/business/postmortem-arrowhead-game-studios-i-magicka-i-)).

**Adopt:** Form + element + delivery + target is a fiction-readable invention grammar. Explicit oppositions and
derived states make the world feel coherent.

**Adapt:** Unlock vocabulary gradually and retain recipe history, previews, and situational tradeoffs.

**Reject:** Do not treat the mathematical count of possible permutations as content quality. Without context and
counters, the space collapses into a few memorized recipes.

### Baba Is You: executable rules can also be explanations

Baba Is You demonstrates a constrained rule language whose representation is both human-readable and executable.
It also demonstrates how quickly tracking load grows when many simple rules are active simultaneously
([design talk](https://www.gamedeveloper.com/design/video-understanding-the-rules-of-i-baba-is-you-i-)).

**Adopt:** A compiled spell should be explainable as a short, stable rule diff.

**Adapt:** Scope every invented rule by subject, target, duration, phase, and budget. Cap simultaneously active
invented rules, provide an always-visible active-rule recap, and make draft simulation reversible.

**Reject:** Generated content cannot rewrite evaluation order, compiler semantics, or the rule grammar itself.

## Adopt, adapt, reject

### Adopt

- A stable universal action economy.
- A compact effect vocabulary with precise timing.
- Compatibility tags and explicit targeting.
- Transparent pre-resolution stakes.
- A starter catalog that teaches the magical laws.
- A safe laboratory for experimentation.
- Progressive disclosure of system depth.
- Reversible early experimentation and meaningful later commitment.
- Versioned abilities with exact provenance and replay.

### Adapt

- Freeform magic becomes structured intent compilation.
- Point-based construction becomes an internal budget and validation model.
- Support gems become typed effect modifiers rather than unrestricted rule fragments.
- GM rulings become inspectable adjudicator verdicts followed by deterministic resolution.
- Random loot becomes authored or generated affordances with a reason to exist in a build.
- Hidden discovery preserves unknown content but never hides already-learned mechanics.

### Reject

- The LLM changing mechanics during narration.
- Procedural uniqueness based only on names, rarity colors, and numeric affixes.
- Permanent trap choices before the player understands the system.
- Unbounded reaction chains.
- Multiplicative modifiers without a declared stacking model.
- Rule text that requires semantic interpretation during deterministic resolution.
- A generated ability silently entering the world because its prose sounded plausible.
- Balance defined as making every build produce the same damage.

## Product risks exposed by the research

### AI trust and continuity

LLMs can produce engaging RPG fiction while struggling with verifiable mechanics over longer or more complex
scenarios. RPGBench therefore argues for separating structured game state and mechanics from narration
([RPGBench](https://arxiv.org/abs/2502.00595)). Long-story research independently finds recurring factual and
temporal consistency errors; this is adjacent narrative-generation evidence rather than direct game-player evidence
([ConStory-Bench](https://arxiv.org/abs/2603.05890)).

The product response is an authoritative state model, exact action and resolution traces, explicit provenance,
versioned content, and a continuity checker. It is not a larger prompt. NPC testimony must also remain distinct
from canonical truth: a character may lie or be mistaken without the engine losing track of reality.

### Generated-content sludge

This is a product-risk hypothesis, supported directionally by RPGBench's declining interestingness over longer
runs. An effectively infinite catalog can become less valuable than a small authored one if entries differ only in
name, rarity, or percentage. Generated content needs novelty checks at the mechanical and thematic levels, quotas,
counter availability, encounter relevance, and a curation threshold. The default reward rate should favor sparse,
memorable affordances over constant drops.

### Complexity and external-tool dependence

Path of Exile, Lancer, Noita, and Magic all show that a coherent kernel can still become difficult through
combinatorial scale. SemMachina should provide the preview, validator, local interaction graph, glossary, test
fixtures, and causal trace itself. A community planner may be useful; needing one to avoid invalid ordinary choices
violates the proposed product principle of progressive disclosure.

### Dominant builds and compatibility debt

Runtime invention creates a growing compatibility surface. Every ability version needs a legality scope and
migration policy. New content must be tested against an encounter portfolio and known interaction corpus. The goal
is viable strategic diversity and situational strengths, not identical throughput or permanent compatibility with
every future world version.

### Authorship and provenance

The interface should state which parts are core-authored, authored in the world template, player-composed,
model-proposed, and mechanically validated. “AI-generated” is not a sufficient provenance category. Players need
to know who chose the purpose, which physics version accepted it, and whether a human or player explicitly approved
it.
CHI research found that participants' default trust and perceived intentional meaning could fall when they knew an
LLM produced game content, which makes this a player-evidence question rather than a messaging exercise
([Lies, Deceit, and Hallucinations](https://www.mikeyin.xyz/files/chi2024.pdf)).

## Recommended magic kernel

The research does not support choosing one of the candidate kernels in isolation. Each solves a different product
problem.

### Semantic authoring: Technique + Domain

Techniques describe what magic attempts to do: create, destroy, transform, move, bind, reveal, protect, summon,
exchange, or perceive. Domains describe what it acts upon: flame, force, flesh, mind, shadow, distance, time,
identity, and so on.

This layer supports character fantasy, research, teaching, and LLM interpretation. It is not executable by itself.

### Deterministic execution: Trigger + Cost + Target + Delivery + Effect + Modifier + Duration + Limits

Every installed ability compiles to a closed structure:

```text
ability
  identity + version + provenance
  technique + domain + tradition permissions
  activation window + action cost
  resource costs
  preconditions
  target query + range + area or scale
  potency + duration
  ordered effects
  modifiers
  outcome bands
  limits + cooldown
  stacking + overreach
  tags + counters
  world-law requirements
  presentation references
  validation + simulation evidence references
```

Effects are bounded operations such as damage, restore, move, add condition, remove condition, reserve resource,
spawn an entity from an approved template, reveal a fact, or transform a material state. A modifier may change an
approved parameter or wrap an effect using a declared composition point; it is not arbitrary executable code.

Traditions should also declare hard capability boundaries—the setting equivalent of Ars Magica's limits or
Magic's color identity. A domain that can do everything erases both build identity and counterplay. Breaking a
cosmological boundary should require a named, expensive world event or new physics version, not a clever prompt.

### World ecology: materials and conditions

Conditions are graph facts with explicit producers, consumers, stacking, expiry, and counters. Examples include
`Heated`, `Wet`, `Brittle`, `Anchored`, `Marked`, `Silenced`, and `Obscured`. Interactions belong to the world's
versioned physics, not individual LLM rulings.

This layer produces discovery. A player learns not merely that a spell deals five damage, but that heat propagates,
wetness changes it, brittle objects respond differently to force, and a newly invented spell participates in those
same laws.

## Starting experience

Do not begin with a blank spell-design prompt. At character creation, the player should choose:

- one magical tradition or theory;
- one primary resource loop;
- one focus item;
- three to five equipped starter abilities;
- one meaningful limitation or vulnerability; and
- one initial research direction.

Each starter tradition should demonstrate offense, defense, movement, control, utility, and resource recovery. The
initial choices should be prebuilt and previewable, with optional customization after the player has seen at least
one resolved example.

The interface should disclose abilities in layers:

1. **fantasy:** name, image, and one-sentence promise;
2. **card:** exact cost, timing, target, effect, and important interactions;
3. **trace:** ordered resolution, applied modifiers, rolls, and state changes; and
4. **laboratory:** editable composition graph, compatibility explanations, and test fixtures.

## Campaign modes

### Fixed Canon

All abilities and items are authored before play. This is the most auditable mode and the appropriate baseline for
competitive or tightly balanced scenarios.

### Seeded Discovery — recommended default

The world's physics and latent catalog are fixed at world creation. A seed may drive generation, but reproducibility
cannot depend on rerunning an LLM: the accepted catalog is snapshotted and content-addressed alongside its generator,
model, prompt, decoding, physics, and validator versions. Players do not know the catalog, but replay, sharing, and
continuity use the accepted artifact hash rather than regeneration.

### Living Magic

Players and NPC entities carrying an explicit invention permission may propose abilities during play. A proposal
becomes authoritative only after validation, compatibility analysis, simulation, acceptance, and scheduled
installation. This is an explicit campaign capability, not an invisible default.

## Runtime invention lifecycle

Treat spell research as a lifecycle-managed world entity:

```text
proposed -> interpreted -> validated -> compatible -> simulated -> offered
                    |              |             |           |         |
                    +------------> revised <------+           |      accepted
                    +------------> rejected <-----------------+         |
                                                                    scheduled
                                                                       |
                                                                    installed
```

1. The player states an intention and research method.
2. An agentic designer—a persona limited to interpreting the proposal—translates fiction into a structured
   `AbilityDraft` and explanation.
3. A validator checks whether the individual draft is legal: vocabulary, capability, target scope, costs,
   magnitude, composition, and hard bounds.
4. Compatibility analysis checks the draft against the known catalog and world-law interaction corpus.
5. A deterministic simulator probes representative fixtures and abuse cases. Passing provides evidence, not proof
   of safety.
6. The system presents the exact card, assumptions, projected tradeoffs, and required in-world cost.
7. The player accepts, revises, rejects, or lets the proposal expire.
8. An accepted draft is scheduled for the next safe activation boundary, then installed as a versioned ability
   entity whose `AbilitySpec` is consumed by the fixed resolver.
9. The narrator reveals the result in fiction; narration cannot alter the installed definition.

The lifecycle follows SemMachina's house boundary:

- **persona:** interprets the proposal and emits a structured draft;
- **components:** caller-agnostic validator, compatibility analyzer, simulator, compiler, resolver, and prose store
  each perform one unit of work;
- **rule:** reacts to closed lifecycle and world facts with bounded actions;
- **lifecycle:** owns research phase and operator-visible progress; and
- **knowledge graph:** owns installed ability, item, character, and world-condition facts.

Drafts, simulation reports, and compiler artifacts are operational data in component-owned KV or ObjectStore with
reference triples. The lifecycle entity owns only phase, status, ownership, and artifact references. Installed
ability facts enter `ENTITY_STATES` through `graph-ingest`, which remains the sole graph writer.

## Runtime ability and rule safety boundary

SemStreams provides rule primitives, but safe runtime ability or rule installation is unproven and unauthorized.
Current SemMachina experience packs are selected during boot. The present authoring contract explicitly prevents
packages from replacing turn stages, publishing arbitrary work, dispatching personas, writing arbitrary KV, or
touching protected state. Living Magic therefore requires a future product contract; it is not already delivered
by pack selection.

The safest default is to install a closed declarative `AbilitySpec` interpreted by a fixed, versioned resolver.
Runtime invention may select approved operations, parameters, and composition points; it may not register new
evaluator logic. Only genuinely reactive portions—such as `on_hit`, expiry, or threshold reactions—might justify
future dynamic SemStreams rules, and those rules would trigger bounded component work rather than execute the
ability's ordered effects.

The future contract should require:

- install only declarative `AbilitySpec` data for the fixed resolver, never model-generated executable code;
- host policy intersected with world policy and player permission;
- literal, namespaced predicates and bounded target queries;
- declared maximum targets, spawns, nested triggers, event emissions, effect operations, random draws, and
  activations per event, turn, and encounter;
- cycle detection plus a hard runtime exhaust action;
- deterministic random-draw order plus instruction and wall-clock exhaust guards;
- deterministic priority and conflict resolution;
- content-addressed versions and no in-place semantic mutation;
- rollback by disabling a version, while retaining ledger history;
- simulation against canonical fixtures before activation;
- an operator-readable explanation of requested capabilities;
- installation between turns, scenes, or downtime phases rather than during action resolution; and
- separate authorization before enabling any dynamic rule-definition installation beyond `AbilitySpec` data.

The current package boundary is documented in the
[world-authoring guide](../guides/world-authoring.md#package-rule-authority). The implementation plan must wait for
the SemStreams refactor and revalidate every assumed rule-management contract afterward.

## Items and progression

Items should be ability participants rather than bags of unrelated percentages. A strong item normally provides:

- a clear fictional identity;
- one build-changing affordance;
- one understandable cost or constraint;
- a small number of supporting properties; and
- a discoverable relationship to a tradition, condition, enemy, location, or historical event.

Recommended item roles include focus, catalyst, reservoir, converter, trigger source, modifier host, counter,
research specimen, and proof of mastery. A legendary item may alter one normal rule, but it must do so through a
named, testable exception with explicit timing.

Build-defining identity should live in a named affordance. Random generation may tune bounded supporting
properties, but it should not randomly assemble an item's semantic purpose from unrelated fragments.

Progression should cycle through:

```text
challenge -> evidence -> choice -> acquisition or invention -> laboratory -> field proof -> consolidation
```

Power should grow through new relationships and affordances more often than through raw multiplication. An upgrade
is interesting when it changes what a player considers possible.

## Initial product plan

### Phase 0: research closure during the code hold

- Review this brief against the post-refactor SemStreams contracts when they settle.
- Select a target player segment and campaign mode for the first prototype.
- Paper-prototype three traditions over the recommended shared kernel and resolve example turns without software.
- Conduct structured interviews with system-heavy RPG, ARPG, and LitRPG players.
- Test whether ability cards and resolution traces make outcomes predictable.

### Phase 1: paper and data prototype

- Define 8–12 Techniques, 8–12 Domains, 12–20 executable effects, and 6–10 world conditions.
- Author three starter traditions and enough abilities to cover the core tactical roles.
- Define exact timing, targeting, stacking, expiry, and reaction rules.
- Build a spreadsheet or static harness that resolves canonical examples without an LLM.
- Create abuse cases before adding generative content.

### Phase 2: deterministic resolver contract

- Specify `AbilitySpec`, `ActionPlan`, `ResolutionTrace`, and version/provenance contracts through OpenSpec.
- Prove exact mechanical replay from the persisted catalog hash and random seed, plus idempotent effect application.
- Add property tests for caps, cycles, invalid targets, resource conservation, and stacking.
- Keep all abilities authored; do not introduce an LLM compiler yet.

### Phase 3: laboratory and authored composition

- Provide composition, compatibility, preview, and deterministic simulation tools.
- Measure whether players can predict outcomes and diagnose invalid builds without external guides.
- Add safe respec and versioned build snapshots.

### Phase 4: seeded catalog and content evaluation

- Generate a latent catalog at world creation, validate it, and content-address the result.
- Evaluate thematic cohesion, mechanical distinctness, counter availability, and starter relevance.
- Compare against a Fixed Canon control to test whether generation adds player value.
- Add discovery clues, compendium progression, and build sharing.

### Phase 5: bounded live invention

- Add `AbilityDraft` generation behind the same validator used by human-authored content.
- Require explicit player acceptance, in-world research cost, and scheduled safe-boundary activation.
- Track rejection classes, revisions, exploit attempts, similarity, and player retention of generated abilities.
- Keep a non-generative fallback for every workflow.

## Prototype quality gates

These are initial directional gates, not market validation. Phase 0 should preregister the exact tasks and fixtures
before testing begins.

### Deterministic resolver gate

- Reproduce exact mechanical resolution and state deltas from world version, catalog hash, ability versions, frozen
  `ActionPlan`, and random seed. Narration is replayed from its stored artifact, not regenerated byte-for-byte.
- Reject every invalid authored fixture without partial state mutation.
- Produce no unbounded trigger chain across fuzz and adversarial fixtures.
- Explain every applied cost, modifier, target, random draw, and state delta without prose interpretation.

Failure blocks the laboratory and all generated-content work.

### Laboratory usability gate

Recruit at least 12 participants matching the candidate system-explorer/buildcrafter segment. After one guided
example:

- at least 10 of 12 complete a valid starter build within 10 minutes without opening external documentation;
- at least 10 of 12 correctly predict four of five scripted interaction outcomes using the card and trace;
- at least 9 of 12 diagnose and repair an invalid composition within two attempts; and
- every irreversible action has a preview and a reversible draft simulation.

Missing a threshold returns the design to presentation or grammar work before catalog generation.

### Seeded Discovery gate

Run a blinded, counterbalanced comparison with at least 12 candidate-segment participants using mechanically
matched Fixed Canon and Seeded Discovery catalogs. Proceed only if:

- at least 8 of 12 prefer the Seeded Discovery session for continued play;
- at least 9 of 12 identify three mechanically distinct discoveries correctly; and
- generated-catalog review finds no invalid entry, missing counter class, or unexplained duplicate purpose.

Failure keeps Fixed Canon as the default and returns generation to content-quality work.

### Living Magic gate

- Reject every invalid generated draft without partial installation.
- Activate no accepted draft before its scheduled safe boundary.
- Produce no unbounded or over-budget execution across the known compatibility corpus and adversarial fixtures.
- At least 9 of 12 candidate-segment participants create a valid intended ability within two revision cycles and
  can explain its main cost and counter.

Failure leaves Living Magic disabled while preserving the validated authored and seeded modes.

Balance evaluation should use encounter portfolios and viable tradeoffs, not equal damage. A utility or control
build may be balanced by solving different problems, but every supported build needs declared strengths, counters,
and failure states.

## Research questions still requiring player evidence

### Strategic gates

1. **Discovery or invention:** Is the candidate audience more interested in discovering a hidden catalog or
   inventing new abilities? The Program Manager owns the recommendation; the product owner authorizes the choice.
   Test through the Phase 4 Fixed Canon/Seeded comparison followed by a paper Living Magic task. The result selects
   the default campaign mode and whether Phase 5 belongs on the initial product path.
2. **AI trust and provenance:** Does revealing model involvement reduce trust, or can explicit provenance and
   player approval create attachment? The Program Manager owns the research design. Test the same accepted ability
   with blinded, disclosed, and player-co-created provenance treatments. The result determines disclosure UX and
   whether model-generated content belongs in the launch experience.

### Prototype design tests

1. How much exact arithmetic belongs on the normal ability card?
2. Should the default action economy use two or three actions?
3. Do players prefer one shared magical resource or tradition-specific resource loops?
4. Should discovered interactions be globally documented, character-known, or player-private?
5. Which commitments should be reversible, and which make a build feel owned?
6. Is a safe laboratory sufficient for experimentation, or do players require a full build planner?

These questions use observed task completion, prediction, correction, and choice behavior in Phases 1–3 rather than
interview preference alone.

### Longitudinal questions

1. When does generated content feel personally meaningful rather than disposable across repeated sessions?
2. How much imbalance is delightful in solo play before it removes meaningful choice over time?

These cannot be closed by a first-session prototype and should not block the deterministic resolver.

## Provisional constraints and hypotheses

The recommendation is **not** to design the final ability ontology during the code hold.

Provisional architectural constraints are:

- magical physics precede play;
- the starter experience is curated;
- learned mechanics are inspectable;
- invention compiles to bounded deterministic structure;
- authoritative effects never depend on narration.

Product hypotheses requiring player evidence are:

- Seeded Discovery should be the default campaign mode;
- the candidate system-explorer/buildcrafter/fictional-experimenter overlap is reachable; and
- bounded model-assisted invention creates more value than a larger curated catalog.

Field names, payload schemas, rule installation APIs, and SemStreams integration remain deliberately open until the
upstream refactor settles and an OpenSpec change is authorized.

## Evidence inventory

Research was conducted on 2026-08-05. Links adjacent to claims are the reproducible source trail.

### Primary or publisher sources

- tabletop: Ars Magica, Mage: The Awakening, Pathfinder 2e, D&D 2024, Lancer, Blades in the Dark,
  Apocalypse World, GURPS, and HERO;
- digital: Path of Exile, Noita, Diablo, Last Epoch, Grim Dawn, Magic: The Gathering, Magicka, and Baba Is You; and
- SemMachina: the current founding proposal, project contract, orchestration skill, and world-authoring guide.

These sources establish mechanics and stated design intent. They do not establish player preference or product fit.

### Player and research evidence

- motivation and curiosity: PENS, Quantic Foundry's proprietary industry taxonomy, and curiosity-in-games
  research;
- LLM reliability and trust: RPGBench, ConStory-Bench, and the cited CHI trust study; and
- directional critique: RPGnet reviews, developer postmortems, official forums, and public community discussions
  covering D&D 4e, complexity, adjudication, build commitment, opacity, generated content, and LitRPG progression.

The community corpus was exploratory rather than systematic. Search themes included spell construction, action
economy, buildcraft motivation, system complexity, trap choices, LLM GM consistency, AI provenance, LitRPG stats,
and progression appeal. No claim in this brief should be read as a population estimate.
