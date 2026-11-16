-- =============================================================================
-- VOLTIK · FICTITIOUS data for development and staging (never in production)
-- Run as voltik_migrations after the migrations have been applied (000002).
-- Every user signs in with an external provider: there are no passwords.
-- The ids match the examples of api/openapi.yaml where both have the same row.
-- =============================================================================
BEGIN;

INSERT INTO organizations (id, company_name, tax_id, industry, seat_count, contract_start_date, contract_end_date)
VALUES ('10000000-0000-4000-8000-000000000001', 'Metalúrgica do Vale (fictitious)', '500000001', 'Metalworking', 40, '2026-01-01', '2026-12-31');

INSERT INTO users (id, email, full_name) VALUES
 ('20000000-0000-4000-8000-000000000001', 'manuel.sousa@example.com', 'Manuel Sousa'),
 ('20000000-0000-4000-8000-000000000002', 'ines.sousa@example.com', 'Inês Sousa'),
 ('20000000-0000-4000-8000-000000000003', 'ana.ribeiro@example.com', 'Ana Ribeiro'),
 ('20000000-0000-4000-8000-000000000004', 'luis.teixeira@example.com', 'Luís Teixeira');

-- One user per active provider (Apple comes in a later phase)
INSERT INTO external_identities (user_id, provider_id, provider_subject, provider_email)
SELECT v.user_id::uuid, ip.id, v.subject, v.email
  FROM (VALUES
        ('20000000-0000-4000-8000-000000000001', 'google',    'google-sub-0001',    'manuel.sousa@example.com'),
        ('20000000-0000-4000-8000-000000000002', 'facebook',  'facebook-sub-0002',  'ines.sousa@example.com'),
        ('20000000-0000-4000-8000-000000000003', 'microsoft', 'microsoft-sub-0003', 'ana.ribeiro@example.com'),
        ('20000000-0000-4000-8000-000000000004', 'microsoft', 'microsoft-sub-0004', 'luis.teixeira@example.com')
       ) AS v (user_id, provider, subject, email)
  JOIN identity_providers ip ON ip.code = v.provider;

-- Luís manages the company
INSERT INTO organization_managers (id, organization_id, user_id)
VALUES ('11000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000004');

-- Manuel is the patient
INSERT INTO patients (id, user_id, birth_date, occupation, diabetes_type_id, therapy_type_id, weight_kg, height_cm)
SELECT '30000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '1968-03-02', 'Metalworker',
       (SELECT id FROM diabetes_types WHERE code = 'type_1'),
       (SELECT id FROM therapy_types WHERE code = 'multiple_daily_injections'),
       82.5, 174;

-- Manuel joined through a company code; a second code is still free
INSERT INTO company_invite_codes (organization_id, code, status, created_by_manager_id, used_by_patient_id, created_at, expires_at, used_at) VALUES
 ('10000000-0000-4000-8000-000000000001', 'MDV-0001', 'used', '11000000-0000-4000-8000-000000000001',
  '30000000-0000-4000-8000-000000000001', now() - interval '40 days', now() + interval '50 days', now() - interval '32 days'),
 ('10000000-0000-4000-8000-000000000001', 'MDV-0002', 'available', '11000000-0000-4000-8000-000000000001',
  NULL, now() - interval '40 days', now() + interval '50 days', NULL);

INSERT INTO emergency_contacts (patient_id, full_name, relationship, phone, call_priority)
VALUES ('30000000-0000-4000-8000-000000000001', 'Inês Sousa', 'Daughter', '+351910000000', 1);

-- Inês, his daughter, is his caregiver and pays a subscription that covers him
INSERT INTO caregiver_links (id, patient_id, caregiver_user_id, permission_level, status, linked_at)
VALUES ('31000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000002',
        'full_access', 'active', now() - interval '30 days');

INSERT INTO b2c_subscriptions (id, payer_user_id, plan_id, monthly_price, start_at)
SELECT '32000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000002', sp.id, sp.monthly_price, now() - interval '30 days'
  FROM subscription_plans sp WHERE sp.code = 'premium_individual';
INSERT INTO subscription_beneficiaries (subscription_id, patient_id, added_at)
VALUES ('32000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', now() - interval '30 days');

-- Ana is his endocrinologist, available on weekday mornings
INSERT INTO health_professionals (id, user_id, license_number, specialty_id)
SELECT '40000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000003', 'LIC-00001', id
  FROM medical_specialties WHERE code = 'endocrinology';

