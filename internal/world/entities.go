package world

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	ssvocab "github.com/c360studio/semstreams/vocabulary"

	"github.com/c360studio/semmachina/internal/payload"
	"github.com/c360studio/semmachina/internal/vocabulary"
)

const (
	// EntitiesFile is the entity file's fixed name inside a world package.
	EntitiesFile = "entities.jsonl"

	// LocalRefPrefix marks a template-local entity reference. The prefix exists
	// so a reference is syntactically distinct from a name: without it,
	// "gatehouse" as the object of world.location.current is indistinguishable
	// from "gatehouse" as somebody's nickname, and the importer would have to
	// guess which strings to rewrite.
	LocalRefPrefix = "local:"

	// MaxEntityLineBytes bounds one line of entities.jsonl. A world package is
	// author input; a single unbounded line is a single unbounded allocation,
	// and bufio.Scanner's default 64 KiB limit would otherwise fail with
	// "token too long" rather than with a reason.
	MaxEntityLineBytes = 64 * 1024
)

// LineError reports a fault at a specific line of a package file.
//
// The line number is the entire point. F9's trap is that entity-ID segments
// and triple predicates have different alphabets, so a package can carry a
// legal `local_id: rusty_sword` and an illegal `item.condition.rust_level` in
// the same file; without a line number the author is told their world is
// invalid and left to find where.
type LineError struct {
	File string
	Line int
	Err  error
}

// Error implements error.
func (e *LineError) Error() string {
	return fmt.Sprintf("%s:%d: %v", e.File, e.Line, e.Err)
}

// Unwrap exposes the underlying reason.
func (e *LineError) Unwrap() error { return e.Err }

func lineErr(line int, err error) error {
	return &LineError{File: EntitiesFile, Line: line, Err: err}
}

// TemplateFact is one authored fact, parsed but not yet mapped to instance
// identity. A reference still names a template-local id; only Resolve turns it
// into a six-part entity ID.
type TemplateFact struct {
	// Predicate is a registered, author-writable world-fact predicate.
	Predicate vocabulary.Predicate
	// LocalRef is the referenced template-local id, empty for literals.
	LocalRef string
	// Literal is the object of a non-reference fact: a string or an int.
	Literal any
}

// IsReference reports whether this fact points at another template entity.
func (f TemplateFact) IsReference() bool { return f.LocalRef != "" }

// TemplateEntity is one line of entities.jsonl after parsing and validation.
type TemplateEntity struct {
	// LocalID is the template-local identity. It becomes the instance position
	// of the composed entity ID, so it faces the entity-ID alphabet — which
	// permits uppercase and underscores that a predicate may not.
	LocalID string
	// Kind is the declared entity kind. It becomes the type position of the
	// composed ID and the object of the projected kind triple.
	Kind vocabulary.EntityKind
	// Facts are the authored starting facts in file order.
	Facts []TemplateFact
	// Line is the 1-based source line, carried for error reporting.
	Line int
}

// entityLine is the wire shape of one entities.jsonl record. It is separate
// from TemplateEntity so the parsed domain value cannot carry unvalidated wire
// text, and so DisallowUnknownFields has a closed struct to check against.
type entityLine struct {
	LocalID string       `json:"local_id"`
	Type    string       `json:"type"`
	Triples []tripleLine `json:"triples"`
}

type tripleLine struct {
	Predicate string          `json:"predicate"`
	Object    json.RawMessage `json:"object"`
}

// ParseEntities reads entities.jsonl and returns fully validated template
// entities.
//
// Everything checkable without instance identity is checked here: the
// predicate alphabet, vocabulary membership, author-writability, kind
// compatibility, object shape, and attribute bounds. What is deliberately NOT
// checked here is reference resolution — a `local:` target may be declared on a
// later line, so dangling references are a whole-package question that Resolve
// answers.
func ParseEntities(r io.Reader, specs vocabulary.AttributeSpecSet) ([]TemplateEntity, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), MaxEntityLineBytes)

	var entities []TemplateEntity
	declaredAt := make(map[string]int)

	// lineNumber is declared outside the loop so a SCANNER error — notably a
	// line past MaxEntityLineBytes — can still name the line it failed on. A
	// bare "token too long" tells an author their world is broken and nothing
	// about where, which is the failure mode the line numbers exist to prevent.
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}

		entity, err := parseEntityLine(text, lineNumber, specs)
		if err != nil {
			return nil, err
		}
		if first, duplicate := declaredAt[entity.LocalID]; duplicate {
			return nil, lineErr(lineNumber, fmt.Errorf(
				"local_id %q was already declared on line %d; one template id names one entity",
				entity.LocalID, first))
		}
		declaredAt[entity.LocalID] = lineNumber
		entities = append(entities, entity)
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, lineErr(lineNumber+1, fmt.Errorf(
				"line exceeds the %d-byte limit; a world package is author input and one line is one entity",
				MaxEntityLineBytes))
		}
		return nil, lineErr(lineNumber+1, err)
	}
	if len(entities) == 0 {
		return nil, fmt.Errorf("%s: declares no entities", EntitiesFile)
	}
	return entities, nil
}

