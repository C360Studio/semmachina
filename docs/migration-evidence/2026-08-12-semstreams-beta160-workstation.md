# SemStreams beta.160 Workstation Migration Evidence

This record captures the token-free blue/green migration rehearsal authorized by the repository
operator in the Codex task on 2026-08-12. It proves the workstation deployment procedure; it does
not claim an external production traffic switch or paid-provider acceptance.

## Scope and authorization

- Evidence window: 2026-08-12 18:16:29Z through 18:24:31Z.
- Operator authorization: the task owner directed Codex to continue after the remaining operator
  gates were identified.
- Host posture: one SemMachina application ran at a time; all application and broker ports were
  loopback-only.
- Unrelated resource: `semboids-demo-nats` was observed and left untouched.
- No model-provider credential was supplied and no action was submitted, so this rehearsal made no
  paid model call.

## Release identities

| Evidence | Beta.159 blue | Beta.160 green |
| --- | --- | --- |
| SemMachina commit | `1cf1b936c514ccd03a140bd8a57c3da2f3eea8d0` | `53771831ea73f1a385c54a47c240a3e4a6b139b2` |
| Linked SemStreams | `v1.0.0-beta.159` | `v1.0.0-beta.160` |
| Physical content bucket | `SEMMACHINA_B159_BLUE_CONTENT` | `SEMMACHINA_B160_GREEN_CONTENT` |

- Blue binary SHA-256:
  `2623a70f2f88ccb80d90006830464641128aa8b546c563d71a150f50a0f73f19`.
- Green binary SHA-256:
  `47b47e7ef0efe23833b360c71b3e729025499e62e006aed70e0f38eeee20e421`.
- Blue configuration SHA-256:
  `af8667961bdfdc995fceabe52840453ece41dd77ecbf6092ef9aa6812ec43680`.
- Green configuration SHA-256:
  `ad2f5262a0edbe58452b5a8da8931ecddee6c4d1da84a2901bfc2a4fc8958895`.

`go mod download -json` resolved beta.160 to upstream commit
`8403a2218000e45a31c5132fbfe01af42ed04f14`, module sum
`h1:sVkaLPOqs0Jy/iyYvfamkSi9An+Vq0o/5nqMxZVCvtk=`, and tag ref
`refs/tags/v1.0.0-beta.160`. The green component configuration version is `1.1.0`; its logical
content storage instance is `objectstore`.

The credential-bearing local configurations remain in the mode-0700 operator directory
`/private/tmp/semmachina-beta160-operator-20260812T181629Z`; its JSON files use mode 0600. Only
their hashes and non-secret deployment fields are recorded here. This temporary path is local
workstation evidence, not a durable production evidence store.

## Broker and storage identities

| Evidence | Beta.159 blue | Beta.160 green |
| --- | --- | --- |
| Container | `semmachina-beta159-blue-nats` | `semmachina-beta160-green-nats` |
| Server name | `semmachina-b159-blue-20260812` | `semmachina-b160-green-20260812` |
| NATS version | `2.12.14` | `2.14.4` |
| Account | `$G` | `$G` |
| Volume | `semmachina-beta159-blue-js` | `semmachina-beta160-green-js` |
| Volume created | 2026-08-12 18:17:35Z | 2026-08-12 18:18:12Z |
| Client/monitor ports | `127.0.0.1:34222` / `127.0.0.1:38222` | `127.0.0.1:48422` / `127.0.0.1:58422` |

- Blue container ID:
  `7f3f2f84c5eb5fb055fd92c98a39bb4638a99e6a6e900ab9407e31ff86d4bd2b`.
- Green container ID:
  `0408f63c5f7629909800eeeade3771da6002bdb344a8cb0af7fb3f94fe477c5e`.
- Blue server ID: `NAYFYH45FK2ZJ4QR2HYB7ZD5IVQMMEWAMLWK7DZRBVECGKNYS3YJTCEO`.
- Green server ID: `NDMTR666RYHQQUUIXOAWYCCJFIVYOMQEOZOSAZF6W5PXQZY52AEXH4O4`.
- Blue image digest:
  `sha256:7cef1bd3fed6034e95cf6e6bc9c28c5afa6dc58e9fb778dd7924a1ac62569f2d`.
- Green image digest:
  `sha256:ecf677bae6a0ae7900bd3217be041c6614d5dcd2cae780000f9cd69462b36541`.

Both containers mounted only their named volume at `/data`. The green container used exact image
`nats:2.14.4` by immutable digest. The inspection client was the already-local
`natsio/nats-box` digest `sha256:ffce8bd103383f179f8c7f11cf645726acf5d17280706c530c3b342dbe16334c`
with CLI version `0.4.0`.

## Green freshness proof

