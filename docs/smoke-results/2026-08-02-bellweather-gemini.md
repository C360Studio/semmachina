# Bellweather Gemini Smoke — 2026-08-02

## Result

**Passed.** The operator explicitly authorized this paid smoke. The successful retry ran for
approximately 59 seconds on 2026-08-02 in `America/Chicago` and completed both bounded provider
turn chains without a queue wedge.

This is paid-run evidence, separate from deterministic mock-model acceptance. Provider billing
detail was not printed or captured.

## Command and isolation

The operator ran the repository's fixed smoke target:

```sh
task smoke:gemini:bellweather
```

The run used a fresh timestamp-suffixed Bellweather world namespace and a temporary isolated NATS
instance. The temporary NATS instance was stopped and automatically removed after the run. The
pre-existing `semdev-nats` instance was not touched.

## Pre-ingress correction

The first local boot attempt failed before player ingress because the configured content-bucket
name exceeded the 32-byte ObjectStore reference budget. It made no player submission and no
provider call.

The configuration was corrected to the reference-safe `BELLWEATHER_GEMINI_SMOKE` bucket, and its
exact value and maximum length were pinned by the smoke contract test before the authorized retry.

## Successful retry evidence

All times below include the local UTC-05:00 offset.

| Time | Authoritative evidence |
|---|---|
| `2026-08-02T01:09:30-05:00` | Production engine started. |
| After boot | The body-observation turn was accepted through player WebSocket ingress. |
| `2026-08-02T01:09:58-05:00` | The authoritative case phase reached `discovery`. |
| After discovery | The explicit Kit-hint turn was accepted through player WebSocket ingress. |
| `2026-08-02T01:10:29-05:00` | Final success reported `phase=complete`. |

The final success evidence also reported:

- `companion_kind=hint`;
- `provider_turns=2`;
- no 60-second phase/queue wedge;
- both terminal player deliveries and the persisted Kit-hint route accepted by the driver.

The successful live interval from engine start to final success was approximately 59 seconds,
within the three-minute absolute timeout.

## Secret and cost record

The evidence captured no Gemini API key, `.env` content, player bearer credential, prompt or
request body, or provider response body. Provider billing and token detail were not printed or
captured, so this record makes no exact cost claim. The first failed boot made no provider call;
the successful retry reported the two bounded provider turn chains above.

## Teardown

The smoke stopped its production engine and connections normally. The temporary isolated NATS
instance was stopped and auto-removed. No cleanup action was applied to the pre-existing
`semdev-nats` instance.