func parseEntityLine(text string, lineNumber int, specs vocabulary.AttributeSpecSet) (TemplateEntity, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.DisallowUnknownFields()

	var wire entityLine
	if err := decoder.Decode(&wire); err != nil {
		return TemplateEntity{}, lineErr(lineNumber, err)
	}
	if decoder.More() {
		return TemplateEntity{}, lineErr(lineNumber,
			errors.New("line carries more than one JSON value; entities.jsonl is one entity per line"))
	}

	if err := vocabulary.ValidateIDSegment(wire.LocalID); err != nil {
		return TemplateEntity{}, lineErr(lineNumber, fmt.Errorf("local_id: %w", err))
	}
	kind, err := vocabulary.ParseEntityKind(wire.Type)
	if err != nil {
		return TemplateEntity{}, lineErr(lineNumber, fmt.Errorf("type: %w", err))
	}
	if len(wire.Triples) == 0 {
		return TemplateEntity{}, lineErr(lineNumber,
			fmt.Errorf("entity %q declares no triples", wire.LocalID))
	}

	facts := make([]TemplateFact, 0, len(wire.Triples))
	for index, triple := range wire.Triples {
		fact, factErr := parseTemplateFact(triple, kind, specs)
		if factErr != nil {
			return TemplateEntity{}, lineErr(lineNumber,
				fmt.Errorf("entity %q triple %d: %w", wire.LocalID, index, factErr))
		}
		facts = append(facts, fact)
	}

	return TemplateEntity{LocalID: wire.LocalID, Kind: kind, Facts: facts, Line: lineNumber}, nil
}

// parseTemplateFact applies, in order, the two identity contracts F9 warns are
// different and then the vocabulary's own rules.
//
// The upstream predicate parser runs FIRST and unconditionally. It is the same
// parser the ENTITY_STATES write gate runs, so a predicate that fails it would
// have been rejected at write time — long after the import "succeeded" — with
// an error naming a triple rather than a file and line.
func parseTemplateFact(
	triple tripleLine,
	kind vocabulary.EntityKind,
	specs vocabulary.AttributeSpecSet,
) (TemplateFact, error) {
	if _, err := ssvocab.ParsePredicate(triple.Predicate); err != nil {
		return TemplateFact{}, fmt.Errorf(
			"predicate %q is not a canonical three-segment lower-kebab predicate "+
				"(entity ids may carry underscores and uppercase, predicates may not): %w",
			triple.Predicate, err)
	}

	predicate := vocabulary.Predicate(triple.Predicate)
	if !AuthorWritable(predicate) {
		return TemplateFact{}, fmt.Errorf(
			"predicate %q is not author-writable; a template may declare %v",
			predicate, AuthorWritablePredicates())
	}
	if !vocabulary.AllowsSubjectKind(predicate, kind) {
		allowed, _ := vocabulary.SubjectKindsFor(predicate)
		return TemplateFact{}, fmt.Errorf(
			"predicate %q cannot be carried by a %q entity (allowed kinds: %v)", predicate, kind, allowed)
	}

	return parseFactObject(predicate, triple.Object, specs)
}

