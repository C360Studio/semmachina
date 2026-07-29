package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/campaign"
	"github.com/c360studio/semmachina/internal/content"
	"github.com/c360studio/semmachina/internal/dice"
	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

// Sentinels for the three outcomes a caller genuinely branches on.
var (
	// ErrManifestNotFound reports a turn the archive has no record of.
	ErrManifestNotFound = errors.New("no ledger manifest for this turn")
	// ErrNotReproducible reports a roll whose recorded outcome is not the one
	// its own inputs produce.
	//
	// It is the failure this whole package exists to be able to detect. Seeded
	// determinism is a claim, and a claim nothing can falsify is decoration —
	// so replay does not "reproduce" a roll by reading it back and agreeing with
	// itself; it re-executes and compares, and this is what a disagreement is
	// called.
	ErrNotReproducible = errors.New("the archived roll is not reproducible from its own inputs")
	// ErrNotReplayable reports a manifest whose deterministic half cannot be
	// re-executed at all — a mechanic or generator this engine no longer holds.
	ErrNotReplayable = errors.New("the archived turn cannot be re-executed by this engine")
)

// ArtifactReader resolves the references a manifest carries.
//
// Every method is a READ. There is no seam here through which a reader could
// produce an artifact rather than fetch one, which is the type-level half of
// replay honesty: a narration comes back from the store or it does not come
// back.
type ArtifactReader interface {
	GetVerdict(ctx context.Context, ref content.Ref) (*payload.Verdict, error)
	GetRoll(ctx context.Context, ref content.Ref) (*payload.RollResult, error)
	GetNarration(ctx context.Context, ref content.Ref) (*content.Narration, error)
}

// SeedSource supplies the campaign seed a roll re-derives from, and the campaign
// it belongs to.
//
// The seed is read from the CAMPAIGN ENTITY rather than carried on the roll
// record, which is what the roll's SeedSource field is for: it records the
// derivation INPUTS (campaign id, turn id) and never the seed material, so a
// ledger can be handed to a reader without handing over the ability to
// pre-compute every future roll.
type SeedSource interface {
	// CampaignID is the campaign this source holds the seed for.
	CampaignID() string
	// LoadSeed reads the stored campaign seed.
	LoadSeed(ctx context.Context) (campaign.Seed, error)
}

// The claims above, enforced by the compiler rather than by doc comments.
var (
	_ ArtifactReader = (*content.Store)(nil)
	_ SeedSource     = (*campaign.Gate)(nil)
)

// Reader reads archived turns back and replays their deterministic half.
type Reader struct {
	streams   StreamEnsurer
	decoder   *message.Decoder
	artifacts ArtifactReader
	seeds     SeedSource

	// mu guards the lazily-opened stream handle. A reader is a read surface and
	// nothing about it says single-threaded — the writer loop, the chronicler
	// and an export are three plausible concurrent callers — so the one piece of
	// mutable state it has is locked rather than left to whoever reads this next.
	mu     sync.Mutex
	stream archive
}

// NewReader builds the campaign ledger's replay reader.
func NewReader(
	streams StreamEnsurer,
	decoder *message.Decoder,
	artifacts ArtifactReader,
	seeds SeedSource,
) (*Reader, error) {
	switch {
	case streams == nil:
		return nil, errors.New("the ledger reader requires a stream creator")
	case decoder == nil:
		return nil, errors.New("the ledger reader requires a payload decoder")
	case artifacts == nil:
		return nil, errors.New("the ledger reader requires an artifact reader")
	case seeds == nil:
		return nil, errors.New(
			"the ledger reader requires a campaign seed source; without the seed a roll cannot be re-executed, " +
				"and a replay that skipped the dice would be reading a record rather than reproducing it")
	}
	return &Reader{streams: streams, decoder: decoder, artifacts: artifacts, seeds: seeds}, nil
}

