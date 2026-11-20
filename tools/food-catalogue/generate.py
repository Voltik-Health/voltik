#!/usr/bin/env python3
"""Generates the migration that loads the Portuguese food composition table
(INSA, Base de Dados da Composição de Alimentos, v 7.1 - 2026) into the
Voltik food catalogue.

    python3 tools/food-catalogue/generate.py [path/to/insa_tca.xlsx]

Without a path it downloads the table from INSA. The file must match
INSA_SHA256: a new version of the table needs a new migration, because a
migration that already ran is never edited.

Only the Python standard library is used.
"""
import csv
import hashlib
import pathlib
import re
import sys
import urllib.request
import uuid
import zipfile
import xml.etree.ElementTree as ET
from decimal import ROUND_HALF_UP, Decimal

INSA_URL = "https://portfir.insa.min-saude.pt/wp-content/uploads/2025/11/insa_tca.xlsx"
INSA_SHA256 = "155d233b6e16e21848d766bb9a13c1965b8c990b6cd8e69e3f39f4df75c066ca"
ATTRIBUTION = (
    "Fonte: Base de Dados da Composição de Alimentos. Instituto Nacional de Saúde "
    "Doutor Ricardo Jorge, I. P. - INSA. v 7.1 - 2026"
)

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parent.parent
MIGRATIONS = REPO / "database" / "migrations"
UP_SQL = MIGRATIONS / "000004_insa_food_catalogue.up.sql"
DOWN_SQL = MIGRATIONS / "000004_insa_food_catalogue.down.sql"
PORTIONS = HERE / "portions.csv"

# Fixed ids: the same INSA food always gets the same id.
NAMESPACE = uuid.uuid5(uuid.NAMESPACE_URL, "https://portfir.insa.min-saude.pt/bdca")

# INSA groups (level 1, FoodEx2) and the shorter category shown in the apps.
CATEGORIES = {
    "Pratos compostos": "Pratos e sopas",
    "Carne e produtos cárneos": "Carne e derivados",
    "Peixes, mariscos, anfíbios, répteis e invertebrados": "Peixe e marisco",
    "Anfíbios, répteis, e invertebrados terrestres": "Caracóis e insetos",
    "Leite e produtos lácteos": "Leite e derivados",
    "Cereais e produtos à base de cereais": "Pão, cereais e bolos",
    "Produtos hortícolas e derivados": "Legumes e hortícolas",
    "Frutos e produtos derivados de frutos": "Fruta",
    "Leguminosas, frutos de casca rija, sementes oleaginosas e especiarias": "Leguminosas, frutos secos e especiarias",
    "Leguminosas, nozes, oleaginosas e especiarias.": "Leguminosas, frutos secos e especiarias",
    "Bebidas alcoólicas": "Bebidas alcoólicas",
    "Temperos, molhos e condimentos": "Molhos e temperos",
    "Óleos e gorduras de origem animal e vegetal e seus derivados": "Óleos e gorduras",
    "Produtos para dietas não padronizadas, substitutos de alimentos e suplementos alimentares": "Dietas especiais e suplementos",
    "Açúcar e similares, confeitaria e sobremesas doces à base de água": "Açúcar, doces e sobremesas",
    "Sumos e néctares de frutos e produtos hortícolas (incluindo concentrados)": "Sumos e néctares",
    "Ovos e ovoprodutos": "Ovos",
    "Café, cacau, chá e tisanas": "Café, chá e cacau",
    "Água e bebidas à base de água": "Água e refrigerantes",
    "Raízes amiláceas ou tubérculos e seus produtos, plantas sacarinas": "Batata e tubérculos",
    "Ingredientes principais isolados, aditivos, aromas, fermentos e auxiliares tecnológicos": "Ingredientes e aditivos",
    "Produtos alimentares para a população jovem": "Alimentação infantil",
}
DISH_GROUP = "Pratos compostos"
# INSA gives values per 100 g of edible part, except alcoholic drinks: per 100 ml.
ML_GROUP = "Bebidas alcoólicas"