INSERT INTO patient_professional_assignments (id, patient_id, professional_id)
VALUES ('41000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', '40000000-0000-4000-8000-000000000001');

INSERT INTO professional_availability (professional_id, weekday, start_time, end_time, slot_minutes, valid_from)
SELECT '40000000-0000-4000-8000-000000000001', d, '09:00', '13:00', 30, '2026-01-01'
  FROM generate_series(1, 5) AS d;

-- His CGM sensor and his watch
INSERT INTO devices (id, patient_id, device_type_id, brand_model, serial_or_mac, battery_percent, first_reading_at, sensor_expires_at, last_synced_at)
SELECT v.id::uuid, '30000000-0000-4000-8000-000000000001', dt.id, v.brand_model, v.serial, v.battery,
       now() - interval '3 hours', v.expires, now()
  FROM (VALUES
        ('50000000-0000-4000-8000-000000000001', 'cgm_sensor', 'Voltik CGM simulator',  'SIM-0001',          NULL::int, now() + interval '10 days'),
        ('50000000-0000-4000-8000-000000000002', 'smartwatch', 'Samsung Galaxy Watch6', 'D4:3A:2C:91:7F:10', 64,        NULL)
       ) AS v (id, device_type, brand_model, serial, battery, expires)
  JOIN device_types dt ON dt.code = v.device_type;

-- 3 hours of readings, one per minute, with a gentle drop at the end
INSERT INTO glucose_readings (patient_id, device_id, measured_at, value_mg_dl, trend, signal_quality, idempotency_key)
SELECT '30000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001',
       now() - make_interval(mins => 180 - g),
       round(125 + 12 * sin(g / 20.0) - greatest(0, g - 150) * 0.4)::int,
       CASE WHEN g > 150 THEN 'falling' ELSE 'stable' END,
       100,
       'seed-glucose-' || g
FROM generate_series(1, 180) AS g;

-- The same 3 hours of heart rate from the watch
INSERT INTO heart_rate_samples (device_id, measured_at, bpm, idempotency_key)
SELECT '50000000-0000-4000-8000-000000000002',
       now() - make_interval(mins => 180 - g),
       round(72 + 8 * sin(g / 15.0))::int,
       'seed-heart-' || g
FROM generate_series(1, 180) AS g;

INSERT INTO food_categories (id, name)
VALUES ('62000000-0000-4000-8000-000000000001', 'Pratos tradicionais');
INSERT INTO foods (id, food_name, data_source, category_id, is_verified) VALUES
 ('60000000-0000-4000-8000-000000000001', 'Arroz de pato', 'portuguese_traditional', '62000000-0000-4000-8000-000000000001', TRUE);
INSERT INTO food_servings (id, food_id, serving_description, carbs_g, calories_kcal)
VALUES ('61000000-0000-4000-8000-000000000001', '60000000-0000-4000-8000-000000000001', '1 plate', 65, 620);

INSERT INTO meals (id, patient_id, meal_type_id, meal_date, status, eaten_at, total_carbs_g, total_calories_kcal)
SELECT '70000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', id,
       current_date, 'eaten', now() - interval '2 hours', 65, 620
  FROM meal_types WHERE code = 'lunch';
INSERT INTO meal_items (meal_id, serving_id, serving_quantity, carbs_g, calories_kcal)
VALUES ('70000000-0000-4000-8000-000000000001', '61000000-0000-4000-8000-000000000001', 1, 65, 620);

INSERT INTO insulin_logs (patient_id, meal_id, insulin_type_id, dose_units, administered_at)
SELECT '30000000-0000-4000-8000-000000000001', '70000000-0000-4000-8000-000000000001', id, 6.5, now() - interval '2 hours 5 minutes'
  FROM insulin_types WHERE code = 'rapid_bolus';

INSERT INTO chat_conversations (channel_type, assignment_id, topic)
VALUES ('patient_doctor', '41000000-0000-4000-8000-000000000001', 'Follow-up');

INSERT INTO notification_tokens (id, user_id, platform, push_token)
VALUES ('90000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', 'android', 'seed-fcm-token-0001');

-- Manuel accepted version 1.0 of the two mandatory documents and of external AI processing
INSERT INTO consents (user_id, document_id, ip_address)
SELECT '20000000-0000-4000-8000-000000000001', id, '192.0.2.10'
  FROM legal_documents
 WHERE version = '1.0' AND document_type IN ('terms_of_service', 'health_data_processing', 'external_ai_processing');

COMMIT;