// Manifest reads one turn's archived manifest.
//
// A turn the archive has no record of is ErrManifestNotFound, never a zero
// manifest: "this campaign never resolved that turn" and "here is an empty turn"
// must not be the same value at a call site deciding what happened.
func (r *Reader) Manifest(ctx context.Context, turnID string) (*payload.TurnManifest, error) {
	subject, err := SubjectFor(turnID)
	if err != nil {
		return nil, err
	}
	stream, err := r.archive(ctx)
	if err != nil {
		return nil, err
	}
	return readManifest(ctx, stream, r.decoder, subject)
}

func (r *Reader) archive(ctx context.Context) (archive, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stream != nil {
		return r.stream, nil
	}
	stream, err := r.streams.EnsureStream(ctx, StreamConfig())
	if err != nil {
		return nil, fmt.Errorf("open the %s stream: %w", Stream, err)
	}
	r.stream = stream
	return stream, nil
}

// Replay is one archived turn, re-executed where that is honest and read back
// where it is not.
//
// The split is the point, and the field names say which is which. Verdict,
// Roll and Narration were READ from the references the turn preserved.
// Reproduced was RE-EXECUTED from those artifacts' own inputs. Nothing here was
// regenerated: a persona re-run would be a new rendition, and a rendition
// presented as a replay is the archive lying about its own history.
type Replay struct {
	// Manifest is the archived record this replay is of.
	Manifest *payload.TurnManifest
	// Verdict is the adjudicator's judgment as it was recorded.
	Verdict *payload.Verdict
	// Roll is the resolution as it was recorded; nil for a turn that never
	// reached the dice.
	Roll *payload.RollResult
	// Reproduced is the roll RE-EXECUTED from the campaign seed, the turn id,
	// and the verdict's modifiers; nil when Roll is.
	//
	// It is byte-identical to Roll or Replay returned an error. Carrying it
	// anyway lets a caller show the comparison rather than take it on trust.
	Reproduced *payload.RollResult
	// Narration is the prose the narrator committed, READ from its reference;
	// nil for a turn that was never narrated.
	Narration *content.Narration
}

// Rolled reports whether this turn reached the dice.
func (r Replay) Rolled() bool { return r.Roll != nil }

// Replay re-executes an archived turn's deterministic half and reads the rest.
//
// What is re-executed: the dice, and only the dice. The roll is re-derived from
// the campaign seed and the turn id — the two inputs the record names — with the
// modifiers taken from the VERDICT rather than from the roll's own copy of them.
// That last choice is what makes the check non-circular: re-deriving a record
// from the inputs it stored about itself proves the record is self-consistent
// and nothing else, while re-deriving it from the artifact upstream proves the
// chain still produces the outcome the campaign committed to.
//
// What is read: everything a persona authored. The verdict and the narration
// come back from the references the turn preserved, and there is no branch here
// that produces either — the engine's persona machinery is not reachable from
// this package at all, which a source scan and a dependency-tree check hold it
// to.
//
// The versions are checked before anything is re-executed. A record naming a
// mechanic or a generator this engine no longer implements is ErrNotReplayable
// rather than a reproduction attempt, because re-executing under a substituted
// version would produce dice that look like agreement and are not — which is
// exactly the failure both fields were recorded to prevent.
func (r *Reader) Replay(ctx context.Context, manifest *payload.TurnManifest) (*Replay, error) {
	if manifest == nil {
		return nil, errors.New("replay requires a manifest")
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("refusing to replay an incoherent manifest: %w", err)
	}
	if manifest.CampaignID != r.seeds.CampaignID() {
		return nil, fmt.Errorf(
			"manifest for turn %s belongs to campaign %s, but this reader holds campaign %s; replaying it here "+
				"would roll another campaign's turn under this campaign's seed and report the mismatch as a "+
				"determinism failure", manifest.TurnID, manifest.CampaignID, r.seeds.CampaignID())
	}

	replay := &Replay{Manifest: manifest}

	verdict, err := r.verdict(ctx, manifest)
	if err != nil {
		return nil, err
	}
	replay.Verdict = verdict

	if err := r.replayRoll(ctx, manifest, verdict, replay); err != nil {
		return nil, err
	}
	if err := r.readNarration(ctx, manifest, replay); err != nil {
		return nil, err
	}
	return replay, nil
}