# Column of each value in the first sheet, and the start of its header.
COLUMNS = {
    "code": ("A", "cod"),
    "name": ("B", "nome do alimento"),
    "group": ("C", "nível 1"),
    "calories_kcal": ("F", "energia [kcal]"),
    "fat_g": ("H", "lípidos [g]"),
    "saturated_fat_g": ("I", "ácidos gordos saturados [g]"),
    "carbs_g": ("N", "hidratos de carbono [g]"),
    "sugars_g": ("O", "açúcares [g]"),
    "fiber_g": ("S", "fibra [g]"),
    "protein_g": ("T", "proteínas [g]"),
    "sodium_mg": ("AS", "sódio [mg]"),
}
NUTRIENTS = ["carbs_g", "fiber_g", "sugars_g", "calories_kcal", "protein_g", "fat_g", "saturated_fat_g", "sodium_mg"]
# Upper limits of food_servings (migration 000002)
LIMITS = {"carbs_g": 1000, "fiber_g": 1000, "sugars_g": 1000, "calories_kcal": 9000,
          "protein_g": 1000, "fat_g": 1000, "saturated_fat_g": 1000, "sodium_mg": 50000}

NS = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
CENT = Decimal("0.01")


def fail(message):
    sys.exit("generate.py: " + message)


def load_xlsx(path):
    """Reads the first sheet as a list of {column letter: text} rows."""
    with zipfile.ZipFile(path) as z:
        strings = []
        root = ET.fromstring(z.read("xl/sharedStrings.xml"))
        for si in root.findall("m:si", NS):
            strings.append("".join(t.text or "" for t in si.iter("{%s}t" % NS["m"])))
        sheet = ET.fromstring(z.read("xl/worksheets/sheet1.xml"))
    rows = []
    for r in sheet.iter("{%s}row" % NS["m"]):
        row = {}
        for c in r.findall("m:c", NS):
            col = re.match(r"[A-Z]+", c.get("r")).group()
            v = c.find("m:v", NS)
            if c.get("t") == "s" and v is not None:
                row[col] = strings[int(v.text)]
            elif c.get("t") == "inlineStr":
                row[col] = "".join(t.text or "" for t in c.iter("{%s}t" % NS["m"]))
            else:
                row[col] = v.text if v is not None else None
        rows.append(row)
    return rows


def normalise(text):
    return re.sub(r"\s+", " ", (text or "").strip().lower())


def check_headers(header):
    for field, (col, start) in COLUMNS.items():
        if not normalise(header.get(col)).startswith(start):
            fail(f"column {col} should be '{start}' ({field}), found {header.get(col)!r}: the INSA file changed")


def number(text, field, name):
    try:
        value = Decimal(str(text).strip())
    except Exception:
        fail(f"{name}: {field} is not a number ({text!r})")
    if value < 0:
        fail(f"{name}: {field} is negative")
    return value


def scaled(per100, amount):
    return {k: (v * Decimal(amount) / 100).quantize(CENT, rounding=ROUND_HALF_UP) for k, v in per100.items()}


def sql(text):
    return "'" + text.replace("'", "''") + "'"


def serving_row(food_id, description, amount, unit, values):
    for k, v in values.items():
        if v > LIMITS[k]:
            fail(f"{description}: {k} = {v} is above the database limit")
    sid = uuid.uuid5(NAMESPACE, f"serving:{food_id}:{description}")
    nums = ", ".join(str(values[k]) for k in NUTRIENTS)
    return f"({sql(str(sid))}, {sql(str(food_id))}, {sql(description)}, {Decimal(amount).quantize(CENT)}, {sql(unit)}, {nums})"


