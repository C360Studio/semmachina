package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxProbeBody = 1 << 20

var graphqlHTTPClient = &http.Client{Timeout: 5 * time.Second}

func awaitGraphQL(ctx context.Context, endpoint, prefix, locationID string) error {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		err := probeGraphQL(ctx, endpoint, prefix, locationID)
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-deadline.C:
			return fmt.Errorf("real entity and relationship probes did not pass: %w", last)
		case <-ticker.C:
		}
	}
}

func probeGraphQL(ctx context.Context, endpoint, prefix, locationID string) error {
	var prefixResponse struct {
		Data struct {
			Entities []struct {
				ID string `json:"id"`
			} `json:"entitiesByPrefix"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := postGraphQL(ctx, endpoint,
		`query($prefix:String!){entitiesByPrefix(prefix:$prefix,limit:999){id}}`,
		map[string]any{"prefix": prefix}, &prefixResponse); err != nil {
		return err
	}
	if len(prefixResponse.Errors) != 0 {
		return errors.New("entitiesByPrefix returned a GraphQL error")
	}
	found := false
	for _, entity := range prefixResponse.Data.Entities {
		if entity.ID == locationID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("known Bellweather location is absent")
	}

	var relationshipResponse struct {
		Data struct {
			Relationships json.RawMessage `json:"relationships"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := postGraphQL(ctx, endpoint,
		`query($entityId:String!,$direction:String!){relationships(entityId:$entityId,direction:$direction){from to predicate}}`,
		map[string]any{"entityId": locationID, "direction": "outgoing"}, &relationshipResponse); err != nil {
		return err
	}
	if len(relationshipResponse.Errors) != 0 {
		return errors.New("relationships returned a GraphQL error")
	}
	if len(relationshipResponse.Data.Relationships) == 0 || bytes.Equal(bytes.TrimSpace(relationshipResponse.Data.Relationships), []byte("null")) {
		return errors.New("relationships result is absent")
	}
	var relationships []map[string]json.RawMessage
	if err := json.Unmarshal(relationshipResponse.Data.Relationships, &relationships); err != nil {
		return errors.New("relationships result is not an array")
	}
	for _, candidate := range relationships {
		corrected, correctedPresent, err := readRelationshipRepresentation(candidate, [3]string{"from", "to", "predicate"})
		if err != nil {
			return err
		}
		beta159, beta159Present, err := readRelationshipRepresentation(candidate, [3]string{"from_entity_id", "to_entity_id", "edge_type"})
		if err != nil {
			return err
		}
		if !correctedPresent && !beta159Present {
			return errors.New("relationships result contains no supported representation")
		}
		if correctedPresent && beta159Present && corrected != beta159 {
			return errors.New("relationships result contains conflicting representations")
		}
	}
	return nil
}

type normalizedRelationship struct{ From, To, Predicate string }

func readRelationshipRepresentation(candidate map[string]json.RawMessage, keys [3]string) (normalizedRelationship, bool, error) {
	present := false
	for _, key := range keys {
		if _, ok := candidate[key]; ok {
			present = true
		}
	}
	if !present {
		return normalizedRelationship{}, false, nil
	}
	values := [3]string{}
	for index, key := range keys {
		raw, ok := candidate[key]
		if !ok || json.Unmarshal(raw, &values[index]) != nil || strings.TrimSpace(values[index]) == "" {
			return normalizedRelationship{}, true, errors.New("relationships result contains an incomplete member")
		}
	}
	return normalizedRelationship{From: values[0], To: values[1], Predicate: values[2]}, true, nil
}

func postGraphQL(ctx context.Context, endpoint, query string, variables map[string]any, target any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("encode GraphQL probe: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build GraphQL probe: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := graphqlHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send GraphQL probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GraphQL probe status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxProbeBody+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read GraphQL probe: %w", err)
	}
	if len(raw) > maxProbeBody {
		return errors.New("GraphQL probe exceeded response limit")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode GraphQL probe: %w", err)
	}
	return nil
}
