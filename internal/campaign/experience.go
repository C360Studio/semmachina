package campaign

import (
	"errors"
	"fmt"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// ErrExperienceMigrationRequired identifies a campaign created before
// experience-pack provenance was recorded. Boot must stop for an explicit
// migration; it may not infer, default, or backfill the missing selection.
var ErrExperienceMigrationRequired = errors.New("campaign experience provenance requires migration")

// ErrExperienceMismatch identifies a restart requesting different sealed
// experience packs than the campaign was instantiated with.
var ErrExperienceMismatch = errors.New("requested campaign experience does not match recorded provenance")

// Experience is the immutable pair of content packs selected for a campaign.
type Experience struct {
	PersonaPack   string
	MechanicsPack string
}

// Validate requires each pack name to be one canonical entity-ID segment.
func (e Experience) Validate() error {
	if err := vocabulary.ValidateIDSegment(e.PersonaPack); err != nil {
		return fmt.Errorf("persona pack: %w", err)
	}
	if err := vocabulary.ValidateIDSegment(e.MechanicsPack); err != nil {
		return fmt.Errorf("mechanics pack: %w", err)
	}
	return nil
}