func parseFactObject(
	predicate vocabulary.Predicate,
	raw json.RawMessage,
	specs vocabulary.AttributeSpecSet,
) (TemplateFact, error) {
	switch ObjectShapeFor(predicate) {
	case ShapeReference:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		local, found := strings.CutPrefix(text, LocalRefPrefix)
		if !found {
			return TemplateFact{}, fmt.Errorf(
				"predicate %q takes an entity reference; write %q, not %q",
				predicate, LocalRefPrefix+"some-local-id", text)
		}
		if err := vocabulary.ValidateIDSegment(local); err != nil {
			return TemplateFact{}, fmt.Errorf("reference target: %w", err)
		}
		return TemplateFact{Predicate: predicate, LocalRef: local}, nil

	case ShapeStatus:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		status, err := vocabulary.ParseStatus(text)
		if err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: string(status)}, nil

	case ShapeAttribute:
		attribute, ok := vocabulary.AttributeForPredicate(predicate)
		if !ok {
			// Unreachable: ObjectShapeFor derives ShapeAttribute from the same map.
			return TemplateFact{}, fmt.Errorf("predicate %q has no registered attribute", predicate)
		}
		value, err := decodeInt(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		if err := specs.Check(attribute, value); err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: value}, nil

	case ShapeOrdinal:
		value, err := decodeInt(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		if value < 1 {
			return TemplateFact{}, fmt.Errorf("predicate %q must be a positive whole number", predicate)
		}
		return TemplateFact{Predicate: predicate, Literal: value}, nil

	case ShapeEvidenceTruthStatus:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		status, err := vocabulary.ParseEvidenceTruthStatus(text)
		if err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: string(status)}, nil

	case ShapeCasePhase:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		phase, err := vocabulary.ParseCasePhase(text)
		if err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: string(phase)}, nil

	case ShapeEvidenceRevealKind:
		return parseEvidenceRevealKind(predicate, raw)

	case ShapeBeliefStance:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		stance, err := vocabulary.ParseBeliefStance(text)
		if err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: string(stance)}, nil

	case ShapeCompanionPolicy:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		policy, err := vocabulary.ParseCompanionPolicy(text)
		if err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: string(policy)}, nil

	case ShapeHintLevel:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		level, err := vocabulary.ParseHintLevel(text)
		if err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: string(level)}, nil

	case ShapeNumber:
		value, err := decodeFloat(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		return TemplateFact{Predicate: predicate, Literal: value}, nil

	case ShapeText:
		text, err := decodeString(raw)
		if err != nil {
			return TemplateFact{}, err
		}
		if text == "" {
			return TemplateFact{}, fmt.Errorf("predicate %q has an empty object", predicate)
		}
		if strings.HasPrefix(text, LocalRefPrefix) {
			return TemplateFact{}, fmt.Errorf(
				"predicate %q takes literal text but its object begins with %q; "+
					"a mistyped reference would be stored as a name", predicate, LocalRefPrefix)
		}
		if len(text) > payload.MaxFactTextBytes {
			return TemplateFact{}, fmt.Errorf(
				"predicate %q is %d bytes, which exceeds the %d-byte fact-text budget",
				predicate, len(text), payload.MaxFactTextBytes)
		}
		return TemplateFact{Predicate: predicate, Literal: text}, nil

	default:
		// Unreachable: AuthorWritable already rejected anything without a shape.
		return TemplateFact{}, fmt.Errorf("predicate %q has no registered object shape", predicate)
	}
}

func parseEvidenceRevealKind(predicate vocabulary.Predicate, raw json.RawMessage) (TemplateFact, error) {
	text, err := decodeString(raw)
	if err != nil {
		return TemplateFact{}, err
	}
	kind, err := vocabulary.ParseEvidenceRevealKind(text)
	if err != nil {
		return TemplateFact{}, err
	}
	return TemplateFact{Predicate: predicate, Literal: string(kind)}, nil
}

func decodeString(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("object must be a JSON string: %w", err)
	}
	return text, nil
}

func decodeInt(raw json.RawMessage) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("object must be a JSON number: %w", err)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("object must be a JSON number, got %s", raw)
	}
	integer, err := number.Int64()
	if err != nil {
		return 0, fmt.Errorf("object %s must be a whole number", number)
	}
	return int(integer), nil
}

func decodeFloat(raw json.RawMessage) (float64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fmt.Errorf("object must be a JSON number: %w", err)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("object must be a JSON number, got %s", raw)
	}
	numeric, err := number.Float64()
	if err != nil || math.IsNaN(numeric) || math.IsInf(numeric, 0) {
		return 0, fmt.Errorf("object %s must be a finite number", number)
	}
	return numeric, nil
}
