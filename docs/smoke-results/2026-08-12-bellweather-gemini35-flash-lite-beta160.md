# Bellweather Gemini 3.5 Flash-Lite Beta.160 Smoke — 2026-08-12

## Result

**Passed.** The repository operator explicitly authorized exactly one invocation of
`task smoke:gemini:bellweather` in the Codex task on 2026-08-12. Authorization covered the two
fixed bounded provider chains and no retry. `SEMMACHINA_PAID_SMOKE=1` was scoped to the command and a
non-empty Gemini key was confirmed without recording either value.

The run started at `2026-08-12T18:51:58.027Z`, reported final success at
`2026-08-12T18:52:06.145Z`, completed in approximately 8.118 seconds, and exited zero.

## Executable and infrastructure identity

| Evidence | Recorded value |
| --- | --- |
| SemMachina commit | `c6061fd4f2c41c0fd52af4d61eda81c48ff2e715` (clean) |
| SemStreams | `v1.0.0-beta.160` at `8403a2218000e45a31c5132fbfe01af42ed04f14` |
| Configuration | `configs/instance.gemini35-flash-lite.bellweather.example.json` |
| World | `fixtures/worlds/bellweather-maze` |
| NATS server | `2.14.4`, `semmachina-b160-paid-20260812` |
| NATS server ID | `NBWMHDIJLYSSR53EHHI4FIHZ4DQOAH26TQ54EYNZWS5TUOM3UQTOMCUD` |
| NATS image digest | `sha256:ecf677bae6a0ae7900bd3217be041c6614d5dcd2cae780000f9cd69462b36541` |
| NATS container | `semmachina-beta160-paid-nats` |
| NATS volume | `semmachina-beta160-paid-js` |
| World namespace | `bellweather-gemini35-flash-lite-smoke-1786560703853200000` |
| Logical content store | `objectstore` |
| Physical content bucket | `BELLWEATHER_GEMINI_SMOKE` |

The campaign was
`c360.semmachina.bellweather-gemini35-flash-lite-smoke-1786560703853200000.bellweather-maze.campaign.main`.
The broker exposed loopback ports only. Its fresh preflight reported zero storage bytes, streams,
and consumers before application boot.

## Authoritative acceptance evidence

The accepted identities were:

- Action 1 `3IMLZX64CBJYTZ7P2H5PDP6FGY4U67RJQXWHEZZGEPEJQ2WW32GA`, turn
  `turn-3IMLZX64CBJYTZ7P2H5PDP6FGY4U67RJQXWHEZZGEPEJQ2WW32GA`.
- Action 2 `45YNUJMBHYLH3FXOQFR3X6P5W7ATXKD2FSCHZ6S2QTE66NP7PKVQ`, turn
  `turn-45YNUJMBHYLH3FXOQFR3X6P5W7ATXKD2FSCHZ6S2QTE66NP7PKVQ`.

| UTC time | Evidence |
| --- | --- |
| `18:51:58.036Z` | Action 1 accepted. |
| `18:52:00.740936Z` | Case revision 4 recorded authoritative phase `discovery`. |
| `18:52:01.646Z` | Turn 1 revision 16 proved `observe` and terminal phase `complete`. |
| `18:52:01.656Z` | The smoke's post-delivery case proof observed `discovery`. |
| `18:52:01.660Z` | Action 2 accepted. |
| `18:52:06.128071Z` | Turn 2 revision 16 proved the exact persisted Kit route and completion. |
| `18:52:06.145Z` | Final success reported phase `complete`, `companion_kind=hint`, and `provider_turns=2`. |

The command verified both terminal WebSocket deliveries, the first turn's authoritative discovery
transition, Kit's identity and narrated companion output, and the exact persisted Kit route:
`request_hint`, `player-hint`, and `case-decision`.

No 30-second periodic poll row was emitted because each turn completed before its first polling
interval. The authoritative terminal, case, and route reads above supplied the acceptance evidence.
None of the configured phase-aware no-movement budgets, 180-second per-turn caps, 390-second whole
smoke cap, 60-second delivery bounds, failure aborts, or 30-second discovery bound fired.

## Cost and secret boundary

Exact provider billing and token detail were unavailable, so this record makes no exact cost claim.
No Gemini key, `.env` content, player bearer, prompt, provider request or response body, or
credential-bearing URL was retained. No raw log artifact was retained.

## Teardown

After the run, no smoke process remained and NATS reported zero connections. Final broker state was
28 streams, 46 consumers, 1,221 messages, 1,351,339 bytes of JetStream account storage, and
1,364,587 total reported bytes. The broker stopped with exit status zero and the paid-smoke volume
was retained for evidence. The unrelated `semboids-demo-nats` container remained running and was
not modified.
