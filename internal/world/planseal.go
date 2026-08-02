package world

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// planSealWire excludes the seal itself while retaining every importable byte.
// It uses slices and structs only, so encoding/json's output is deterministic.
type planSealWire struct {
	Org             string
	WorldNS         string
	TemplateID      string
	TemplateVersion string
	Experience      ResolvedExperience
	Entities        []PlannedEntity
}

func (p *Plan) sealBytes() ([32]byte, error) {
	wire, err := json.Marshal(planSealWire{
		Org:             p.Org,
		WorldNS:         p.WorldNS,
		TemplateID:      p.TemplateID,
		TemplateVersion: p.TemplateVersion,
		Experience:      p.Experience,
		Entities:        p.Entities,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("seal resolved world plan: %w", err)
	}
	return sha256.Sum256(wire), nil
}

func (p *Plan) sealResolved() error {
	seal, err := p.sealBytes()
	if err != nil {
		return err
	}
	p.seal = seal
	return nil
}

func (p *Plan) requireResolvedSeal() error {
	if p == nil {
		return errors.New("world plan is nil")
	}
	seal, err := p.sealBytes()
	if err != nil {
		return err
	}
	if p.seal != seal {
		return errors.New("world plan was not produced unchanged by Package.Resolve; import accepts only validated seed data")
	}
	return nil
}
