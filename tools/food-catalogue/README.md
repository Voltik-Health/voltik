# Food catalogue (INSA)

`generate.py` turns the Portuguese food composition table into the migration
`database/migrations/000004_insa_food_catalogue`. Nobody types foods by hand.

- **Source:** Base de Dados da Composição de Alimentos, v 7.1 (2026), of the Instituto Nacional de Saúde Doutor Ricardo Jorge (INSA), downloaded from [PortFIR](https://portfir.insa.min-saude.pt/).
- **Contents:** 1376 foods, 238 of them Portuguese dishes (caldo verde, bacalhau à Brás, francesinha, pastel de nata…), with values per 100 g of edible part (per 100 ml for alcoholic drinks).
- **Terms of use:** they are in the spreadsheet itself. The data is free to use, but the source must appear **visibly wherever it is shown**: "Fonte: Base de Dados da Composição de Alimentos. Instituto Nacional de Saúde Doutor Ricardo Jorge, I. P. - INSA. v 7.1 - 2026". The API returns it in `source_attribution`, and the apps show it next to the food.

```bash
python3 tools/food-catalogue/generate.py                  # downloads the table
python3 tools/food-catalogue/generate.py insa_tca.xlsx    # or uses a copy
```

The script:

- checks the SHA-256 of the spreadsheet and the names of its columns. A new version of the table needs a new migration, because a migration that ran is never edited.
- gives each food and serving a fixed id (UUID v5 of the INSA code).
- maps the 22 INSA groups to 21 shorter categories (`CATEGORIES`). Dishes become `composite_dish`.
- keeps the database rules: sugars ≤ carbohydrates, saturated fat ≤ fat. The banana has saturated fat 0.1 g with total fat 0 g, so it is stored with 0.

## Household servings

Every food gets "100 g" (or "100 ml"). `portions.csv` adds household servings for about 70 common foods, such as "1 pão (50 g)", "1 prato (300 g)" or "1 pastel (60 g)", worked out from the values per 100 g.

**These weights are estimates of typical portions and must be reviewed by someone with nutrition training before production.** To change one, edit `portions.csv` and generate a new migration.
