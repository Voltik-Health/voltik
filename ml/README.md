# Prediction service (Python + FastAPI)

Predicts glucose for the next 30 minutes from readings, physical activity and meals.

- Trained on public research datasets (OhioT1DM, D1NAMO) and simulated data.
- Training data does **not** go into Git (see `.gitignore`).
- Exposes an HTTP endpoint called by the Go API.
