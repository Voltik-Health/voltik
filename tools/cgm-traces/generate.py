# /// script
# requires-python = "==3.12.*"
# dependencies = [
#     "simglucose==0.2.11",
#     # gym 0.9.4 (a dependency of simglucose) needs distutils and pkg_resources,
#     # which only setuptools < 81 still provides on Python 3.12 (see ml/README.md)
#     "setuptools<81",
# ]
# ///
"""Generates the CGM traces that the staging simulator replays (DVT-79).

    uv run tools/cgm-traces/generate.py

Each trace is 30 days of glucose of one virtual patient of simglucose (the
UVA/Padova type 1 diabetes simulator, accepted by the FDA for preclinical
studies), with random meals and a basal-bolus controller, sampled every
5 minutes. The output is backend/internal/simulator/traces/<name>.csv.

The seeds are fixed, so the traces are always the same. DVT-81 must train
and test the prediction model with other seeds (see ml/README.md).
"""
import csv
import pathlib
import sys
import warnings
from datetime import datetime, timedelta

warnings.filterwarnings("ignore")

from simglucose.actuator.pump import InsulinPump  # noqa: E402
from simglucose.controller.basal_bolus_ctrller import BBController  # noqa: E402
from simglucose.patient.t1dpatient import T1DPatient  # noqa: E402
from simglucose.sensor.cgm import CGMSensor  # noqa: E402
from simglucose.simulation.env import T1DSimEnv  # noqa: E402
from simglucose.simulation.scenario_gen import RandomScenario  # noqa: E402
from simglucose.simulation.sim_engine import SimObj  # noqa: E402

# name of the trace -> (simglucose patient, seed). Seeds 7901-7999 are DVT-79's.
TRACES = {
    "adult-a": ("adult#001", 7901),
    "adult-b": ("adult#005", 7902),
    "adult-c": ("adult#009", 7903),
    "adolescent-a": ("adolescent#003", 7904),
}
DAYS = 30
STEP = 5  # minutes
START = datetime(2026, 1, 5, 0, 0, 0)  # a Monday; only the time of day matters

OUT = pathlib.Path(__file__).resolve().parents[2] / "backend" / "internal" / "simulator" / "traces"


def simulate(patient_name, seed, workdir):
    patient = T1DPatient.withName(patient_name)
    sensor = CGMSensor.withName("Dexcom", seed=seed)
    pump = InsulinPump.withName("Insulet")
    scenario = RandomScenario(start_time=START, seed=seed)
    sim = SimObj(T1DSimEnv(patient, sensor, pump, scenario), BBController(), timedelta(days=DAYS),
                 animate=False, path=str(workdir))
    sim.simulate()
    return sim.results()["CGM"]


def resample(cgm):
    """Values every 5 minutes from START, by linear interpolation."""
    times = [(t - START).total_seconds() / 60 for t in cgm.index]
    values = list(cgm.values)
    out, j = [], 0
    for minute in range(0, DAYS * 24 * 60, STEP):
        while j + 1 < len(times) and times[j + 1] <= minute:
            j += 1
        if j + 1 < len(times) and times[j + 1] > times[j]:
            f = (minute - times[j]) / (times[j + 1] - times[j])
            v = values[j] + f * (values[j + 1] - values[j])
        else:
            v = values[j]
        out.append(min(400, max(40, round(v))))  # the range a CGM sensor reports
    return out


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    workdir = pathlib.Path(__file__).resolve().parent / ".cache"
    workdir.mkdir(exist_ok=True)
    for name, (patient, seed) in TRACES.items():
        values = resample(simulate(patient, seed, workdir))
        with (OUT / f"{name}.csv").open("w", newline="") as f:
            w = csv.writer(f)
            w.writerow(["minute", "glucose_mg_dl"])
            for i, v in enumerate(values):
                w.writerow([i * STEP, v])
        n = len(values)
        low = sum(v < 70 for v in values) / n * 100
        high = sum(v > 180 for v in values) / n * 100
        print(f"{name} ({patient}, seed {seed}): {n} values, min {min(values)}, max {max(values)}, "
              f"<70: {low:.1f} %, >180: {high:.1f} %", file=sys.stderr)


if __name__ == "__main__":
    main()
