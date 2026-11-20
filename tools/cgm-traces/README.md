# CGM traces of the simulator

`generate.py` creates the glucose curves that the staging simulator replays
(`backend/internal/simulator`, ADR 0009). They are generated once, offline,
and the CSV files are committed: the API never runs Python.

```bash
uv run tools/cgm-traces/generate.py     # about 5 minutes
```

`uv` reads the dependencies from the top of the script: Python 3.12, `simglucose==0.2.11` and `setuptools<81`. The pin on setuptools is needed: simglucose depends on gym 0.9.4, which imports `distutils` and `pkg_resources` (see `ml/README.md`).

Each trace is 30 days of one virtual patient of simglucose (UVA/Padova type 1 simulator), with random meals and a basal-bolus controller, every 5 minutes, limited to 40–400 mg/dL like a real sensor:

| Trace | Patient | Seed | Min–max (mg/dL) | Below 70 | Above 180 |
|---|---|---|---|---|---|
| `adult-a` | adult#001 | 7901 | 40–243 | 6.2 % | 3.2 % |
| `adult-b` | adult#005 | 7902 | 53–206 | 0.6 % | 1.2 % |
| `adult-c` | adult#009 | 7903 | 40–226 | 25.4 % | 3.0 % |
| `adolescent-a` | adolescent#003 | 7904 | 40–233 | 3.0 % | 8.7 % |

- **Hypoglycaemias:** the traces have real ones, so the demo shows `critical_low` alerts.
- **Highs:** they never reach 250 mg/dL. To show a `critical_high` alert, lower the demo patient's `critical_high_mg_dl`, for example to 220.
- **Seeds 7901–7999 are DVT-79's.** The prediction model (DVT-81) must be trained and tested with other seeds, so the demo data is not data it has already seen.
