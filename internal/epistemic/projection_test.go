package epistemic_test

import (
	"reflect"
	"testing"

	"github.com/c360studio/semmachina/internal/epistemic"
)

func TestPurposesAreClosed(t *testing.T) {
	want := []epistemic.Purpose{
		epistemic.PurposeCasekeeper,
		epistemic.PurposePlayer,
		epistemic.PurposeCompanion,
		epistemic.PurposePublicAdjudicator,
		epistemic.PurposeNarrator,
		epistemic.PurposeDenouement,
		epistemic.PurposeVerifier,
		epistemic.PurposeOperator,
	}
	if got := epistemic.Purposes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Purposes() = %v, want %v", got, want)
	}
	if _, err := epistemic.ParsePurpose("debug-everything"); err == nil {
		t.Fatal("ParsePurpose accepted an undeclared purpose")
	}
}

func TestCurrentPublicPersonaAudiencesCarryNoCallerSuppliedActorID(t *testing.T) {
	if _, found := reflect.TypeOf(epistemic.AuthenticatedAudience{}).FieldByName("actorID"); found {
		t.Fatal("AuthenticatedAudience still carries a caller-selected actor ID")
	}
	for _, audience := range []epistemic.AuthenticatedAudience{
		epistemic.PublicAdjudicatorAudience("turn-1", "turn-entity-1"),
		epistemic.NarratorAudience("turn-1", "turn-entity-1"),
	} {
		value := reflect.ValueOf(audience)
		typeOf := value.Type()
		for index := 0; index < value.NumField(); index++ {
			if typeOf.Field(index).IsExported() {
				t.Fatalf("AuthenticatedAudience exposes field %q; callers could forge projector authorization",
					typeOf.Field(index).Name)
			}
		}
		if audience.Purpose() != epistemic.PurposePublicAdjudicator &&
			audience.Purpose() != epistemic.PurposeNarrator {
			t.Fatalf("public constructor returned purpose %q", audience.Purpose())
		}
	}
}

func TestProjectAcceptsOnlyTheAudienceBoundScope(t *testing.T) {
	method, ok := reflect.TypeOf((*epistemic.Projector)(nil)).MethodByName("Project")
	if !ok {
		t.Fatal("Projector.Project is missing")
	}
	// receiver + context + authenticated audience; no separate turn/case/actor
	// arguments remain for a caller to substitute after audience construction.
	if method.Type.NumIn() != 3 {
		t.Fatalf("Projector.Project has %d inputs including receiver, want 3", method.Type.NumIn())
	}
}

func TestProjectionIsAValueWithoutReadersOrExclusionIdentifiers(t *testing.T) {
	typeOf := reflect.TypeOf(epistemic.Projection{})
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if field.Name == "Excluded" || field.Name == "Missing" || field.Name == "Stubs" {
			t.Fatalf("Projection exposes unauthorized omission metadata through %s", field.Name)
		}
		if field.Type.Kind() == reflect.Interface ||
			field.Type.Kind() == reflect.Func ||
			field.Type.Kind() == reflect.Pointer {
			t.Fatalf("Projection field %s has lazy/capability-bearing type %s", field.Name, field.Type)
		}
	}
}
