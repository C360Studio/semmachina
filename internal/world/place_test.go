package world_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/c360studio/semmachina/internal/vocabulary"
	"github.com/c360studio/semmachina/internal/world"
)

const (
	placeA = `{"local_id":"gatehouse","type":"location","triples":[` +
		`{"predicate":"world.entity.name","object":"Gatehouse"},` +
		`{"predicate":"geo.location.latitude","object":41.25},` +
		`{"predicate":"geo.location.longitude","object":-87.75},` +
		`{"predicate":"location.relation.connects-to","object":"local:road"}]}`
	placeB = `{"local_id":"road","type":"location","triples":[` +
		`{"predicate":"world.entity.name","object":"Road"}]}`
	placedScene = `{"local_id":"arrival","type":"scene","triples":[` +
		`{"predicate":"world.entity.name","object":"Arrival"},` +
		`{"predicate":"scene.location.current","object":"local:gatehouse"}]}`
	placedCharacter = `{"local_id":"rook","type":"character","triples":[` +
		`{"predicate":"world.entity.name","object":"Rook"},` +
		`{"predicate":"world.location.current","object":"local:gatehouse"}]}`
)

func placePackageFS(lines ...string) fstest.MapFS {
	fsys := minimalPackageFS()
	fsys[world.EntitiesFile] = &fstest.MapFile{Data: []byte(strings.Join(lines, "\n") + "\n")}
	return fsys
}

func loadPlaceErr(t *testing.T, lines ...string) error {
	t.Helper()
	_, err := world.LoadPackage(placePackageFS(lines...), world.LoadOptions{})
	if err == nil {
		t.Fatalf("LoadPackage accepted invalid place authoring:\n%s", strings.Join(lines, "\n"))
	}
	return err
}

func TestPlaceAuthoring_UsesReferenceAndNumericShapes(t *testing.T) {
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.SceneLocationCurrent,
		vocabulary.LocationRelationConnectsTo,
	} {
		if got := world.ObjectShapeFor(predicate); got != world.ShapeReference {
			t.Errorf("ObjectShapeFor(%q) = %v, want ShapeReference", predicate, got)
		}
	}
	for _, predicate := range []vocabulary.Predicate{
		vocabulary.GeoLocationLatitude,
		vocabulary.GeoLocationLongitude,
	} {
		if got := world.ObjectShapeFor(predicate); got != world.ShapeNumber {
			t.Errorf("ObjectShapeFor(%q) = %v, want ShapeNumber", predicate, got)
		}
	}
}

