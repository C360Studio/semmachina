package world

import (
	"fmt"
	"math"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// validatePlaceEntities checks authored place invariants while every identity
// is still template-local. LoadPackage runs it before it can return a package
// that resolution or import could materialize.
func validatePlaceEntities(entities []TemplateEntity) error {
	byID := make(map[string]TemplateEntity, len(entities))
	for _, entity := range entities {
		byID[entity.LocalID] = entity
	}

	for _, entity := range entities {
		switch entity.Kind {
		case vocabulary.EntityKindScene:
			placements := factsFor(entity, vocabulary.SceneLocationCurrent)
			if len(placements) != 1 {
				return lineErr(entity.Line, fmt.Errorf(
					"scene %q placement requires exactly one %s reference, got %d",
					entity.LocalID, vocabulary.SceneLocationCurrent, len(placements)))
			}
			if err := requirePlaceRef(entity, placements[0], byID, "scene placement"); err != nil {
				return lineErr(entity.Line, err)
			}
		case vocabulary.EntityKindCharacter, vocabulary.EntityKindItem:
			occupancy := factsFor(entity, vocabulary.WorldLocationCurrent)
			if len(occupancy) > 1 {
				return lineErr(entity.Line, fmt.Errorf(
					"entity %q occupancy allows at most one %s reference, got %d",
					entity.LocalID, vocabulary.WorldLocationCurrent, len(occupancy)))
			}
			if len(occupancy) == 1 {
				if err := requirePlaceRef(entity, occupancy[0], byID, "occupancy"); err != nil {
					return lineErr(entity.Line, err)
				}
			}
		case vocabulary.EntityKindLocation:
			if err := validateLocation(entity, byID); err != nil {
				return lineErr(entity.Line, err)
			}
		}
	}
	return nil
}

func requirePlaceRef(
	subject TemplateEntity,
	fact TemplateFact,
	byID map[string]TemplateEntity,
	field string,
) error {
	if !fact.IsReference() {
		return fmt.Errorf("%s for %q is not a reference", field, subject.LocalID)
	}
	if fact.LocalRef == subject.LocalID {
		return fmt.Errorf("%s for %q references itself", field, subject.LocalID)
	}
	target, ok := byID[fact.LocalRef]
	if !ok {
		return fmt.Errorf("%s for %q references missing location %q",
			field, subject.LocalID, LocalRefPrefix+fact.LocalRef)
	}
	if target.Kind != vocabulary.EntityKindLocation {
		return fmt.Errorf("%s for %q references %q of kind %q; want location",
			field, subject.LocalID, LocalRefPrefix+fact.LocalRef, target.Kind)
	}
	return nil
}

func validateLocation(entity TemplateEntity, byID map[string]TemplateEntity) error {
	connections := factsFor(entity, vocabulary.LocationRelationConnectsTo)
	seen := make(map[string]bool, len(connections))
	for _, connection := range connections {
		if connection.LocalRef == entity.LocalID {
			return fmt.Errorf("location %q connects to itself", entity.LocalID)
		}
		if seen[connection.LocalRef] {
			return fmt.Errorf("location %q connection names %q more than once",
				entity.LocalID, connection.LocalRef)
		}
		seen[connection.LocalRef] = true
		if err := requirePlaceRef(entity, connection, byID, "connection"); err != nil {
			return err
		}
	}

	latitudes := factsFor(entity, vocabulary.GeoLocationLatitude)
	longitudes := factsFor(entity, vocabulary.GeoLocationLongitude)
	if len(latitudes) > 1 || len(longitudes) > 1 {
		return fmt.Errorf("location %q coordinates are single-valued", entity.LocalID)
	}
	if (len(latitudes) == 1) != (len(longitudes) == 1) {
		return fmt.Errorf("location %q coordinates must declare latitude and longitude as a pair", entity.LocalID)
	}
	if len(latitudes) == 0 {
		return nil
	}
	latitude, ok := latitudes[0].Literal.(float64)
	if !ok || math.IsNaN(latitude) || math.IsInf(latitude, 0) {
		return fmt.Errorf("location %q latitude must be finite", entity.LocalID)
	}
	longitude, ok := longitudes[0].Literal.(float64)
	if !ok || math.IsNaN(longitude) || math.IsInf(longitude, 0) {
		return fmt.Errorf("location %q longitude must be finite", entity.LocalID)
	}
	if latitude < -90 || latitude > 90 {
		return fmt.Errorf("location %q latitude %v is outside [-90, 90]", entity.LocalID, latitude)
	}
	if longitude < -180 || longitude > 180 {
		return fmt.Errorf("location %q longitude %v is outside [-180, 180]", entity.LocalID, longitude)
	}
	return nil
}
