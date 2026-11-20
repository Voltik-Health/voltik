-- Removes the INSA catalogue of 000004. It fails while a meal or a dish still
-- uses one of these foods (meal_items and food_components are ON DELETE RESTRICT).

BEGIN;

DELETE FROM foods WHERE data_source = 'insa';

DELETE FROM food_categories c
 WHERE c.name IN ('Alimentação infantil', 'Açúcar, doces e sobremesas', 'Batata e tubérculos', 'Bebidas alcoólicas', 'Café, chá e cacau', 'Caracóis e insetos', 'Carne e derivados', 'Dietas especiais e suplementos', 'Fruta', 'Ingredientes e aditivos', 'Legumes e hortícolas', 'Leguminosas, frutos secos e especiarias', 'Leite e derivados', 'Molhos e temperos', 'Ovos', 'Peixe e marisco', 'Pratos e sopas', 'Pão, cereais e bolos', 'Sumos e néctares', 'Água e refrigerantes', 'Óleos e gorduras')
   AND NOT EXISTS (SELECT 1 FROM foods f WHERE f.category_id = c.id);

COMMIT;
