---
name: query-pattern
description: Choose the right query access pattern (GraphQL, MCP, NATS Direct) for a use case. Use when designing query APIs, adding new access points, or choosing between gateway types.
argument-hint: [access scenario or caller description]
---

# Query Access Pattern Selection (SemMachina)

## What is the access scenario?

$ARGUMENTS

## Three Access Patterns

| Pattern | Best For | Key Property |
|---------|----------|-------------|
| **GraphQL** | External apps, web frontends, data exploration | Schema-validated, field selection, introspection |
| **MCP** | AI agents, LLMs, automated reasoning | Bounded capabilities, full audit trail, structured tools |
| **NATS Direct** | Internal services, low-latency paths | No gateway overhead, lowest latency |

## Quick Decision

```
Who is calling?

  External app / web frontend   --> GraphQL
  AI agent / LLM                --> MCP
  Internal service              --> NATS Direct
  Multiple caller types         --> Combine patterns (see below)
```

## Game-flavored cases

| Caller | Pattern | Notes |
|--------|---------|-------|
| Personas retrieving scene context (context assembler) | NATS Direct / agent tools | Internal, latency-sensitive, per-turn |
| Player client (actions in, narration out) | WebSocket components | Not a query path — player I/O is evented |
| GM / debug dashboard ("what does the world believe?") | GraphQL | Exploration, field selection |
| Legends-mode queries ("what happened in the northern valley?") | GraphRAG via MCP or GraphQL | Theme-spanning; the hard retrieval path |
| Writer-loop batch replay | NATS Direct + ObjectStore reads | Offline consumer over event log + trajectories |

## Decision Matrix

| Factor | GraphQL | MCP | NATS Direct |
|--------|---------|-----|-------------|
| Latency | Higher (HTTP) | Higher (HTTP) | Lowest (direct) |
| Schema control | Strong (SDL) | Strong (tool schemas) | Per-component |
| Auditability | Good (query logs) | Excellent (tool call audit) | Manual |
| Field selection | Yes (client picks fields) | Yes (tool parameters) | No (full response) |
| External access | Yes | Yes | No (internal only) |
| Discovery | Schema introspection | Tool list enumeration | Capability queries |
| NL query support | Yes (query classification) | Yes (query classification) | No |

## Key Points

- All three patterns read from the same underlying knowledge graph
- Consistency is eventually consistent regardless of access pattern
- MCP wraps GraphQL capabilities with bounded tool definitions and structured audit
- GraphRAG (community search) and PathRAG (structural traversal) are available through GraphQL and MCP,
  not NATS Direct currently

## GraphRAG vs PathRAG

| Pattern | Use When | Returns |
|---------|----------|---------|
| **GraphRAG** | Discovery, Q&A, "what does this NPC know about X?" | Community-scoped results with summaries |
| **PathRAG** | Impact analysis, "what's connected to this artifact?" | Bounded traversal from known entity |

Read `~/Code/c360/semstreams/docs/concepts/11-query-access.md` for full documentation.
Read `~/Code/c360/semstreams/docs/concepts/09-graphrag-pattern.md` and
`~/Code/c360/semstreams/docs/concepts/10-pathrag-pattern.md` for retrieval details.
