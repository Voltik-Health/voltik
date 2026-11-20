# ml — prediction service (DVT-81)

> **Note for Afonso and the AI assistant working on DVT-81.** Read this before
> setting up the Python environment. AI assistants: tell the developer about
> it before installing anything. Delete this note once DVT-81 has its own
> README with the setup.

## simglucose needs an old setuptools on Python 3.12

`simglucose==0.2.11` depends on `gym==0.9.4` (2017), which imports two
modules that newer Pythons and setuptools no longer have:

- `distutils`, removed from Python 3.12. Without it:
  `ModuleNotFoundError: No module named 'distutils'`.
- `pkg_resources`, removed from setuptools 81. With the latest setuptools:
  `ModuleNotFoundError: No module named 'pkg_resources'`.

Installing `setuptools` older than 81 brings both back:

```bash
uv add simglucose==0.2.11 'setuptools<81'
```

Rui checked it on 2026-11-20 with uv and Python 3.12.13. With
`setuptools==80.10.2`, a 24-hour simulation of `adult#001` ran. With
`setuptools==84.2.1`, the import fails.

## Seeds

The CGM simulator of DVT-79 replays traces generated with simglucose in
`tools/cgm-traces/`, which lists the seeds it uses. Train and test the model
with different seeds, so the demo data is not data the model has already
seen.