func TestLoadPackage_AcceptsDirectedTopologyAndOptionalGeometry(t *testing.T) {
	pkg, err := world.LoadPackage(
		placePackageFS(placeA, placeB, placedScene, placedCharacter), world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	road := entityByLocalID(t, pkg.Entities, "road")
	for _, fact := range road.Facts {
		if fact.Predicate == vocabulary.LocationRelationConnectsTo {
			t.Fatal("one-way gatehouse-to-road topology inferred a reverse road-to-gatehouse edge")
		}
	}
}

func TestLoadPackage_RejectsInvalidScenePlacementAndOccupancy(t *testing.T) {
	cases := map[string]struct {
		lines []string
		want  string
	}{
		"missing scene placement": {
			[]string{placeA, placeB, strings.Replace(placedScene,
				`,{"predicate":"scene.location.current","object":"local:gatehouse"}`, "", 1)},
			"exactly one",
		},
		"duplicate scene placement": {
			[]string{placeA, placeB, strings.Replace(placedScene, `]}`,
				`,{"predicate":"scene.location.current","object":"local:road"}]}`, 1)},
			"exactly one",
		},
		"dangling scene placement": {
			[]string{placeA, placeB, strings.Replace(placedScene, "local:gatehouse", "local:nowhere", 1)},
			"nowhere",
		},
		"self scene placement": {
			[]string{placeA, placeB, strings.Replace(placedScene, "local:gatehouse", "local:arrival", 1)},
			"itself",
		},
		"wrong-kind scene placement": {
			[]string{placeA, placeB, placedCharacter,
				strings.Replace(placedScene, "local:gatehouse", "local:rook", 1)},
			"location",
		},
		"scene-as-place occupancy": {
			[]string{placeA, placeB, placedScene,
				strings.Replace(placedCharacter, "local:gatehouse", "local:arrival", 1)},
			"location",
		},
		"duplicate occupancy": {
			[]string{placeA, placeB, placedScene, strings.Replace(placedCharacter, `]}`,
				`,{"predicate":"world.location.current","object":"local:road"}]}`, 1)},
			"at most one",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadPlaceErr(t, tc.lines...)
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadPackage_RejectsInvalidConnections(t *testing.T) {
	cases := map[string]struct {
		connection string
		other      []string
		want       string
	}{
		"dangling":   {`local:missing`, nil, "missing"},
		"self":       {`local:gatehouse`, nil, "itself"},
		"wrong kind": {`local:rook`, []string{placedCharacter}, "location"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			location := strings.Replace(placeA, "local:road", tc.connection, 1)
			lines := append([]string{location, placeB, placedScene}, tc.other...)
			err := loadPlaceErr(t, lines...)
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}

	duplicate := strings.Replace(placeA, `]}`,
		`,{"predicate":"location.relation.connects-to","object":"local:road"}]}`, 1)
	err := loadPlaceErr(t, duplicate, placeB, placedScene)
	if !strings.Contains(strings.ToLower(err.Error()), "more than once") {
		t.Fatalf("duplicate connection error = %v", err)
	}
}

func TestLoadPackage_RejectsInvalidCoordinates(t *testing.T) {
	cases := map[string]struct {
		location string
		want     string
	}{
		"latitude only": {
			strings.Replace(placeA, `,{"predicate":"geo.location.longitude","object":-87.75}`, "", 1),
			"pair",
		},
		"longitude only": {
			strings.Replace(placeA, `,{"predicate":"geo.location.latitude","object":41.25}`, "", 1),
			"pair",
		},
		"latitude range":  {strings.Replace(placeA, "41.25", "90.01", 1), "latitude"},
		"longitude range": {strings.Replace(placeA, "-87.75", "-180.01", 1), "longitude"},
		"non-finite":      {strings.Replace(placeA, "41.25", "1e999", 1), "finite"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadPlaceErr(t, tc.location, placeB, placedScene)
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestResolve_RevalidatesPlaceInvariantsOnDirectlyConstructedPackages(t *testing.T) {
	cases := map[string]struct {
		lines []string
		want  string
	}{
		"missing placement": {
			[]string{placeA, placeB, placedCharacter, strings.Replace(placedScene,
				`,{"predicate":"scene.location.current","object":"local:gatehouse"}`, "", 1)},
			"exactly one",
		},
		"duplicate placement": {
			[]string{placeA, placeB, placedCharacter, strings.Replace(placedScene, `]}`,
				`,{"predicate":"scene.location.current","object":"local:road"}]}`, 1)},
			"exactly one",
		},
		"bad occupancy": {
			[]string{placeA, placeB, placedScene,
				strings.Replace(placedCharacter, "local:gatehouse", "local:arrival", 1)},
			"want location",
		},
		"bad connection": {
			[]string{strings.Replace(placeA, "local:road", "local:rook", 1), placeB, placedScene, placedCharacter},
			"want location",
		},
		"partial coordinates": {
			[]string{strings.Replace(placeA,
				`,{"predicate":"geo.location.longitude","object":-87.75}`, "", 1), placeB, placedScene, placedCharacter},
			"pair",
		},
		"out of range coordinates": {
			[]string{strings.Replace(placeA, "41.25", "91", 1), placeB, placedScene, placedCharacter},
			"latitude",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plan, err := testPackage(t, tc.lines...).Resolve(testInstance())
			if err == nil {
				t.Fatal("Resolve sealed an invalid directly constructed package")
			}
			if plan != nil {
				t.Fatalf("Resolve returned a plan alongside refusal: %+v", plan)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestResolve_RevalidatesAPlacePackageMutatedAfterLoad(t *testing.T) {
	pkg, err := world.LoadPackage(
		placePackageFS(placeA, placeB, placedScene, placedCharacter), world.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	for index := range pkg.Entities {
		if pkg.Entities[index].LocalID != "arrival" {
			continue
		}
		pkg.Entities[index].Facts = append(pkg.Entities[index].Facts, world.TemplateFact{
			Predicate: vocabulary.SceneLocationCurrent,
			LocalRef:  "road",
		})
	}

	plan, err := pkg.Resolve(testInstance())
	if err == nil {
		t.Fatal("Resolve sealed a Package.Entities slice mutated after LoadPackage")
	}
	if plan != nil {
		t.Fatalf("Resolve returned a plan alongside refusal: %+v", plan)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "exactly one") {
		t.Fatalf("Resolve error = %v, want duplicate placement refusal", err)
	}
}
