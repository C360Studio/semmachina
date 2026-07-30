package campaign

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// MarkerSource is the Source stamped on the import-completion triple.
//
// Its own source rather than InstantiationSource, because the two facts are
// written at different moments by different steps and an operator reading the
// campaign entity should be able to tell "the claim" from "the import that
// followed it" without inferring it from a timestamp.
const MarkerSource = "world-import"

// Marker polling defaults for a boot that finds a campaign nobody has marked.
const (
	// DefaultMarkerWait bounds how long a boot waits for another claimant's
	// import to finish before it fails loudly.
	//
	// Generous, because the thing being waited for is a whole world import
	// against a real broker, and impatient here means refusing to boot a
	// deployment that was seconds from being ready. Bounded, because the other
	// shape this state has is a claimant that CRASHED mid-import, and waiting
	// forever on that is a process that never says what is wrong.
	DefaultMarkerWait = 2 * time.Minute
	// DefaultMarkerPoll is how often the wait re-reads the campaign entity.
	DefaultMarkerPoll = 250 * time.Millisecond
)

// ErrImportInterrupted reports a campaign that was claimed and never marked.
//
// It is its own sentinel because it is the one instantiation outcome a boot
// cannot resolve on its own. Importing would be wrong (this process is not the
// claimant, and the real importer may be mid-flight — a late loser writing the
// marker manufactures exactly the marked-but-partial world the marker exists to
// prevent); skipping would be worse (serving play from half a world). So it
// ends the boot with something an operator can act on.
var ErrImportInterrupted = errors.New("the campaign was claimed but its world import never completed")

// MarkImported records that the import which followed a FRESH claim finished.
//
// # It takes the claim, and refuses one that is not Fresh
//
// That is the mechanism behind "only the claimant that observed Fresh imports",
// which is otherwise a rule somebody has to remember. A boot that lost the claim
// sees a campaign it did not create; if it could mark that campaign it could
// declare somebody else's in-flight import finished, and the marker would then
// certify a world it never looked at. The claim is proof of which side of that
// race the caller is on, so it is an argument rather than a comment.
//
// The claim's campaign is checked against this gate's as well, because two
// gates in one process is a wiring shape nothing else forbids and a marker
// written onto the wrong campaign is a world declared ready by a boot that
// imported a different one.
//
// # The merge lane, and what it protects
//
// The campaign entity carries the seed. The appending lane would leave a
// re-marked campaign holding two completion instants — both true, no error — and
// would leave a reader unable to say which import it is being told about; the
// merge lane replaces by (subject, predicate) and leaves campaign.seed.value
// exactly where it was. A seed rewritten at boot is every recorded roll made
// unreproducible, so this is the lane choice with the sharpest consequence in
// the package.
func (g *Gate) MarkImported(ctx context.Context, claim Instantiation) (time.Time, error) {
	if !claim.claimed {
		return time.Time{}, fmt.Errorf(
			"refusing to mark campaign %s imported from an Instantiation that did not come out of Claim; the "+
				"argument is PROOF that this boot won the atomic create, and a hand-built value proves nothing "+
				"about who imported the world", g.campaignID)
	}
	if !claim.Fresh {
		return time.Time{}, fmt.Errorf(
			"refusing to mark campaign %s imported from a claim that did not create it; the marker says an "+
				"import FINISHED, and only the boot whose claim came back fresh ran one — a claimant that lost "+
				"could otherwise certify an import still in flight, which is precisely the marked-but-partial "+
				"world this marker exists to prevent", g.campaignID)
	}
	if claim.CampaignID != g.campaignID {
		return time.Time{}, fmt.Errorf(
			"refusing to mark campaign %s imported from a claim on %s; the marker would declare a world ready "+
				"that this boot never imported", g.campaignID, claim.CampaignID)
	}

	at := g.now().UTC()
	triple := message.Triple{
		Subject:   g.campaignID,
		Predicate: vocabulary.CampaignImportCompleted.String(),
		Object:    at.Format(time.RFC3339Nano),
		Source:    MarkerSource,
		Timestamp: at,
		// Recorded, never inferred: this boot published the world and read it
		// back before writing this.
		Confidence: 1.0,
		Context:    g.campaignID,
	}
	if _, err := g.store.MergeTriples(ctx, g.campaignID, []message.Triple{triple}); err != nil {
		return time.Time{}, fmt.Errorf(
			"record the import-completion marker on campaign %s: %w; the world is imported and unmarked, so the "+
				"next boot will refuse to serve it rather than guess", g.campaignID, err)
	}
	return at, nil
}