// verdict reads the adjudicator's judgment back off its reference.
//
// A manifest with no verdict reference is a turn that failed before
// adjudication, which is a legitimate archived turn with nothing to replay.
func (r *Reader) verdict(ctx context.Context, manifest *payload.TurnManifest) (*payload.Verdict, error) {
	if manifest.VerdictRef == "" {
		return nil, nil
	}
	ref, err := content.ParseRef(manifest.VerdictRef)
	if err != nil {
		return nil, fmt.Errorf("turn %s records an unresolvable verdict reference: %w", manifest.TurnID, err)
	}
	verdict, err := r.artifacts.GetVerdict(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve the archived verdict for turn %s: %w", manifest.TurnID, err)
	}
	if verdict.TurnID != manifest.TurnID {
		return nil, fmt.Errorf(
			"the verdict at %s belongs to turn %s, not %s; the manifest's reference points at another turn's "+
				"judgment", ref, verdict.TurnID, manifest.TurnID)
	}
	return verdict, nil
}

// replayRoll re-executes the dice and compares byte for byte.
func (r *Reader) replayRoll(
	ctx context.Context,
	manifest *payload.TurnManifest,
	verdict *payload.Verdict,
	into *Replay,
) error {
	if manifest.RollRef == "" {
		// A no-roll turn, or one that failed before the dice. Both are honest
		// archived turns; there is simply nothing deterministic to re-execute.
		// The coherence of "no roll" against the verdict's own reported gate is
		// checked below so a MISSING roll cannot pass as a declined one.
		if verdict != nil && verdict.Scalars.RequiresRoll {
			return fmt.Errorf(
				"turn %s archived a verdict that called for the dice and no roll reference; the roll it was "+
					"resolved by is not in the archive", manifest.TurnID)
		}
		return nil
	}
	if verdict == nil {
		return fmt.Errorf(
			"turn %s archived a roll and no verdict; the roll's modifiers come from the verdict, so it cannot "+
				"be re-executed from the archive alone", manifest.TurnID)
	}
	if !verdict.Scalars.RequiresRoll {
		return fmt.Errorf(
			"turn %s archived a roll under a verdict that declined the dice; the engine never rolls a turn the "+
				"adjudicator did not send to the dice", manifest.TurnID)
	}

	ref, err := content.ParseRef(manifest.RollRef)
	if err != nil {
		return fmt.Errorf("turn %s records an unresolvable roll reference: %w", manifest.TurnID, err)
	}
	recorded, err := r.artifacts.GetRoll(ctx, ref)
	if err != nil {
		return fmt.Errorf("resolve the archived roll for turn %s: %w", manifest.TurnID, err)
	}
	if recorded.TurnID != manifest.TurnID {
		return fmt.Errorf(
			"the roll at %s belongs to turn %s, not %s", ref, recorded.TurnID, manifest.TurnID)
	}
	if recorded.Seed.CampaignID != manifest.CampaignID {
		return fmt.Errorf(
			"the roll for turn %s was derived under campaign %s and the manifest archives campaign %s; the seed "+
				"inputs and the record disagree about which campaign this turn belongs to",
			manifest.TurnID, recorded.Seed.CampaignID, manifest.CampaignID)
	}
	if recorded.RNGVersion != dice.RNGVersion {
		return fmt.Errorf(
			"%w: turn %s was rolled under generator %s and this engine rolls %s. Re-executing under a different "+
				"generator produces different dice from the same seed, and the mismatch would look like a "+
				"determinism failure rather than a version one",
			ErrNotReplayable, manifest.TurnID, recorded.RNGVersion, dice.RNGVersion)
	}

	roller, err := dice.NewRoller(recorded.Mechanic)
	if err != nil {
		return fmt.Errorf(
			"%w: turn %s was rolled under mechanic %s: %w", ErrNotReplayable, manifest.TurnID, recorded.Mechanic, err)
	}
	seed, err := r.seeds.LoadSeed(ctx)
	if err != nil {
		return fmt.Errorf("load the campaign seed to replay turn %s: %w", manifest.TurnID, err)
	}

	// The modifiers come from the VERDICT, not from the roll's own record of
	// them. Taking them from the roll would make this a self-consistency check;
	// taking them from the artifact the dice were handed makes it a check that
	// the chain still produces what the campaign committed to.
	reproduced, err := roller.Roll(seed, recorded.Seed.CampaignID, manifest.TurnID, verdict.Modifiers)
	if err != nil {
		return fmt.Errorf("re-execute the roll for turn %s: %w", manifest.TurnID, err)
	}

	if err := identicalRolls(recorded, reproduced); err != nil {
		return fmt.Errorf("%w: turn %s: %w", ErrNotReproducible, manifest.TurnID, err)
	}
	into.Roll = recorded
	into.Reproduced = reproduced
	return nil
}