def main():
    if len(sys.argv) > 1:
        path = pathlib.Path(sys.argv[1])
    else:
        path = HERE / ".cache" / "insa_tca.xlsx"
        if not path.exists():
            path.parent.mkdir(exist_ok=True)
            print("downloading", INSA_URL)
            urllib.request.urlretrieve(INSA_URL, path)
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if digest != INSA_SHA256:
        fail(f"{path} has SHA-256 {digest}, expected {INSA_SHA256}. A new INSA version needs a new migration.")

    rows = load_xlsx(path)
    check_headers(rows[1])
    foods = []
    for row in rows[2:]:
        code, name = (row.get("A") or "").strip(), (row.get("B") or "").strip()
        if not code or not name:
            continue
        group = (row.get("C") or "").strip()
        if group not in CATEGORIES:
            fail(f"{name}: unknown INSA group {group!r}; add it to CATEGORIES")
        per100 = {k: number(row.get(COLUMNS[k][0]), k, name) for k in NUTRIENTS}
        fixes = []
        if per100["saturated_fat_g"] > per100["fat_g"]:
            fixes.append(f"saturated fat {per100['saturated_fat_g']} > fat {per100['fat_g']}: kept at the fat value")
            per100["saturated_fat_g"] = per100["fat_g"]
        if per100["sugars_g"] > per100["carbs_g"]:
            fixes.append(f"sugars {per100['sugars_g']} > carbohydrates {per100['carbs_g']}: kept at the carbohydrate value")
            per100["sugars_g"] = per100["carbs_g"]
        for f in fixes:
            print(f"  fixed {name} ({code}): {f}")
        if len(name) > 200:
            fail(f"{name}: longer than 200 characters")
        foods.append({
            "code": code, "name": name, "group": group,
            "id": uuid.uuid5(NAMESPACE, f"food:{code}"),
            "unit": "ml" if group == ML_GROUP else "g",
            "per100": per100,
        })
    by_code = {f["code"]: f for f in foods}
    if len(by_code) != len(foods):
        fail("repeated INSA codes")

    portions = {}
    with PORTIONS.open(newline="", encoding="utf-8") as fh:
        for p in csv.DictReader(fh):
            food = by_code.get(p["insa_code"])
            if food is None:
                fail(f"portions.csv: no INSA food with code {p['insa_code']}")
            portions.setdefault(food["code"], []).append((p["serving_description"].strip(), Decimal(p["amount"])))

    foods.sort(key=lambda f: (f["name"].casefold(), f["code"]))
    categories = sorted(set(CATEGORIES.values()))
    food_rows, serving_rows = [], []
    for f in foods:
        food_type = "composite_dish" if f["group"] == DISH_GROUP else "generic"
        category = CATEGORIES[f["group"]]
        food_rows.append(
            f"({sql(str(f['id']))}, {sql(f['name'])}, '{food_type}', 'insa', "
            f"(SELECT id FROM food_categories WHERE name = {sql(category)}), TRUE)")
        base = f"100 {f['unit']}"
        serving_rows.append(serving_row(f["id"], base, 100, f["unit"], scaled(f["per100"], 100)))
        for description, amount in portions.get(f["code"], []):
            if description == base:
                fail(f"portions.csv: {f['name']} already has the serving {base}")
            serving_rows.append(serving_row(f["id"], description, amount, f["unit"], scaled(f["per100"], amount)))

    header = (
        "-- Generated by tools/food-catalogue/generate.py from the INSA food composition table.\n"
        "-- Do not edit by hand: change the script or portions.csv and generate a new migration.\n"
        f"-- {ATTRIBUTION}\n"
        "-- The apps must show this source wherever these values appear (INSA terms of use).\n"
    )
    up = [header + "BEGIN;\n",
          f"-- {len(foods)} foods with values per 100 g (100 ml for alcoholic drinks), "
          f"and {sum(len(v) for v in portions.values())} household servings from portions.csv.\n",
          "INSERT INTO food_categories (id, name) VALUES\n"
          + ",\n".join(f"    ({sql(str(uuid.uuid5(NAMESPACE, 'category:' + c)))}, {sql(c)})" for c in categories)
          + "\nON CONFLICT (name) DO NOTHING;\n",
          "INSERT INTO foods (id, food_name, food_type, data_source, category_id, is_verified) VALUES\n"
          + ",\n".join("    " + r for r in food_rows) + ";\n",
          "INSERT INTO food_servings (id, food_id, serving_description, metric_amount, metric_unit,\n"
          "                           carbs_g, fiber_g, sugars_g, calories_kcal, protein_g, fat_g,\n"
          "                           saturated_fat_g, sodium_mg) VALUES\n"
          + ",\n".join("    " + r for r in serving_rows) + ";\n",
          "COMMIT;\n"]
    down = [
        "-- Removes the INSA catalogue of 000004. It fails while a meal or a dish still\n"
        "-- uses one of these foods (meal_items and food_components are ON DELETE RESTRICT).\n",
        "BEGIN;\n",
        "DELETE FROM foods WHERE data_source = 'insa';\n",
        "DELETE FROM food_categories c\n WHERE c.name IN ("
        + ", ".join(sql(c) for c in categories)
        + ")\n   AND NOT EXISTS (SELECT 1 FROM foods f WHERE f.category_id = c.id);\n",
        "COMMIT;\n",
    ]
    UP_SQL.write_text("\n".join(up), encoding="utf-8")
    DOWN_SQL.write_text("\n".join(down), encoding="utf-8")
    print(f"{len(foods)} foods, {len(serving_rows)} servings, {len(categories)} categories")
    print("wrote", UP_SQL.relative_to(REPO), "and", DOWN_SQL.name)


if __name__ == "__main__":
    main()
