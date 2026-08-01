package epistemic

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Scope pins private case reads to one resolved world instance. Its fields are
// private so projection code cannot fall back to discovering a case or belief
// collection from the shared graph.
type Scope struct {
	caseID           string
	beliefsByActorID map[string][]string
}

// MaxCasekeeperTargetActors is the audience bound shared by scope derivation
// and projection validation.
const MaxCasekeeperTargetActors = 8

// NewScope records the resolved case ID and the authored belief record IDs
// associated with each actor in that same validated world plan. An empty case
// with no beliefs is the explicit non-mystery-world scope.
func NewScope(caseID string, beliefsByActorID map[string][]string) (Scope, error) {
	caseID = strings.TrimSpace(caseID)
	if caseID == "" && len(beliefsByActorID) > 0 {
		return Scope{}, errors.New("epistemic scope cannot carry beliefs without a scoped case")
	}

	scope := Scope{caseID: caseID, beliefsByActorID: make(map[string][]string, len(beliefsByActorID))}
	recordOwners := make(map[string]string)
	for actorID, recordIDs := range beliefsByActorID {
		actorID = strings.TrimSpace(actorID)
		if actorID == "" {
			return Scope{}, errors.New("epistemic scope contains an empty belief actor ID")
		}
		ids := slices.Clone(recordIDs)
		for index := range ids {
			ids[index] = strings.TrimSpace(ids[index])
			if ids[index] == "" {
				return Scope{}, fmt.Errorf("epistemic scope actor %s contains an empty belief record ID", actorID)
			}
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		for _, recordID := range ids {
			if owner, exists := recordOwners[recordID]; exists && owner != actorID {
				return Scope{}, fmt.Errorf("belief record %s is assigned to both %s and %s", recordID, owner, actorID)
			}
			recordOwners[recordID] = actorID
		}
		scope.beliefsByActorID[actorID] = ids
	}
	return scope, nil
}

// CasekeeperAudience derives the private interpreter's authorization from the
// validated plan scope. A non-mystery scope is explicitly not applicable; it
// does not mint an empty privileged audience.
func (s Scope) CasekeeperAudience(
	turnID, turnEntityID string,
) (AuthenticatedAudience, bool, error) {
	if s.caseID == "" {
		return AuthenticatedAudience{}, false, nil
	}
	targets := make([]string, 0, len(s.beliefsByActorID))
	for actorID := range s.beliefsByActorID {
		targets = append(targets, actorID)
	}
	sort.Strings(targets)
	if len(targets) > MaxCasekeeperTargetActors {
		return AuthenticatedAudience{}, false, fmt.Errorf(
			"casekeeper scope has %d belief holders; limit is %d", len(targets), MaxCasekeeperTargetActors)
	}
	return CasekeeperAudience(s.caseID, turnID, turnEntityID, targets...), true, nil
}

func (s Scope) beliefRecords(targetActorIDs []string, limit int) (map[string]string, error) {
	targets := slices.Clone(targetActorIDs)
	for index := range targets {
		targets[index] = strings.TrimSpace(targets[index])
		if targets[index] == "" {
			return nil, errors.New("casekeeper target actor IDs must not be empty")
		}
	}
	slices.Sort(targets)
	targets = slices.Compact(targets)

	records := make(map[string]string)
	for _, actorID := range targets {
		for _, recordID := range s.beliefsByActorID[actorID] {
			records[recordID] = actorID
			if len(records) > limit {
				return nil, fmt.Errorf("scoped belief records exceed supplemental entity limit %d", limit)
			}
		}
	}
	return records, nil
}

func sortedRecordIDs(records map[string]string) []string {
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