// ImportCompletion reports when this campaign's world import finished.
//
// The bool is false for a campaign that exists and carries no marker, which is
// the interrupted case — a distinct answer from "no campaign", which is
// ErrEntityNotFound from the read, and from "the marker is unreadable", which is
// an error. Collapsing any two of those would let a boot import over a living
// campaign or serve play from half a world.
func (g *Gate) ImportCompletion(ctx context.Context) (time.Time, bool, error) {
	state, err := g.store.GetEntity(ctx, g.campaignID)
	if err != nil {
		return time.Time{}, false, err
	}
	return completionFromEntity(state, g.campaignID)
}

// AwaitImportCompletion waits a BOUNDED time for another claimant's import to
// finish, and fails loudly when it does not.
//
// This is the third of the three answers the marker has to give, and the one
// that is easy to get wrong in the expensive direction. A boot that finds a
// campaign nobody has marked is either racing a live importer — in which case
// the right move is to wait, because the world is about to be whole — or looking
// at the wreckage of one that crashed. Nothing distinguishes them from here, and
// the two wrong resolutions are both silent: importing would re-run a template
// over a world another process is mid-way through writing, and marking would
// certify a world this boot never read.
//
// So it waits, and then it stops. An operator with a crashed import has a
// campaign entity to delete and a boot to re-run; an operator with a boot that
// hung has nothing at all.
func (g *Gate) AwaitImportCompletion(ctx context.Context, wait, poll time.Duration) (time.Time, error) {
	if wait <= 0 || poll <= 0 {
		return time.Time{}, errors.New("waiting for the import marker requires a positive bound and interval")
	}
	deadline := g.now().Add(wait)
	for {
		at, marked, err := g.ImportCompletion(ctx)
		if err != nil {
			return time.Time{}, err
		}
		if marked {
			return at, nil
		}
		if !g.now().Before(deadline) {
			return time.Time{}, fmt.Errorf(
				"%w: campaign %s has existed for the whole %s this boot waited, with no %s. Either the import "+
					"that claimed it crashed part-way through, or it is slower than this bound. This boot will "+
					"NOT import over it — another process may still be writing that world — and will not mark it "+
					"either, because a marker written by a boot that imported nothing would certify a world "+
					"nobody checked",
				ErrImportInterrupted, g.campaignID, wait, vocabulary.CampaignImportCompleted)
		}
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// completionFromEntity reads the marker off a stored campaign entity.
func completionFromEntity(state *graph.EntityState, campaignID string) (time.Time, bool, error) {
	if state == nil {
		return time.Time{}, false, fmt.Errorf("campaign entity %s read back as nil", campaignID)
	}
	if state.IsStub() {
		// A stub occupies the key and carries none of the campaign's own facts,
		// so it is neither an instantiated campaign nor a completed import. Read
		// as "unmarked" it would send a boot into the bounded wait for a marker
		// that can never arrive; read as "marked" it would serve play from a
		// world that was never imported at all.
		return time.Time{}, false, fmt.Errorf(
			"campaign entity %s is a referential stub: something referenced it before it was created, so it "+
				"records neither an instantiation nor an import", campaignID)
	}

	var objects []any
	for _, triple := range state.Triples {
		if triple.Predicate == vocabulary.CampaignImportCompleted.String() {
			objects = append(objects, triple.Object)
		}
	}
	switch len(objects) {
	case 0:
		return time.Time{}, false, nil
	case 1:
	default:
		// Two values for a single-valued marker is the signature of a write that
		// took an appending lane, and a reader taking the first would be choosing
		// which import it is being told about.
		return time.Time{}, false, fmt.Errorf(
			"campaign entity %s holds %d values for the single-valued %s; a marker written on an appending lane "+
				"cannot say which import finished", campaignID, len(objects), vocabulary.CampaignImportCompleted)
	}

	value, ok := objects[0].(string)
	if !ok {
		return time.Time{}, false, fmt.Errorf(
			"campaign entity %s records a %T for %s, want an RFC3339 instant",
			campaignID, objects[0], vocabulary.CampaignImportCompleted)
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false, fmt.Errorf(
			"campaign entity %s records %q for %s, which is not an RFC3339 instant: %w",
			campaignID, value, vocabulary.CampaignImportCompleted, err)
	}
	return at.UTC(), true, nil
}
