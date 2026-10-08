-- =============================================================================
-- VOLTIK · FICTITIOUS data for development and staging (never in production)
-- Run as voltik_migrations after the migrations have been applied.
-- Every user signs in with an external provider: there are no passwords.
-- =============================================================================
BEGIN;

INSERT INTO organizations (id, company_name, tax_id, industry, seat_count, contract_start_date, contract_end_date)
VALUES ('10000000-0000-4000-8000-000000000001', 'Metalúrgica do Vale (fictitious)', '500000001', 'Metalworking', 40, '2026-01-01', '2026-12-31');

INSERT INTO users (id, email, full_name) VALUES
 ('20000000-0000-4000-8000-000000000001', 'manuel.sousa@example.com', 'Manuel Sousa'),
 ('20000000-0000-4000-8000-000000000002', 'ines.sousa@example.com', 'Inês Sousa'),
 ('20000000-0000-4000-8000-000000000003', 'ana.ribeiro@example.com', 'Ana Ribeiro'),
 ('20000000-0000-4000-8000-000000000004', 'luis.teixeira@example.com', 'Luís Teixeira');

INSERT INTO external_identities (user_id, provider, provider_subject, provider_email) VALUES
 ('20000000-0000-4000-8000-000000000001', 'google',    'google-sub-0001',    'manuel.sousa@example.com'),
 ('20000000-0000-4000-8000-000000000002', 'apple',     'apple-sub-0002',     'ines.sousa@example.com'),
 ('20000000-0000-4000-8000-000000000003', 'microsoft', 'microsoft-sub-0003', 'ana.ribeiro@example.com'),
 ('20000000-0000-4000-8000-000000000004', 'microsoft', 'microsoft-sub-0004', 'luis.teixeira@example.com');

INSERT INTO user_profiles (user_id, profile_type, organization_id) VALUES
 ('20000000-0000-4000-8000-000000000001', 'patient', NULL),
 ('20000000-0000-4000-8000-000000000002', 'caregiver', NULL),
 ('20000000-0000-4000-8000-000000000003', 'health_professional', NULL),
 ('20000000-0000-4000-8000-000000000004', 'company_manager', '10000000-0000-4000-8000-000000000001');

INSERT INTO patients (id, user_id, organization_id, birth_date, occupation, diabetes_type, current_therapy)
VALUES ('30000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', '1968-03-02', 'Metalworker', 'type_1', 'multiple_daily_injections');

INSERT INTO emergency_contacts (patient_id, full_name, relationship, phone)
VALUES ('30000000-0000-4000-8000-000000000001', 'Inês Sousa', 'Daughter', '+351910000000');

INSERT INTO caregiver_links (patient_id, caregiver_user_id, permission_level)
VALUES ('30000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000002', 'full_access');

INSERT INTO b2c_subscriptions (user_id, beneficiary_patient_id, plan, monthly_price)
VALUES ('20000000-0000-4000-8000-000000000002', '30000000-0000-4000-8000-000000000001', 'premium_individual', 9.99);

INSERT INTO health_professionals (id, user_id, license_number, specialty)
VALUES ('40000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000003', 'LIC-00001', 'endocrinology');

INSERT INTO patient_professional_assignments (patient_id, professional_id)
VALUES ('30000000-0000-4000-8000-000000000001', '40000000-0000-4000-8000-000000000001');

INSERT INTO devices (id, patient_id, device_type, brand_model)
VALUES ('50000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', 'cgm_sensor', 'Voltik BLE simulator');

-- 3 hours of readings, one per minute, with a gentle drop at the end
INSERT INTO glucose_readings (patient_id, device_id, measured_at, value_mg_dl, trend, idempotency_key)
SELECT '30000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001',
       now() - make_interval(mins => 180 - g),
       round(125 + 12 * sin(g / 20.0) - greatest(0, g - 150) * 0.4)::int,
       CASE WHEN g > 150 THEN 'falling' ELSE 'stable' END,
       'seed-' || g
FROM generate_series(1, 180) AS g;

INSERT INTO foods (id, food_name, data_source, is_verified) VALUES
 ('60000000-0000-4000-8000-000000000001', 'Arroz de pato', 'portuguese_traditional', TRUE);
INSERT INTO food_servings (id, food_id, serving_description, carbs_g, calories_kcal)
VALUES ('61000000-0000-4000-8000-000000000001', '60000000-0000-4000-8000-000000000001', '1 plate', 65, 620);

INSERT INTO meals (id, patient_id, meal_type, meal_date, status, eaten_at, total_carbs_g, total_calories_kcal)
VALUES ('70000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', 'lunch', current_date, 'eaten', now() - interval '2 hours', 65, 620);
INSERT INTO meal_items (meal_id, serving_id, serving_quantity, carbs_g, calories_kcal)
VALUES ('70000000-0000-4000-8000-000000000001', '61000000-0000-4000-8000-000000000001', 1, 65, 620);

INSERT INTO insulin_logs (patient_id, meal_id, insulin_type, dose_units, administered_at)
VALUES ('30000000-0000-4000-8000-000000000001', '70000000-0000-4000-8000-000000000001', 'rapid_bolus', 6.5, now() - interval '2 hours 5 minutes');

INSERT INTO chat_conversations (patient_id, channel_type, professional_id, topic)
VALUES ('30000000-0000-4000-8000-000000000001', 'patient_doctor', '40000000-0000-4000-8000-000000000001', 'Follow-up');

INSERT INTO consents (user_id, consent_type, document_version) VALUES
 ('20000000-0000-4000-8000-000000000001', 'terms_of_service', '1.0'),
 ('20000000-0000-4000-8000-000000000001', 'health_data_processing', '1.0'),
 ('20000000-0000-4000-8000-000000000001', 'external_ai_processing', '1.0');

COMMIT;