The green volume and container names did not exist before provisioning. Before the first green
application boot, the volume creation time, container mount, server identity, and account were
recorded. Read-only NATS inspection then returned:

- `nats stream ls --json`: `null`;
- `nats kv ls`: `No Key-Value buckets found`;
- `nats object ls`: `No Object Store buckets found`;
- account usage: zero bytes, zero streams, and zero consumers; and
- `/varz`: NATS `2.14.4`, JetStream enabled, file store `/data/jetstream`, zero storage bytes.

This inspection completed before the first green application boot at 18:20:56Z. No retained state
was found, so the stop-for-review condition did not fire. Item 1.1's fresh-target requirement is
satisfied for this workstation deployment.

## Boot, restart, and storage convergence

The beta.159 rollback unit was established first. It imported its world, opened the shared
loopback application listener at 18:20:15Z, and committed blue import marker
`2026-08-12T18:20:07.043082Z`. It then exited zero on SIGTERM before the blue broker was stopped.

Green first boot:

- started at 18:20:56Z against only `127.0.0.1:48422`;
- committed campaign import marker `2026-08-12T18:20:57.277729Z`;
- reported rule state `ready`, `ready: true`, and `bootstrap_complete: true`;
- created physical object store `SEMMACHINA_B160_GREEN_CONTENT` with file storage; and
- opened the shared listener at 18:21:06Z.

The green application exited zero on SIGTERM at 18:21:58Z while its broker remained healthy. It
restarted at 18:22:17Z on the unchanged container ID and volume, skipped world re-import using the
original marker, restored fresh rule readiness, reopened the same physical object store, and opened
the listener at 18:22:27Z. The campaign seed and import timestamp were unchanged.

## Reverse-stop and teardown proof

Each application stop followed the same deployment order:

1. send SIGTERM to the application and stop ingress;
2. wait for application exit status zero and `semmachina stopped`;
3. verify the application listener is gone while the paired broker still reports healthy; and
4. gracefully stop the paired broker, retaining its volume.

No stop logged `a component barrier did not stop cleanly`. The green shutdowns emitted exactly
three `nats: invalid subscription` watcher messages followed by `Rule processor stopped`, graceful
graph component stops, and `semmachina stopped`. This is the known SemStreams issue #424 shape.

The source-level ordering proof also passed:

```text
TestComponentManagersStopInStrictReverseActivationOrder                         PASS
TestEngineStopLetsManagersReleaseOwnedConsumersBeforeGlobalClientShutdown      PASS
TestComponentManagerStartFailureUnwindsPartiallyStartedBarrierFirst             PASS
```

This proves rule, agentic, and graph managers stop in reverse activation order before global
consumer/client shutdown, including partial-start unwind.

## Rollback result and approved posture

After the converged green application exited zero, green NATS was stopped with its volume retained.
The original blue broker was restarted with only `semmachina-beta159-blue-js`, while the green
container remained stopped with only `semmachina-beta160-green-js`. The exact beta.159 binary then
opened the shared application listener at 18:23:55Z and read its original blue import marker and
campaign seed. It did not open green storage.

The workstation rollback posture exercised by the authorized rehearsal is:

- a green failure stops the green application before its broker;
- failed green state is retained for diagnosis and is not purged, converted, or reseeded;
- rollback selects the exact beta.159 application and original blue broker/account/volume together;
- neither release may open the other release's volume;
- traffic moves only after the selected unit reports readiness; and
- both rehearsal volumes remain retained until the operator separately authorizes removal after
  migration closure.

The local traffic switch was the exclusive listener `127.0.0.1:58423`; no external traffic moved.
The repository operator remains the decision owner for a real deployment switch or rollback. This
workstation result does not approve or satisfy a future production deployment's separate storage,
cutover, or rollback record.

## Final resource state

At the end of the rehearsal:

- both application processes had exited zero;
- both named NATS containers were stopped with exit status zero;
- both named volumes remained present and distinct;
- no process listened on `127.0.0.1:58423`; and
- the unrelated `semboids-demo-nats` container remained untouched.

## Existing validation and approvals

GitHub Actions run `31622902518` passed lint, build, OpenSpec, all six Go lanes, the complete
zero-skip union, frontend server/unit, browser components, Playwright UI journeys, and the aggregate
CI status check. The E2E lane passed in 9m08s. The reviewed local evidence remains:

- Go lanes: 3,025 unit/race; 1,394 integration/race; 82 pipeline/race; 59 recovery/race;
  134 acceptance; and 28 E2E outcomes;
- union: 4,722 outcomes across 32 packages, zero skips;
- frontend: 532 server-unit, 30 browser-component, and 6 Playwright UI-journey tests;
- action-free real-stack preflight: one test with all six checkpoints; and
- formal Go and Svelte reviewer approvals.

Paid acceptance remains unrun and requires separate operator authorization.
