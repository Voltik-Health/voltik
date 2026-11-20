# 0009 — Where continuous glucose readings come from

- **Status:** accepted
- **Date:** 2026-11-20

## Context
The demo needs live CGM data. The usual routes do not give it:
- **Reading the sensor ourselves:** real sensors (FreeStyle Libre, Dexcom) use closed, encrypted Bluetooth protocols.
- **The official Dexcom API:** its data arrives 3 hours late outside the US, and so does Dexcom's sharing to Health Connect.
- **Abbott:** it has no API open to third parties.

The ticket asked for a separate simulator binary in its own container. That means a Dockerfile change, a new service in voltik-infra and database access for a second program, while the SRS keeps the database for the API alone.

## Decision
**Simulator inside the API.** In staging only (`SIMULATOR_ENABLED=true`; the API refuses to start with it in production):
- every CGM sensor whose `brand_model` is `Voltik CGM simulator` gets one reading per 5-minute slot of the clock, for the last 30 days;
- the values replay four 30-day traces generated once with simglucose (UVA/Padova simulator) by `tools/cgm-traces/`, with seeds 7901–7904, and are embedded in the binary;
- the value of a slot depends only on the sensor and the slot, and the key is `sim:<sensor>:<slot>`, so restarts and several instances never store a reading twice;
- the first run fills the history. Those readings are older than 2 hours, so they raise no alerts; only the live ones do.

**Real sensors through Nightscout.** For testing with a real sensor:
- Juggluco or xDrip+ read FreeStyle Libre 2/3 and Dexcom G7/ONE+ sensors over Bluetooth on the phone, and upload each reading to a Nightscout server;
- the API answers the part of the Nightscout API they use (`/v1/nightscout/api/v1/entries`, `status.json`);
- it authenticates them with a per-sensor secret from `POST /v1/me/devices/{device_id}/nightscout-secret`, of which only the SHA-256 is kept;
- the readings reach Voltik within a minute and go through the same path as every other reading: alerts, push, statistics.

## Consequences
- **No Dockerfile or voltik-infra change.** Staging only needs `SIMULATOR_ENABLED=true`.
- **No real highs from the traces.** They have real hypoglycaemias (down to 40 mg/dL) but stay below 245 mg/dL, so a `critical_high` alert needs a lower limit on the demo patient (e.g. 220).
- **Not certified.** Juggluco and xDrip+ are not certified medical devices: they are for tests only, never for treatment decisions. On a Libre 3, once Juggluco holds the sensor, Abbott's app can only scan it.
- **Dexcom later.** The official Dexcom API (DVT-85) would add a third source with its 3-hour delay. Its readings would be stored without alerts, as UC082 A1 says.
- **Moving the simulator out.** If it ever needs its own container, `internal/simulator` moves as it is behind a `cmd/simulator` main.