// identicalRolls compares an archived roll with its re-execution, byte for
// byte.
//
// Canonical payload JSON rather than a field-by-field comparison, so a field
// added to RollResult is covered the day it is added rather than the day
// somebody remembers to extend this. Byte equality is also the strongest form
// the claim comes in: "the same dice" would pass while the mechanic version,
// the modifier list or the band had silently changed underneath.
func identicalRolls(recorded, reproduced *payload.RollResult) error {
	before, err := json.Marshal(recorded)
	if err != nil {
		return fmt.Errorf("the archived roll could not be re-encoded: %w", err)
	}
	after, err := json.Marshal(reproduced)
	if err != nil {
		return fmt.Errorf("the re-executed roll could not be encoded: %w", err)
	}
	if bytes.Equal(before, after) {
		return nil
	}
	return fmt.Errorf("archived %s, re-executing its inputs now produces %s", before, after)
}

// readNarration reads the turn's prose back off its reference.
//
// READ, never regenerated, and that is the whole of 8.3's honesty claim at the
// one place it could be broken. Re-running the narrator would produce prose from
// the same world state and a different sampling, and calling that a replay would
// make the archive's account of a campaign depend on when it was asked. A
// completed turn's narration reference is required by the manifest's own
// contract, so an absent one here means the turn was never narrated.
func (r *Reader) readNarration(ctx context.Context, manifest *payload.TurnManifest, into *Replay) error {
	if manifest.NarrationRef == "" {
		return nil
	}
	ref, err := content.ParseRef(manifest.NarrationRef)
	if err != nil {
		return fmt.Errorf("turn %s records an unresolvable narration reference: %w", manifest.TurnID, err)
	}
	narration, err := r.artifacts.GetNarration(ctx, ref)
	if err != nil {
		return fmt.Errorf("resolve the archived narration for turn %s: %w", manifest.TurnID, err)
	}
	if narration.TurnID != manifest.TurnID {
		return fmt.Errorf(
			"the narration at %s belongs to turn %s, not %s; the manifest's reference points at another turn's "+
				"prose", ref, narration.TurnID, manifest.TurnID)
	}
	if err := checkBand(manifest, into, narration); err != nil {
		return err
	}
	into.Narration = narration
	return nil
}

// checkBand holds the archived narration to the outcome the dice actually
// selected.
//
// The narrator voices a committed outcome, so a narration filed against a band
// the turn did not land on is a story about a turn that did not happen — which
// is the drift this engine exists to detect, and detecting it in the archive is
// where the writer loop would otherwise inherit it silently.
func checkBand(manifest *payload.TurnManifest, replay *Replay, narration *content.Narration) error {
	want := vocabulary.BandAuto
	if replay.Roll != nil {
		want = replay.Roll.Band
	}
	if narration.Band != want {
		return fmt.Errorf(
			"turn %s archived a narration voicing the %q band while its resolution landed on %q",
			manifest.TurnID, narration.Band, want)
	}
	return nil
}
