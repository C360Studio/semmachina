package vocabulary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semmachina/internal/vocabulary"
)

// declaredStringConstants reads this package's own source and returns, for
// every `type X string` it declares, the literal values of every constant
// declared with that type.
//
// Reflection cannot do this — Go constants are erased at runtime — so source
// is the only place the question "did someone add a constant and forget the
// enumeration?" can be answered. That question is the whole meaning of
// "closed": a set the engine enumerates but the source can exceed is not
// closed, it is a suggestion.
//
// The scan is deliberately narrow and NOT total. It sees only constants
// declared with an explicit named type and a string BasicLit — the form every
// value in this package uses. It does not see values built by conversion
// (`Status(other)`), concatenation (`"a" + "b"`), iota-derived constants, or
// package-level vars. Those forms would slip past this test, so the rule is to
// keep declaring vocabulary the plain way rather than to trust the scan to
// catch a clever one.
func declaredStringConstants(t *testing.T) (types []string, constants map[string][]string) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("no non-test source files found; the closure check would be vacuous")
	}

	constants = make(map[string][]string)
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gen.Tok {
			case token.TYPE:
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if ident, ok := ts.Type.(*ast.Ident); ok && ident.Name == "string" {
						types = append(types, ts.Name.Name)
					}
				}
			case token.CONST:
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || vs.Type == nil || len(vs.Values) == 0 {
						continue
					}
					ident, ok := vs.Type.(*ast.Ident)
					if !ok {
						continue
					}
					for _, value := range vs.Values {
						lit, ok := value.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						unquoted, unquoteErr := strconv.Unquote(lit.Value)
						if unquoteErr != nil {
							t.Fatalf("unquote %s: %v", lit.Value, unquoteErr)
						}
						constants[ident.Name] = append(constants[ident.Name], unquoted)
					}
				}
			}
		}
	}

	slices.Sort(types)
	return types, constants
}

// enumerations maps each declared string type to the enumeration the engine
// exposes for it. Every `type X string` in the package must appear here, so
// adding a new closed set without wiring it into Sets() (or AllPredicates())
// fails this test rather than shipping a half-registered vocabulary.
func enumerations() map[string][]string {
	sets := vocabulary.Sets()
	return map[string][]string{
		"Plausibility":     sets[vocabulary.KindPlausibility],
		"Risk":             sets[vocabulary.KindRisk],
		"ConsequenceClass": sets[vocabulary.KindConsequence],
		"OutcomeBand":      sets[vocabulary.KindOutcomeBand],
		"ModifierSource":   sets[vocabulary.KindModifierSource],
		"EffectType":       sets[vocabulary.KindEffectType],
		"Status":           sets[vocabulary.KindStatus],
		"Attribute":        sets[vocabulary.KindAttribute],
		"Relation":         sets[vocabulary.KindRelation],
		"EntityKind":       sets[vocabulary.KindEntityKind],
		"TurnPhase":        sets[vocabulary.KindTurnPhase],
		"ChannelAdapter":   sets[vocabulary.KindChannelAdapter],
		"Mechanic":         sets[vocabulary.KindMechanic],
		"RNG":              sets[vocabulary.KindRNG],
		"FailureReason":    sets[vocabulary.KindFailureReason],
		"RollGateMapping":  sets[vocabulary.KindRollGateMapping],
		"Predicate":        strs(vocabulary.AllPredicates()),
		// Kind names the sets; it is not itself a rule-matched value set.
		"Kind": nil,
	}
}

func TestClosedSets_EveryDeclaredConstantIsEnumerated(t *testing.T) {
	declaredTypes, constants := declaredStringConstants(t)
	expected := enumerations()

	for _, typeName := range declaredTypes {
		if _, known := expected[typeName]; !known {
			t.Fatalf("string type %q is declared but has no registered enumeration; "+
				"a closed set the tool schema and rule pack cannot see is not closed", typeName)
		}
	}

	for typeName, enumerated := range expected {
		if enumerated == nil {
			continue
		}
		declared := constants[typeName]
		if len(declared) == 0 {
			t.Fatalf("type %q has an enumeration but no constants in source", typeName)
		}

		enumeratedSet := make(map[string]bool, len(enumerated))
		for _, v := range enumerated {
			enumeratedSet[v] = true
		}
		for _, v := range declared {
			if !enumeratedSet[v] {
				t.Fatalf("constant %q of type %s is declared in source but missing from its enumeration; "+
					"the adjudicator schema, the applier, and the rule pack would never see it", v, typeName)
			}
		}

		declaredSet := make(map[string]bool, len(declared))
		for _, v := range declared {
			declaredSet[v] = true
		}
		for _, v := range enumerated {
			if !declaredSet[v] {
				t.Fatalf("enumeration for %s lists %q, which no constant in source declares", typeName, v)
			}
		}
	}
}

// Kind is enumerated by Kinds(); it is exempt from the value-set check above,
// so check it here rather than leaving it untested.
func TestKinds_CoverEveryDeclaredKindConstant(t *testing.T) {
	_, constants := declaredStringConstants(t)
	declared := constants["Kind"]
	if len(declared) == 0 {
		t.Fatal("no Kind constants found in source")
	}

	registered := make(map[string]bool)
	for _, k := range vocabulary.Kinds() {
		registered[string(k)] = true
	}
	for _, k := range declared {
		if !registered[k] {
			t.Fatalf("kind %q is declared but not registered in Kinds()/Sets()", k)
		}
	}
	if len(registered) != len(declared) {
		t.Fatalf("Kinds() reports %d kinds, source declares %d", len(registered), len(declared))
	}
}
