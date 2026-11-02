-- ===========================================================================
-- VOLTIK · MIGRATION 000002 · REFINED DATA MODEL (29 -> 48 tables)
-- PostgreSQL 17 · applied with golang-migrate by the voltik_migrations role
--
-- Turns the initial schema into the data model documented in the SRS
-- (chapters 6 and 7, annex B) and converts the rows that already exist:
--   · 9 enumeration tables replace the CHECK lists the business can extend
--   · roles come from the data: user_profiles is replaced by
--     organization_managers and users.is_platform_admin
--   · relations follow the natural hierarchy: teleconsultations and
--     conversations hang from the follow-up or the caregiver link, alerts
--     from their source, heart rate samples from the device
--   · 5 composite foreign keys keep related rows on the same patient
--   · 10 triggers (8 functions) enforce the rules that span tables
--   · text columns never accept blank text
-- Result: 48 tables, 64 foreign keys, 54 triggers (10 business rules plus
-- set_updated_at() on 44 tables).
--
-- Single-column CHECKs are named <table>_<column>_check, like the ones
-- PostgreSQL generated in 000001; rules over several columns use the ck_/uq_
-- names that the SRS quotes.
--
-- Everything runs in one transaction: if any statement fails, nothing changes.
-- ===========================================================================

BEGIN;

-- ---------------------------------------------------------------------------
-- 0. EXTENSION AND HELPER
-- ---------------------------------------------------------------------------
-- btree_gist lets one EXCLUDE constraint combine "=" on a UUID with "&&" on a range.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Time slot taken by a teleconsultation. IMMUTABLE so the EXCLUDE index can use it
-- (only minutes are added, so the result never depends on the time zone).
CREATE FUNCTION consultation_period(p_start TIMESTAMPTZ, p_minutes INTEGER)
RETURNS tstzrange
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT tstzrange(p_start, p_start + make_interval(mins => p_minutes))
$$;


-- ---------------------------------------------------------------------------
-- 1. ENUMERATION TABLES (SRS 6.5)
-- A new value is a new row, not a migration. Values in use are hidden with
-- is_active = FALSE, never deleted (every reference is ON DELETE RESTRICT).
-- ---------------------------------------------------------------------------
CREATE TABLE identity_providers (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(30) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO identity_providers (id, code, label, sort_order, is_active) VALUES
    (1, 'google',    'Google',    1, TRUE),
    (2, 'microsoft', 'Microsoft', 2, TRUE),
    (3, 'apple',     'Apple',     3, FALSE), -- later phase: needs the paid Apple Developer Program
    (4, 'facebook',  'Facebook',  4, TRUE);

CREATE TABLE genders (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(30) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO genders (id, code, label, sort_order) VALUES
    (1, 'male',        'Masculino',         1),
    (2, 'female',      'Feminino',          2),
    (3, 'other',       'Outro',             3),
    (4, 'undisclosed', 'Prefiro não dizer', 4);

CREATE TABLE diabetes_types (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(30) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO diabetes_types (id, code, label, sort_order) VALUES
    (1, 'type_1',      'Diabetes Tipo 1',      1),
    (2, 'type_2',      'Diabetes Tipo 2',      2),
    (3, 'gestational', 'Diabetes Gestacional', 3),
    (4, 'lada',        'Diabetes LADA',        4),
    (5, 'mody',        'Diabetes MODY',        5);

CREATE TABLE therapy_types (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(40) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO therapy_types (id, code, label, sort_order) VALUES
    (1, 'multiple_daily_injections', 'Múltiplas Injeções Diárias (Canetas)', 1),
    (2, 'insulin_pump',              'Bomba Infusora de Insulina',           2),
    (3, 'oral_medication',           'Antidiabéticos Orais',                 3),
    (4, 'diet_only',                 'Dieta e Exercício',                    4);

CREATE TABLE subscription_plans (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(30) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    monthly_price DECIMAL(10,2) NOT NULL CHECK (monthly_price BETWEEN 0.00 AND 10000.00), -- list price, in euros
    max_beneficiaries SMALLINT NOT NULL CHECK (max_beneficiaries BETWEEN 1 AND 50),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO subscription_plans (id, code, label, monthly_price, max_beneficiaries, sort_order) VALUES
    (1, 'free',               'Plano Gratuito',            0.00, 1, 1),
    (2, 'premium_individual', 'Plano Premium Individual',  9.99, 1, 2),
    (3, 'premium_family',     'Plano Premium Familiar',   19.99, 5, 3);

CREATE TABLE device_types (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(30) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO device_types (id, code, label, sort_order) VALUES
    (1, 'cgm_sensor', 'Sensor de Glicemia Contínua (CGM)', 1),
    (2, 'smartwatch', 'Smartwatch',                        2),
    (3, 'smartband',  'Pulseira Inteligente (Smartband)',  3),
    (4, 'glucometer', 'Glucómetro Capilar',                4);

CREATE TABLE meal_types (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(30) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO meal_types (id, code, label, sort_order) VALUES
    (1, 'breakfast',       'Pequeno-Almoço', 1),
    (2, 'morning_snack',   'Meio da Manhã',  2),
    (3, 'lunch',           'Almoço',         3),
    (4, 'afternoon_snack', 'Lanche',         4),
    (5, 'dinner',          'Jantar',         5),
    (6, 'evening_snack',   'Ceia',           6),
    (7, 'other',           'Outro',          7);

CREATE TABLE insulin_types (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(30) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO insulin_types (id, code, label, sort_order) VALUES
    (1, 'rapid_bolus',       'Insulina de Ação Rápida (Bólus)',       1),
    (2, 'ultra_rapid_bolus', 'Insulina de Ação Ultra-Rápida (Bólus)', 2),
    (3, 'basal',             'Insulina de Ação Lenta (Basal)',        3),
    (4, 'premixed',          'Insulina Pré-Misturada',                4);

CREATE TABLE medical_specialties (
    id SMALLINT PRIMARY KEY CHECK (id > 0),
    code VARCHAR(40) NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    label VARCHAR(80) NOT NULL CHECK (btrim(label) <> ''),
    sort_order SMALLINT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO medical_specialties (id, code, label, sort_order) VALUES
    (1, 'endocrinology',      'Endocrinologia e Nutrição', 1),
    (2, 'general_practice',   'Medicina Geral e Familiar', 2),
    (3, 'diabetes_nursing',   'Enfermagem de Diabetes',    3),
    (4, 'clinical_nutrition', 'Nutrição Clínica',          4);


-- ---------------------------------------------------------------------------
-- 2. M1 · IDENTITY, ACCESS AND COMPANIES
-- ---------------------------------------------------------------------------
-- 2.1 users: admins are flagged on the user, the email becomes optional (some
-- providers do not share it) and a deleted account is always deactivated.
ALTER TABLE users ADD COLUMN is_platform_admin BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE users u SET is_platform_admin = TRUE
  FROM user_profiles up
 WHERE up.user_id = u.id AND up.profile_type = 'admin' AND up.is_active;

UPDATE users SET account_status = 'deactivated' WHERE deleted_at IS NOT NULL;
ALTER TABLE users
    ALTER COLUMN email DROP NOT NULL,
    DROP COLUMN last_login_at, -- kept per provider in external_identities.last_login_at
    ADD CONSTRAINT ck_users_deleted_status CHECK (deleted_at IS NULL OR account_status = 'deactivated');

-- 2.2 external_identities: the provider is a row of identity_providers
ALTER TABLE external_identities ADD COLUMN provider_id SMALLINT REFERENCES identity_providers(id) ON DELETE RESTRICT;
UPDATE external_identities e SET provider_id = ip.id FROM identity_providers ip WHERE ip.code = e.provider;
ALTER TABLE external_identities
    ALTER COLUMN provider_id SET NOT NULL,
    DROP COLUMN provider, -- also drops uq_provider_subject and uq_user_provider
    ADD CONSTRAINT uq_ext_id_provider_subject UNIQUE (provider_id, provider_subject),
    ADD CONSTRAINT uq_ext_id_user_provider UNIQUE (user_id, provider_id);

-- 2.3 organizations
ALTER TABLE organizations RENAME CONSTRAINT ck_organization_contract_dates TO ck_org_contract_dates;
ALTER TABLE organizations
    DROP CONSTRAINT organizations_seat_count_check,
    ADD CONSTRAINT organizations_seat_count_check CHECK (seat_count BETWEEN 1 AND 100000);

-- 2.4 organization_managers: who manages which company (M:N)
CREATE TABLE organization_managers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the user became a manager of the company
    CONSTRAINT uq_org_manager UNIQUE (organization_id, user_id),
    CONSTRAINT uq_org_manager_id_org UNIQUE (id, organization_id) -- target of the composite FK below
);
INSERT INTO organization_managers (organization_id, user_id, created_at)
SELECT organization_id, user_id, created_at
  FROM user_profiles
 WHERE profile_type = 'company_manager' AND is_active;

-- 2.5 company_invite_codes: a code belongs to the company, is issued by one of
-- its managers and is redeemed by one patient.
ALTER TABLE company_invite_codes
    ADD COLUMN created_by_manager_id UUID,
    ADD COLUMN used_by_patient_id UUID REFERENCES patients(id) ON DELETE SET NULL;
UPDATE company_invite_codes c SET created_by_manager_id = om.id
  FROM organization_managers om
 WHERE om.user_id = c.created_by_user_id AND om.organization_id = c.organization_id;
UPDATE company_invite_codes c SET used_by_patient_id = p.id
  FROM patients p
 WHERE p.user_id = c.used_by_user_id AND p.deleted_at IS NULL;
UPDATE company_invite_codes SET used_at = NULL WHERE status <> 'used';
ALTER TABLE company_invite_codes
    DROP COLUMN created_by_user_id,
    DROP COLUMN used_by_user_id,
    DROP CONSTRAINT ck_invite_used_at,
    -- if the manager leaves, only the issuer is cleared: the code and the licence stay
    ADD CONSTRAINT fk_invite_manager_org FOREIGN KEY (created_by_manager_id, organization_id)
        REFERENCES organization_managers (id, organization_id) ON DELETE SET NULL (created_by_manager_id),
    ADD CONSTRAINT uq_company_invite_patient UNIQUE (used_by_patient_id),
    ADD CONSTRAINT ck_company_invite_state CHECK (
           (status = 'used' AND used_at IS NOT NULL)
        OR (status <> 'used' AND used_by_patient_id IS NULL AND used_at IS NULL)),
    ADD CONSTRAINT ck_company_invite_dates CHECK (
        expires_at > created_at AND (used_at IS NULL OR used_at >= created_at));

-- 2.6 Roles are now derived from the data (SRS 3.1), so user_profiles goes away:
-- patient = live row in patients, caregiver = active caregiver_links,
-- clinician = live row in health_professionals, manager = organization_managers,
-- admin = users.is_platform_admin.
DROP TABLE user_profiles;


-- ---------------------------------------------------------------------------
-- 3. M2 · PATIENT, CARE NETWORK AND SUBSCRIPTIONS
-- ---------------------------------------------------------------------------
-- 3.1 patients: clinical lists become enumeration tables. The employer is no
-- longer stored on the patient: it comes from the redeemed company code.
ALTER TABLE patients
    ADD COLUMN gender_id SMALLINT REFERENCES genders(id) ON DELETE RESTRICT,
    ADD COLUMN diabetes_type_id SMALLINT REFERENCES diabetes_types(id) ON DELETE RESTRICT,
    ADD COLUMN therapy_type_id SMALLINT REFERENCES therapy_types(id) ON DELETE RESTRICT;
UPDATE patients p SET gender_id = g.id FROM genders g WHERE g.code = p.gender;
UPDATE patients p SET diabetes_type_id = d.id FROM diabetes_types d WHERE d.code = p.diabetes_type;
UPDATE patients p SET therapy_type_id = t.id FROM therapy_types t WHERE t.code = p.current_therapy;
ALTER TABLE patients
    ALTER COLUMN diabetes_type_id SET NOT NULL,
    DROP COLUMN gender,
    DROP COLUMN diabetes_type,
    DROP COLUMN current_therapy,
    DROP COLUMN organization_id;
ALTER TABLE patients RENAME CONSTRAINT ck_patient_glucose_thresholds TO ck_patient_thresholds;
ALTER TABLE patients
    ADD CONSTRAINT patients_birth_date_check CHECK (birth_date BETWEEN DATE '1900-01-01' AND CURRENT_DATE),
    DROP CONSTRAINT patients_weight_kg_check,
    ADD CONSTRAINT patients_weight_kg_check CHECK (weight_kg BETWEEN 2.00 AND 500.00),
    DROP CONSTRAINT patients_height_cm_check,
    ADD CONSTRAINT patients_height_cm_check CHECK (height_cm BETWEEN 30 AND 260);

-- 3.2 emergency_contacts: priorities 1..10, never repeated for one patient.
-- The uniqueness is checked at commit, so two contacts can swap places (UC053).
UPDATE emergency_contacts e SET call_priority = r.position
  FROM (SELECT id, row_number() OVER (PARTITION BY patient_id ORDER BY call_priority, created_at, id) AS position
          FROM emergency_contacts) r
 WHERE r.id = e.id AND e.call_priority <> r.position;
ALTER TABLE emergency_contacts
    DROP CONSTRAINT emergency_contacts_call_priority_check,
    ADD CONSTRAINT emergency_contacts_call_priority_check CHECK (call_priority BETWEEN 1 AND 10),
    ADD CONSTRAINT uq_emergency_contact_priority UNIQUE (patient_id, call_priority) DEFERRABLE INITIALLY DEFERRED;

-- 3.3 care_invites: one-time codes the patient gives to a caregiver or a clinician
CREATE TABLE care_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    used_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    invite_type VARCHAR(20) NOT NULL CHECK (invite_type IN ('caregiver', 'health_professional')),
    code VARCHAR(8) NOT NULL CHECK (btrim(code) <> ''), -- unique among unused invites (index below)
    permission_level VARCHAR(30) CHECK (permission_level IN ('alerts_only', 'alerts_and_values', 'full_access')),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT ck_care_invite_dates CHECK (expires_at > created_at AND (used_at IS NULL OR used_at >= created_at)),
    CONSTRAINT ck_care_invite_permission CHECK ((invite_type = 'caregiver') = (permission_level IS NOT NULL)),
    CONSTRAINT ck_care_invite_used CHECK (used_by_user_id IS NULL OR used_at IS NOT NULL)
);
CREATE UNIQUE INDEX uq_care_invites_code ON care_invites (code) WHERE used_at IS NULL AND deleted_at IS NULL;

-- 3.4 caregiver_links: status and dates always agree
UPDATE caregiver_links SET linked_at = NULL, revoked_at = NULL WHERE status = 'pending_approval';
UPDATE caregiver_links SET revoked_at = NULL WHERE status = 'active';
UPDATE caregiver_links SET revoked_at = linked_at WHERE status = 'revoked' AND revoked_at < linked_at;
ALTER TABLE caregiver_links
    DROP CONSTRAINT ck_caregiver_link_dates,
    ADD CONSTRAINT ck_caregiver_dates CHECK (
           (status = 'pending_approval' AND linked_at IS NULL AND revoked_at IS NULL)
        OR (status = 'active' AND linked_at IS NOT NULL AND revoked_at IS NULL)
        OR (status = 'revoked' AND revoked_at IS NOT NULL AND (linked_at IS NULL OR revoked_at >= linked_at))),
    ADD CONSTRAINT uq_caregiver_link_id_patient UNIQUE (id, patient_id);

-- 3.5 b2c_subscriptions + subscription_beneficiaries: one subscription can cover
-- several patients (family plan). monthly_price stays: it is the price charged,
-- fixed when the user subscribed.
ALTER TABLE b2c_subscriptions
    ADD COLUMN payer_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN plan_id SMALLINT REFERENCES subscription_plans(id) ON DELETE RESTRICT;
UPDATE b2c_subscriptions s SET payer_user_id = s.user_id, plan_id = sp.id
  FROM subscription_plans sp WHERE sp.code = s.plan;

CREATE TABLE subscription_beneficiaries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL REFERENCES b2c_subscriptions(id) ON DELETE CASCADE,
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    removed_at TIMESTAMPTZ, -- NULL = covered now
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_subscription_patient UNIQUE (subscription_id, patient_id),
    CONSTRAINT ck_beneficiary_dates CHECK (removed_at IS NULL OR removed_at >= added_at)
);
-- A patient is covered by at most one subscription at a time
CREATE UNIQUE INDEX uq_active_beneficiary ON subscription_beneficiaries (patient_id) WHERE removed_at IS NULL;

-- Only active subscriptions keep covering their patient
INSERT INTO subscription_beneficiaries (subscription_id, patient_id, added_at, removed_at, created_at, updated_at)
SELECT id, beneficiary_patient_id, start_at,
       CASE WHEN status = 'active' THEN NULL ELSE GREATEST(start_at, COALESCE(end_at, updated_at)) END,
       created_at, updated_at
  FROM b2c_subscriptions
 WHERE beneficiary_patient_id IS NOT NULL;

ALTER TABLE b2c_subscriptions
    ALTER COLUMN payer_user_id SET NOT NULL,
    ALTER COLUMN plan_id SET NOT NULL,
    DROP COLUMN user_id,
    DROP COLUMN beneficiary_patient_id, -- also drops uq_b2c_subscriptions_active_beneficiary
    DROP COLUMN plan,
    DROP CONSTRAINT b2c_subscriptions_monthly_price_check,
    ADD CONSTRAINT b2c_subscriptions_monthly_price_check CHECK (monthly_price BETWEEN 0.00 AND 10000.00);
ALTER TABLE b2c_subscriptions RENAME CONSTRAINT ck_subscription_dates TO ck_sub_dates;


-- ---------------------------------------------------------------------------
-- 4. M3 · TELEMETRY AND DEVICES
-- ---------------------------------------------------------------------------
-- 4.1 devices
ALTER TABLE devices ADD COLUMN device_type_id SMALLINT REFERENCES device_types(id) ON DELETE RESTRICT;
UPDATE devices d SET device_type_id = dt.id FROM device_types dt WHERE dt.code = d.device_type;
ALTER TABLE devices
    ALTER COLUMN device_type_id SET NOT NULL,
    DROP COLUMN device_type,
    ADD CONSTRAINT uq_devices_id_patient UNIQUE (id, patient_id), -- target of the composite FKs below
    ADD CONSTRAINT ck_device_dates CHECK (
            (sensor_expires_at IS NULL OR first_reading_at IS NULL OR sensor_expires_at >= first_reading_at)
        AND (last_synced_at IS NULL OR first_reading_at IS NULL OR last_synced_at >= first_reading_at));

-- 4.2 heart_rate_samples: append-only, the patient comes from the device
CREATE TABLE heart_rate_samples (
    id BIGSERIAL PRIMARY KEY,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    measured_at TIMESTAMPTZ NOT NULL
        CHECK (measured_at >= TIMESTAMPTZ '2020-01-01 00:00:00+00' AND measured_at <= NOW() + INTERVAL '5 minutes'),
    bpm INTEGER NOT NULL CHECK (bpm BETWEEN 25 AND 250),
    idempotency_key VARCHAR(100) UNIQUE CHECK (btrim(idempotency_key) <> ''), -- generated on the phone
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_device_heart_rate UNIQUE (device_id, measured_at)
);

-- 4.3 glucose_readings: the device must belong to the same patient, and only
-- CGM readings have a device.
UPDATE glucose_readings g SET device_id = NULL
  FROM devices d WHERE d.id = g.device_id AND d.patient_id <> g.patient_id;
UPDATE glucose_readings SET device_id = NULL WHERE device_id IS NOT NULL AND source <> 'cgm_sensor';
ALTER TABLE glucose_readings
    ALTER COLUMN signal_quality DROP DEFAULT,
    DROP CONSTRAINT glucose_readings_device_id_fkey,
    ADD CONSTRAINT fk_glucose_device_patient FOREIGN KEY (device_id, patient_id)
        REFERENCES devices (id, patient_id) ON DELETE SET NULL (device_id),
    ADD CONSTRAINT ck_glucose_source_device CHECK (device_id IS NULL OR source = 'cgm_sensor'),
    ADD CONSTRAINT glucose_readings_measured_at_check
        CHECK (measured_at >= TIMESTAMPTZ '2020-01-01 00:00:00+00' AND measured_at <= NOW() + INTERVAL '5 minutes');

-- 4.4 physical_activity_logs
UPDATE physical_activity_logs a SET device_id = NULL
  FROM devices d WHERE d.id = a.device_id AND d.patient_id <> a.patient_id;
ALTER TABLE physical_activity_logs
    ADD COLUMN idempotency_key VARCHAR(100) UNIQUE,
    DROP CONSTRAINT physical_activity_logs_device_id_fkey,
    ADD CONSTRAINT fk_activity_device_patient FOREIGN KEY (device_id, patient_id)
        REFERENCES devices (id, patient_id) ON DELETE SET NULL (device_id),
    ADD CONSTRAINT physical_activity_logs_started_at_check
        CHECK (started_at >= TIMESTAMPTZ '2020-01-01 00:00:00+00' AND started_at <= NOW() + INTERVAL '5 minutes'),
    DROP CONSTRAINT physical_activity_logs_duration_minutes_check,
    ADD CONSTRAINT physical_activity_logs_duration_minutes_check CHECK (duration_minutes BETWEEN 1 AND 1440),
    DROP CONSTRAINT physical_activity_logs_total_steps_check,
    ADD CONSTRAINT physical_activity_logs_total_steps_check CHECK (total_steps BETWEEN 0 AND 200000),
    DROP CONSTRAINT physical_activity_logs_avg_heart_rate_bpm_check,
    ADD CONSTRAINT physical_activity_logs_avg_heart_rate_bpm_check CHECK (avg_heart_rate_bpm BETWEEN 30 AND 250),
    DROP CONSTRAINT physical_activity_logs_calories_burned_kcal_check,
    ADD CONSTRAINT physical_activity_logs_calories_burned_kcal_check CHECK (calories_burned_kcal BETWEEN 0 AND 15000);


-- ---------------------------------------------------------------------------
-- 5. M4 · NUTRITION AND INSULIN
-- ---------------------------------------------------------------------------
-- 5.1 food_categories (the free-text foods.category becomes a row)
CREATE TABLE food_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(80) NOT NULL UNIQUE CHECK (btrim(name) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO food_categories (name)
SELECT DISTINCT btrim(category) FROM foods WHERE btrim(category) <> '';

-- 5.2 foods: categories, composite dishes. The SRS model does not keep the author.
ALTER TABLE foods ADD COLUMN category_id UUID REFERENCES food_categories(id) ON DELETE SET NULL;
UPDATE foods f SET category_id = fc.id FROM food_categories fc WHERE fc.name = btrim(f.category);
ALTER TABLE foods
    DROP COLUMN category,
    DROP COLUMN created_by_user_id,
    ADD CONSTRAINT foods_fatsecret_food_id_check CHECK (fatsecret_food_id > 0),
    DROP CONSTRAINT foods_food_type_check,
    ADD CONSTRAINT foods_food_type_check CHECK (food_type IN ('generic', 'brand', 'composite_dish'));

-- 5.3 food_components: the ingredients of a composite dish (self M:N on foods)
CREATE TABLE food_components (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dish_food_id UUID NOT NULL REFERENCES foods(id) ON DELETE CASCADE,
    component_food_id UUID NOT NULL REFERENCES foods(id) ON DELETE RESTRICT,
    default_quantity_g DECIMAL(7,2) NOT NULL CHECK (default_quantity_g BETWEEN 0.01 AND 10000.00),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_dish_component UNIQUE (dish_food_id, component_food_id),
    CONSTRAINT ck_dish_not_self CHECK (dish_food_id <> component_food_id)
);

-- 5.4 food_servings: realistic ranges, and fibre, sugars and saturated fat
-- never exceed the total they belong to.
ALTER INDEX uq_food_servings_description RENAME TO uq_food_serving_desc;
ALTER TABLE food_servings
    ADD CONSTRAINT food_servings_fatsecret_serving_id_check CHECK (fatsecret_serving_id > 0),
    ADD CONSTRAINT food_servings_metric_amount_check CHECK (metric_amount BETWEEN 0.01 AND 50000.00),
    DROP CONSTRAINT food_servings_carbs_g_check,
    ADD CONSTRAINT food_servings_carbs_g_check CHECK (carbs_g BETWEEN 0.00 AND 1000.00),
    ADD CONSTRAINT food_servings_fiber_g_check CHECK (fiber_g BETWEEN 0.00 AND 1000.00),
    ADD CONSTRAINT food_servings_sugars_g_check CHECK (sugars_g BETWEEN 0.00 AND 1000.00),
    DROP CONSTRAINT food_servings_calories_kcal_check,
    ADD CONSTRAINT food_servings_calories_kcal_check CHECK (calories_kcal BETWEEN 0.00 AND 9000.00),
    ADD CONSTRAINT food_servings_protein_g_check CHECK (protein_g BETWEEN 0.00 AND 1000.00),
    ADD CONSTRAINT food_servings_fat_g_check CHECK (fat_g BETWEEN 0.00 AND 1000.00),
    ADD CONSTRAINT food_servings_saturated_fat_g_check CHECK (saturated_fat_g BETWEEN 0.00 AND 1000.00),
    ADD CONSTRAINT food_servings_sodium_mg_check CHECK (sodium_mg BETWEEN 0.00 AND 50000.00),
    ADD CONSTRAINT ck_serving_macros_hierarchy CHECK (
        fiber_g <= carbs_g AND sugars_g <= carbs_g AND saturated_fat_g <= fat_g);

-- 5.5 meals
ALTER TABLE meals
    ADD COLUMN meal_type_id SMALLINT REFERENCES meal_types(id) ON DELETE RESTRICT,
    ADD COLUMN idempotency_key VARCHAR(100) UNIQUE;
UPDATE meals m SET meal_type_id = mt.id FROM meal_types mt WHERE mt.code = m.meal_type;
UPDATE meals SET eaten_at = NULL WHERE status <> 'eaten';
ALTER TABLE meals
    ALTER COLUMN meal_type_id SET NOT NULL,
    DROP COLUMN meal_type,
    ADD CONSTRAINT uq_meals_id_patient UNIQUE (id, patient_id), -- target of fk_insulin_meal_patient
    DROP CONSTRAINT ck_meal_eaten_at,
    ADD CONSTRAINT ck_meal_eaten_time CHECK ((status = 'eaten') = (eaten_at IS NOT NULL)),
    ADD CONSTRAINT meals_total_carbs_g_check CHECK (total_carbs_g BETWEEN 0.00 AND 2000.00),
    ADD CONSTRAINT meals_total_calories_kcal_check CHECK (total_calories_kcal BETWEEN 0.00 AND 15000.00),
    ADD CONSTRAINT meals_postprandial_delta_mg_dl_check CHECK (postprandial_delta_mg_dl BETWEEN -530 AND 530);

-- 5.6 meal_items: values frozen when the meal was logged
ALTER TABLE meal_items
    DROP CONSTRAINT meal_items_serving_quantity_check,
    ADD CONSTRAINT meal_items_serving_quantity_check CHECK (serving_quantity BETWEEN 0.01 AND 100.00),
    ADD CONSTRAINT meal_items_carbs_g_check CHECK (carbs_g BETWEEN 0.00 AND 2000.00),
    ADD CONSTRAINT meal_items_calories_kcal_check CHECK (calories_kcal BETWEEN 0.00 AND 15000.00);

-- 5.7 meal_photos: photos analysed by AI; the estimate is only a proposal
CREATE TABLE meal_photos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meal_id UUID NOT NULL REFERENCES meals(id) ON DELETE CASCADE,
    image_url VARCHAR(500) NOT NULL CHECK (btrim(image_url) <> ''),
    analysis_status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (analysis_status IN ('pending', 'completed', 'failed')),
    estimated_weight_g DECIMAL(7,2) CHECK (estimated_weight_g BETWEEN 0.01 AND 10000.00),
    estimated_carbs_g DECIMAL(6,2) CHECK (estimated_carbs_g BETWEEN 0.00 AND 2000.00),
    estimated_calories_kcal DECIMAL(7,2) CHECK (estimated_calories_kcal BETWEEN 0.00 AND 15000.00),
    detected_items JSONB, -- recognised foods and estimated quantity of each
    confidence_percent DECIMAL(5,2) CHECK (confidence_percent BETWEEN 0.00 AND 100.00),
    model_version VARCHAR(50) CHECK (btrim(model_version) <> ''),
    analyzed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT ck_meal_photo_analyzed CHECK (
           (analysis_status = 'completed' AND analyzed_at IS NOT NULL)
        OR (analysis_status = 'pending' AND analyzed_at IS NULL)
        OR analysis_status = 'failed')
);

-- 5.8 insulin_logs: the meal must belong to the same patient
ALTER TABLE insulin_logs
    ADD COLUMN insulin_type_id SMALLINT REFERENCES insulin_types(id) ON DELETE RESTRICT,
    ADD COLUMN idempotency_key VARCHAR(100) UNIQUE;
UPDATE insulin_logs il SET insulin_type_id = it.id FROM insulin_types it WHERE it.code = il.insulin_type;
UPDATE insulin_logs il SET meal_id = NULL
  FROM meals m WHERE m.id = il.meal_id AND m.patient_id <> il.patient_id;
ALTER TABLE insulin_logs
    ALTER COLUMN insulin_type_id SET NOT NULL,
    DROP COLUMN insulin_type,
    DROP CONSTRAINT insulin_logs_meal_id_fkey,
    ADD CONSTRAINT fk_insulin_meal_patient FOREIGN KEY (meal_id, patient_id)
        REFERENCES meals (id, patient_id) ON DELETE SET NULL (meal_id),
    ADD CONSTRAINT insulin_logs_administered_at_check
        CHECK (administered_at >= TIMESTAMPTZ '2020-01-01 00:00:00+00' AND administered_at <= NOW() + INTERVAL '5 minutes'),
    DROP CONSTRAINT insulin_logs_dose_units_check,
    ADD CONSTRAINT insulin_logs_dose_units_check CHECK (dose_units BETWEEN 0.1 AND 100.0);


-- ---------------------------------------------------------------------------
-- 6. M5 · PREDICTION, ALERTS AND IRIS
-- ---------------------------------------------------------------------------
-- 6.1 glucose_predictions
ALTER TABLE glucose_predictions
    DROP CONSTRAINT glucose_predictions_horizon_minutes_check,
    ADD CONSTRAINT glucose_predictions_horizon_minutes_check CHECK (horizon_minutes IN (15, 30, 45, 60)),
    ADD CONSTRAINT glucose_predictions_predicted_mg_dl_check CHECK (predicted_mg_dl BETWEEN 20 AND 550),
    ADD CONSTRAINT glucose_predictions_ci_low_mg_dl_check CHECK (ci_low_mg_dl BETWEEN 20 AND 550),
    ADD CONSTRAINT glucose_predictions_ci_high_mg_dl_check CHECK (ci_high_mg_dl BETWEEN 20 AND 550);

-- 6.2 alerts: exactly one source, which matches the type (critical <-> reading,
-- predicted <-> prediction, sensor <-> device). The patient comes from the source.
ALTER TABLE alerts ADD COLUMN device_id UUID REFERENCES devices(id) ON DELETE CASCADE;
UPDATE alerts SET source_prediction_id = NULL WHERE alert_type IN ('critical_low', 'critical_high');
UPDATE alerts SET source_reading_id = NULL WHERE alert_type IN ('predicted_low', 'predicted_high');
UPDATE alerts SET source_reading_id = NULL, source_prediction_id = NULL
 WHERE alert_type IN ('sensor_disconnected', 'sensor_expiring');
-- Sensor alerts had no device column: link them to the patient's latest CGM sensor
UPDATE alerts a SET device_id = (
           SELECT d.id FROM devices d JOIN device_types dt ON dt.id = d.device_type_id
            WHERE d.patient_id = a.patient_id AND dt.code = 'cgm_sensor'
            ORDER BY d.created_at DESC LIMIT 1)
 WHERE a.alert_type IN ('sensor_disconnected', 'sensor_expiring');
-- An alert whose reading or prediction is gone has no patient in the new model
-- (000001 kept it with a NULL source; now it would have been deleted with it).
DELETE FROM alerts WHERE num_nonnulls(source_reading_id, source_prediction_id, device_id) <> 1;

UPDATE alerts SET acknowledged_at = NULL WHERE ack_status = 'pending';
UPDATE alerts SET acknowledged_at = triggered_at WHERE acknowledged_at < triggered_at;

-- 6.3 alert_recipients: one row per person and channel, with the delivery cycle
CREATE TABLE alert_recipients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel VARCHAR(10) NOT NULL CHECK (channel IN ('push', 'sms', 'email', 'in_app')),
    delivery_status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (delivery_status IN ('pending', 'sent', 'delivered', 'read', 'acknowledged', 'failed')),
    sent_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    read_at TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_alert_recipient_channel UNIQUE (alert_id, user_id, channel),
    -- sent => sent_at, delivered => delivered_at, read => read_at,
    -- acknowledged => acknowledged_at; pending has no dates; nothing before sent_at
    CONSTRAINT ck_recipient_state_timestamps CHECK (
            (delivery_status <> 'pending'
                OR (sent_at IS NULL AND delivered_at IS NULL AND read_at IS NULL AND acknowledged_at IS NULL))
        AND (delivery_status NOT IN ('sent', 'delivered', 'read', 'acknowledged') OR sent_at IS NOT NULL)
        AND (delivery_status <> 'delivered' OR delivered_at IS NOT NULL)
        AND (delivery_status <> 'read' OR read_at IS NOT NULL)
        AND (delivery_status <> 'acknowledged' OR acknowledged_at IS NOT NULL)
        AND (delivered_at IS NULL OR (sent_at IS NOT NULL AND delivered_at >= sent_at))
        AND (read_at IS NULL OR (sent_at IS NOT NULL AND read_at >= sent_at))
        AND (acknowledged_at IS NULL OR (sent_at IS NOT NULL AND acknowledged_at >= sent_at)))
);

-- Who handled an alert is now recorded on that person's recipient row
INSERT INTO alert_recipients (alert_id, user_id, channel, delivery_status, sent_at, acknowledged_at, created_at, updated_at)
SELECT a.id, COALESCE(a.acknowledged_by_user_id, p.user_id), 'in_app', 'acknowledged',
       a.triggered_at, a.acknowledged_at, a.created_at, a.updated_at
  FROM alerts a JOIN patients p ON p.id = a.patient_id
 WHERE a.ack_status <> 'pending';

ALTER TABLE alerts
    DROP COLUMN patient_id,
    DROP COLUMN acknowledged_by_user_id,
    DROP CONSTRAINT ck_alert_acknowledged_at,
    ADD CONSTRAINT ck_alert_ack_time CHECK (
           (ack_status = 'pending' AND acknowledged_at IS NULL)
        OR (ack_status <> 'pending' AND acknowledged_at IS NOT NULL AND acknowledged_at >= triggered_at)),
    ADD CONSTRAINT ck_alert_single_source CHECK (num_nonnulls(source_reading_id, source_prediction_id, device_id) = 1),
    ADD CONSTRAINT ck_alert_type_source CHECK (
           (alert_type IN ('critical_low', 'critical_high') AND source_reading_id IS NOT NULL)
        OR (alert_type IN ('predicted_low', 'predicted_high') AND source_prediction_id IS NOT NULL)
        OR (alert_type IN ('sensor_disconnected', 'sensor_expiring') AND device_id IS NOT NULL)),
    -- an alert goes with its source (SET NULL would leave it without a patient)
    DROP CONSTRAINT alerts_source_reading_id_fkey,
    ADD CONSTRAINT alerts_source_reading_id_fkey FOREIGN KEY (source_reading_id)
        REFERENCES glucose_readings(id) ON DELETE CASCADE,
    DROP CONSTRAINT alerts_source_prediction_id_fkey,
    ADD CONSTRAINT alerts_source_prediction_id_fkey FOREIGN KEY (source_prediction_id)
        REFERENCES glucose_predictions(id) ON DELETE CASCADE;

-- 6.4 iris_conversations: the questions to Iris are grouped in conversations
CREATE TABLE iris_conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    title VARCHAR(150) CHECK (btrim(title) <> ''), -- from the first question, or chosen by the patient
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE iris_chat_interactions
    ADD COLUMN conversation_id UUID REFERENCES iris_conversations(id) ON DELETE CASCADE,
    ADD COLUMN llm_model_version VARCHAR(50),
    ADD COLUMN safety_guardrail_triggered BOOLEAN NOT NULL DEFAULT FALSE;
-- Existing interactions: one conversation per patient
INSERT INTO iris_conversations (patient_id, created_at, updated_at)
SELECT patient_id, MIN(created_at), MAX(updated_at) FROM iris_chat_interactions GROUP BY patient_id;
UPDATE iris_chat_interactions i SET conversation_id = c.id FROM iris_conversations c WHERE c.patient_id = i.patient_id;
ALTER TABLE iris_chat_interactions
    ALTER COLUMN conversation_id SET NOT NULL,
    DROP COLUMN patient_id;


-- ---------------------------------------------------------------------------
-- 7. M6 · CLINICAL FOLLOW-UP AND TELEMEDICINE
-- ---------------------------------------------------------------------------
-- 7.1 health_professionals
ALTER TABLE health_professionals ADD COLUMN specialty_id SMALLINT REFERENCES medical_specialties(id) ON DELETE RESTRICT;
UPDATE health_professionals hp SET specialty_id = ms.id FROM medical_specialties ms WHERE ms.code = hp.specialty;
ALTER TABLE health_professionals
    ALTER COLUMN specialty_id SET NOT NULL,
    DROP COLUMN specialty;
ALTER INDEX uq_health_professionals_user_id RENAME TO uq_hp_user_id;
ALTER INDEX uq_health_professionals_license RENAME TO uq_hp_license;

-- 7.2 professional_availability: weekly blocks; free slots are computed from them
CREATE TABLE professional_availability (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    professional_id UUID NOT NULL REFERENCES health_professionals(id) ON DELETE CASCADE,
    weekday SMALLINT NOT NULL CHECK (weekday BETWEEN 1 AND 7), -- ISO 8601: 1 = Monday
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,
    slot_minutes INTEGER NOT NULL DEFAULT 30 CHECK (slot_minutes IN (15, 20, 30, 45, 60)),
    valid_from DATE NOT NULL,
    valid_until DATE, -- NULL = no end
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_availability_hours CHECK (end_time > start_time),
    CONSTRAINT ck_availability_validity CHECK (valid_until IS NULL OR valid_until >= valid_from)
);

-- 7.3 patient_professional_assignments: a clinician with history is never deleted
UPDATE patient_professional_assignments SET end_date = GREATEST(start_date, updated_at::date)
 WHERE status <> 'active' AND end_date IS NULL;
ALTER TABLE patient_professional_assignments
    DROP CONSTRAINT patient_professional_assignments_professional_id_fkey,
    ADD CONSTRAINT patient_professional_assignments_professional_id_fkey FOREIGN KEY (professional_id)
        REFERENCES health_professionals(id) ON DELETE RESTRICT,
    DROP CONSTRAINT ck_assignment_dates,
    ADD CONSTRAINT ck_assignment_dates CHECK (
        (end_date IS NULL OR end_date >= start_date) AND (status = 'active' OR end_date IS NOT NULL)),
    ADD CONSTRAINT uq_assignment_id_patient UNIQUE (id, patient_id); -- target of fk_report_assignment_patient

-- 7.4 chat_messages: the sender is stored as their role in the conversation.
-- Converted first, while the conversation still names its parties.
ALTER TABLE chat_messages ADD COLUMN sender_role VARCHAR(20) CHECK (sender_role IN ('patient', 'professional', 'caregiver'));
UPDATE chat_messages m SET sender_role = CASE
           WHEN m.sender_user_id = p.user_id THEN 'patient'
           WHEN m.sender_user_id = c.caregiver_user_id THEN 'caregiver'
           WHEN m.sender_user_id = hp.user_id THEN 'professional'
       END
  FROM chat_conversations c
  JOIN patients p ON p.id = c.patient_id
  LEFT JOIN health_professionals hp ON hp.id = c.professional_id
 WHERE c.id = m.conversation_id;
UPDATE chat_messages SET read_at = sent_at WHERE read_at < sent_at;
ALTER TABLE chat_messages
    ALTER COLUMN sender_role SET NOT NULL,
    DROP COLUMN sender_user_id,
    ADD CONSTRAINT ck_chat_message_dates CHECK (read_at IS NULL OR read_at >= sent_at);

-- 7.5 teleconsultations hang from the follow-up (assignment)
ALTER TABLE teleconsultations ADD COLUMN assignment_id UUID REFERENCES patient_professional_assignments(id) ON DELETE CASCADE;
UPDATE teleconsultations t SET assignment_id = a.id
  FROM patient_professional_assignments a
 WHERE a.patient_id = t.patient_id AND a.professional_id = t.professional_id;
ALTER TABLE teleconsultations
    ALTER COLUMN assignment_id SET NOT NULL,
    DROP COLUMN patient_id, -- also drops fk_teleconsultation_assignment
    DROP COLUMN professional_id,
    DROP CONSTRAINT teleconsultations_duration_minutes_check,
    ADD CONSTRAINT teleconsultations_duration_minutes_check CHECK (duration_minutes BETWEEN 10 AND 180),
    -- two active consultations of the same follow-up never overlap; the trigger
    -- verify_teleconsultation_no_overlap() also covers other follow-ups
    ADD CONSTRAINT ex_teleconsultation_no_overlap EXCLUDE USING gist (
        assignment_id WITH =,
        consultation_period(scheduled_at, duration_minutes) WITH &&
    ) WHERE (status IN ('scheduled', 'in_progress'));

-- 7.6 chat_conversations hang from the follow-up and/or the caregiver link
ALTER TABLE chat_conversations
    ADD COLUMN assignment_id UUID REFERENCES patient_professional_assignments(id) ON DELETE CASCADE,
    ADD COLUMN caregiver_link_id UUID REFERENCES caregiver_links(id) ON DELETE CASCADE;
UPDATE chat_conversations c SET assignment_id = a.id
  FROM patient_professional_assignments a
 WHERE a.patient_id = c.patient_id AND a.professional_id = c.professional_id;
UPDATE chat_conversations c SET caregiver_link_id = cl.id
  FROM caregiver_links cl
 WHERE cl.patient_id = c.patient_id AND cl.caregiver_user_id = c.caregiver_user_id;
ALTER TABLE chat_conversations
    DROP COLUMN patient_id, -- also drops ck_conversation_parties, uq_conversation_parties and the composite FKs
    DROP COLUMN professional_id,
    DROP COLUMN caregiver_user_id,
    ADD CONSTRAINT ck_chat_channel_parties CHECK (
           (channel_type = 'patient_doctor' AND assignment_id IS NOT NULL AND caregiver_link_id IS NULL)
        OR (channel_type = 'patient_caregiver' AND assignment_id IS NULL AND caregiver_link_id IS NOT NULL)
        OR (channel_type = 'caregiver_doctor' AND assignment_id IS NOT NULL AND caregiver_link_id IS NOT NULL)),
    ADD CONSTRAINT uq_chat_channel UNIQUE NULLS NOT DISTINCT (channel_type, assignment_id, caregiver_link_id);

-- 7.7 clinical_reports: who generated it, and the follow-up when a clinician did
ALTER TABLE clinical_reports
    ADD COLUMN assignment_id UUID,
    ADD COLUMN generated_by_role VARCHAR(20) NOT NULL DEFAULT 'patient'
        CHECK (generated_by_role IN ('patient', 'professional', 'system_scheduled'));
ALTER TABLE clinical_reports
    ALTER COLUMN generated_by_role DROP DEFAULT, -- the default only filled the existing rows
    ADD CONSTRAINT fk_report_assignment_patient FOREIGN KEY (assignment_id, patient_id)
        REFERENCES patient_professional_assignments (id, patient_id) ON DELETE SET NULL (assignment_id),
    ADD CONSTRAINT ck_report_generator CHECK (assignment_id IS NULL OR generated_by_role = 'professional'),
    DROP CONSTRAINT ck_report_percentages,
    ADD CONSTRAINT clinical_reports_time_in_range_percent_check CHECK (time_in_range_percent BETWEEN 0.00 AND 100.00),
    ADD CONSTRAINT clinical_reports_time_below_range_percent_check CHECK (time_below_range_percent BETWEEN 0.00 AND 100.00),
    ADD CONSTRAINT clinical_reports_time_above_range_percent_check CHECK (time_above_range_percent BETWEEN 0.00 AND 100.00),
    ADD CONSTRAINT ck_report_tir_sum CHECK (
        time_in_range_percent + time_below_range_percent + time_above_range_percent BETWEEN 99.80 AND 100.20),
    DROP CONSTRAINT ck_report_mean,
    ADD CONSTRAINT clinical_reports_mean_glucose_mg_dl_check CHECK (mean_glucose_mg_dl BETWEEN 20 AND 550),
    ADD CONSTRAINT clinical_reports_std_dev_mg_dl_check CHECK (std_dev_mg_dl BETWEEN 0.00 AND 300.00),
    ADD CONSTRAINT clinical_reports_coefficient_of_variation_percent_check
        CHECK (coefficient_of_variation_percent BETWEEN 0.00 AND 200.00),
    ADD CONSTRAINT clinical_reports_gmi_percent_check CHECK (gmi_percent BETWEEN 3.00 AND 20.00),
    ADD CONSTRAINT clinical_reports_hypo_episodes_check CHECK (hypo_episodes BETWEEN 0 AND 10000),
    ADD CONSTRAINT clinical_reports_hyper_episodes_check CHECK (hyper_episodes BETWEEN 0 AND 10000);


-- ---------------------------------------------------------------------------
-- 8. M7 · GDPR, CONSENTS AND AUDIT
-- ---------------------------------------------------------------------------
-- 8.1 legal_documents: every version is a new, never-edited row
CREATE TABLE legal_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_type VARCHAR(40) NOT NULL CHECK (document_type IN (
        'terms_of_service', 'health_data_processing', 'external_ai_processing', 'beta_testing')),
    version VARCHAR(20) NOT NULL CHECK (btrim(version) <> ''),
    title VARCHAR(150) NOT NULL CHECK (btrim(title) <> ''),
    body TEXT NOT NULL CHECK (btrim(body) <> ''),
    is_mandatory BOOLEAN NOT NULL DEFAULT TRUE, -- FALSE for the optional consents (external AI, beta tests)
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_legal_doc_version UNIQUE (document_type, version)
);
-- Version 1.0 of each document, so every environment can record consents.
-- The text is provisional: the approved text is published as a new version.
INSERT INTO legal_documents (document_type, version, title, body, is_mandatory) VALUES
    ('terms_of_service', '1.0', 'Termos de Utilização',
     'Texto provisório da versão 1.0 dos Termos de Utilização do Voltik. O texto aprovado é publicado como nova versão.', TRUE),
    ('health_data_processing', '1.0', 'Tratamento de Dados de Saúde',
     'Texto provisório da versão 1.0 da política de tratamento de dados de saúde do Voltik. O texto aprovado é publicado como nova versão.', TRUE),
    ('external_ai_processing', '1.0', 'Processamento por Inteligência Artificial Externa',
     'Texto provisório da versão 1.0 do consentimento para processamento por IA externa (Iris e análise de fotografias). O texto aprovado é publicado como nova versão.', FALSE),
    ('beta_testing', '1.0', 'Participação em Testes Beta',
     'Texto provisório da versão 1.0 do consentimento para participar em testes beta. O texto aprovado é publicado como nova versão.', FALSE);
-- Other versions already accepted in 000001 become documents too
INSERT INTO legal_documents (document_type, version, title, body, is_mandatory)
SELECT DISTINCT c.consent_type, c.document_version, d.title, d.body, d.is_mandatory
  FROM consents c JOIN legal_documents d ON d.document_type = c.consent_type AND d.version = '1.0'
 WHERE c.document_version <> '1.0';

-- 8.2 consents: each row is the acceptance of one document version, with the IP
ALTER TABLE consents
    ADD COLUMN document_id UUID REFERENCES legal_documents(id) ON DELETE RESTRICT,
    ADD COLUMN ip_address INET; -- proof of where the consent was given
UPDATE consents c SET document_id = d.id
  FROM legal_documents d WHERE d.document_type = c.consent_type AND d.version = c.document_version;
UPDATE consents SET revoked_at = created_at WHERE revoked_at < created_at;
ALTER TABLE consents
    ALTER COLUMN document_id SET NOT NULL,
    DROP COLUMN consent_type, -- also drops uq_consent_version
    DROP COLUMN document_version,
    ADD CONSTRAINT uq_user_document_consent UNIQUE (user_id, document_id),
    ADD CONSTRAINT ck_consent_revoked_time CHECK (revoked_at IS NULL OR revoked_at >= created_at);

-- 8.3 data_access_audit_log: the list of operations is no longer closed
-- (UC021 adds data_export, admin_patient_view, ...). Immutability: section 10.
ALTER TABLE data_access_audit_log DROP CONSTRAINT data_access_audit_log_operation_check;


-- ---------------------------------------------------------------------------
-- 9. TEXT COLUMNS NEVER ACCEPT BLANK TEXT (SRS 6.1)
-- Optional columns turn blank values into NULL first. The new tables above
-- declare the same check inline.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    tc TEXT[];
BEGIN
    FOREACH tc SLICE 1 IN ARRAY ARRAY[
        ['users', 'email'], ['users', 'full_name'], ['users', 'phone'], ['users', 'profile_photo_url'],
        ['external_identities', 'provider_subject'], ['external_identities', 'provider_email'],
        ['notification_tokens', 'push_token'],
        ['organizations', 'company_name'], ['organizations', 'tax_id'], ['organizations', 'industry'],
        ['company_invite_codes', 'code'],
        ['patients', 'occupation'], ['patients', 'clinical_notes'],
        ['emergency_contacts', 'full_name'], ['emergency_contacts', 'relationship'], ['emergency_contacts', 'phone'],
        ['caregiver_links', 'revocation_reason'],
        ['devices', 'brand_model'], ['devices', 'serial_or_mac'],
        ['glucose_readings', 'idempotency_key'],
        ['physical_activity_logs', 'activity_name'], ['physical_activity_logs', 'idempotency_key'],
        ['foods', 'barcode_ean'], ['foods', 'food_name'], ['foods', 'brand'],
        ['food_servings', 'serving_description'], ['food_servings', 'metric_unit'],
        ['meals', 'notes'], ['meals', 'idempotency_key'],
        ['insulin_logs', 'insulin_brand'], ['insulin_logs', 'notes'], ['insulin_logs', 'idempotency_key'],
        ['glucose_predictions', 'model_version'],
        ['alerts', 'title'], ['alerts', 'message'], ['alerts', 'iris_suggested_action'], ['alerts', 'resolution_notes'],
        ['iris_chat_interactions', 'user_question'], ['iris_chat_interactions', 'iris_answer'],
        ['iris_chat_interactions', 'llm_model_version'],
        ['health_professionals', 'license_number'], ['health_professionals', 'institution'], ['health_professionals', 'bio'],
        ['teleconsultations', 'webrtc_room_token'], ['teleconsultations', 'clinical_notes'],
        ['teleconsultations', 'adjusted_treatment_plan'],
        ['chat_conversations', 'topic'],
        ['clinical_reports', 'work_impact_analysis'], ['clinical_reports', 'iris_summary'], ['clinical_reports', 'pdf_file_url'],
        ['chat_messages', 'body'], ['chat_messages', 'attachment_url'],
        ['data_access_audit_log', 'operation'], ['data_access_audit_log', 'user_agent']
    ] LOOP
        IF EXISTS (SELECT 1 FROM information_schema.columns
                    WHERE table_schema = 'public' AND table_name = tc[1] AND column_name = tc[2] AND is_nullable = 'YES') THEN
            EXECUTE format('UPDATE %I SET %I = NULL WHERE btrim(%I) = %L', tc[1], tc[2], tc[2], '');
        END IF;
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (btrim(%I) <> %L)',
                       tc[1], tc[1] || '_' || tc[2] || '_check', tc[2], '');
    END LOOP;
END
$$;


-- ---------------------------------------------------------------------------
-- 10. RULES THAT SPAN TABLES (SRS 6.6): 8 functions, 10 triggers
-- Violations raise check_violation with a constraint name the API can map.
-- ---------------------------------------------------------------------------
-- 10.1 A company issues or activates codes only while active and within the
-- seats it bought. The company row is locked against concurrent requests.
-- Updates that change neither the status nor the company are not checked, so
-- the GDPR deletion of a patient (used_by_patient_id -> NULL) never fails.
CREATE FUNCTION enforce_b2b_seat_limit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_seat_count INTEGER;
    v_org_status VARCHAR(20);
    v_taken INTEGER;
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.status IS NOT DISTINCT FROM OLD.status
       AND NEW.organization_id IS NOT DISTINCT FROM OLD.organization_id THEN
        RETURN NEW;
    END IF;

    IF NEW.status IN ('available', 'used') THEN
        SELECT seat_count, status INTO v_seat_count, v_org_status
          FROM organizations WHERE id = NEW.organization_id
           FOR UPDATE;

        IF v_org_status <> 'active' THEN
            RAISE EXCEPTION 'organization % is % and cannot issue or activate codes', NEW.organization_id, v_org_status
                USING ERRCODE = 'check_violation', CONSTRAINT = 'organization_inactive';
        END IF;

        SELECT count(*) INTO v_taken
          FROM company_invite_codes
         WHERE organization_id = NEW.organization_id
           AND status IN ('available', 'used')
           AND id IS DISTINCT FROM NEW.id;

        IF v_taken >= v_seat_count THEN
            RAISE EXCEPTION 'organization % already uses its % seats', NEW.organization_id, v_seat_count
                USING ERRCODE = 'check_violation', CONSTRAINT = 'seat_limit_reached';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_company_invite_codes_seat_limit BEFORE INSERT OR UPDATE ON company_invite_codes
    FOR EACH ROW EXECUTE FUNCTION enforce_b2b_seat_limit();

-- 10.2 A subscription never covers more patients than its plan allows.
-- The subscription row is locked against concurrent requests.
CREATE FUNCTION enforce_b2c_beneficiary_limit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_max SMALLINT;
    v_covered INTEGER;
BEGIN
    IF NEW.removed_at IS NULL THEN
        SELECT sp.max_beneficiaries INTO v_max
          FROM b2c_subscriptions s JOIN subscription_plans sp ON sp.id = s.plan_id
         WHERE s.id = NEW.subscription_id
           FOR UPDATE OF s;

        SELECT count(*) INTO v_covered
          FROM subscription_beneficiaries
         WHERE subscription_id = NEW.subscription_id
           AND removed_at IS NULL
           AND id IS DISTINCT FROM NEW.id;

        IF v_covered >= v_max THEN
            RAISE EXCEPTION 'subscription % already covers % patient(s), the maximum of its plan', NEW.subscription_id, v_max
                USING ERRCODE = 'check_violation', CONSTRAINT = 'beneficiary_limit';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_subscription_beneficiaries_limit BEFORE INSERT OR UPDATE ON subscription_beneficiaries
    FOR EACH ROW EXECUTE FUNCTION enforce_b2c_beneficiary_limit();

-- 10.3 Nobody is their own caregiver or clinician, or accepts their own invite
CREATE FUNCTION prevent_self_clinical_linking() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_patient_user_id UUID;
    v_other_user_id UUID;
BEGIN
    SELECT user_id INTO v_patient_user_id FROM patients WHERE id = NEW.patient_id;

    IF TG_TABLE_NAME = 'caregiver_links' THEN
        v_other_user_id := NEW.caregiver_user_id;
    ELSIF TG_TABLE_NAME = 'care_invites' THEN
        v_other_user_id := NEW.used_by_user_id;
    ELSE -- patient_professional_assignments
        SELECT user_id INTO v_other_user_id FROM health_professionals WHERE id = NEW.professional_id;
    END IF;

    IF v_other_user_id = v_patient_user_id THEN
        RAISE EXCEPTION 'a patient cannot be linked to themselves (%)', TG_TABLE_NAME
            USING ERRCODE = 'check_violation', CONSTRAINT = 'self_link';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_care_invites_no_self_link BEFORE INSERT OR UPDATE ON care_invites
    FOR EACH ROW EXECUTE FUNCTION prevent_self_clinical_linking();
CREATE TRIGGER trg_caregiver_links_no_self_link BEFORE INSERT OR UPDATE ON caregiver_links
    FOR EACH ROW EXECUTE FUNCTION prevent_self_clinical_linking();
CREATE TRIGGER trg_patient_professional_assignments_no_self_link BEFORE INSERT OR UPDATE ON patient_professional_assignments
    FOR EACH ROW EXECUTE FUNCTION prevent_self_clinical_linking();

-- 10.4 The audit log is never changed or deleted. The only change allowed is
-- the one the database makes itself when a user or patient is deleted (GDPR):
-- the reference becomes NULL.
CREATE FUNCTION protect_audit_log_immutability() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE'
       OR NEW.id IS DISTINCT FROM OLD.id
       OR NEW.operation IS DISTINCT FROM OLD.operation
       OR NEW.ip_address IS DISTINCT FROM OLD.ip_address
       OR NEW.user_agent IS DISTINCT FROM OLD.user_agent
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (NEW.actor_user_id IS NOT NULL AND NEW.actor_user_id IS DISTINCT FROM OLD.actor_user_id)
       OR (NEW.target_patient_id IS NOT NULL AND NEW.target_patient_id IS DISTINCT FROM OLD.target_patient_id) THEN
        RAISE EXCEPTION 'data_access_audit_log rows cannot be changed or deleted'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'audit_log_immutable';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_data_access_audit_log_immutable BEFORE UPDATE OR DELETE ON data_access_audit_log
    FOR EACH ROW EXECUTE FUNCTION protect_audit_log_immutability();

-- 10.5 The sender's role belongs to the channel, and an attached report is
-- about the patient of the conversation.
CREATE FUNCTION verify_chat_message_integrity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_channel VARCHAR(20);
    v_assignment_id UUID;
    v_link_id UUID;
    v_conversation_patient UUID;
    v_report_patient UUID;
BEGIN
    SELECT channel_type, assignment_id, caregiver_link_id INTO v_channel, v_assignment_id, v_link_id
      FROM chat_conversations WHERE id = NEW.conversation_id;

    IF (v_channel = 'patient_doctor' AND NEW.sender_role NOT IN ('patient', 'professional'))
       OR (v_channel = 'patient_caregiver' AND NEW.sender_role NOT IN ('patient', 'caregiver'))
       OR (v_channel = 'caregiver_doctor' AND NEW.sender_role NOT IN ('caregiver', 'professional')) THEN
        RAISE EXCEPTION 'sender_role % does not belong to a % conversation', NEW.sender_role, v_channel
            USING ERRCODE = 'check_violation', CONSTRAINT = 'sender_not_in_channel';
    END IF;

    IF NEW.attached_report_id IS NOT NULL THEN
        IF v_assignment_id IS NOT NULL THEN
            SELECT patient_id INTO v_conversation_patient FROM patient_professional_assignments WHERE id = v_assignment_id;
        ELSE
            SELECT patient_id INTO v_conversation_patient FROM caregiver_links WHERE id = v_link_id;
        END IF;
        SELECT patient_id INTO v_report_patient FROM clinical_reports WHERE id = NEW.attached_report_id;

        IF v_report_patient IS DISTINCT FROM v_conversation_patient THEN
            RAISE EXCEPTION 'the attached report belongs to another patient'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'cross_patient_attachment';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_chat_messages_integrity BEFORE INSERT OR UPDATE ON chat_messages
    FOR EACH ROW EXECUTE FUNCTION verify_chat_message_integrity();

-- 10.6 In a caregiver-doctor conversation, the follow-up and the caregiver
-- link are about the same patient.
CREATE FUNCTION verify_chat_same_patient() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_assignment_patient UUID;
    v_link_patient UUID;
BEGIN
    IF NEW.channel_type = 'caregiver_doctor' THEN
        SELECT patient_id INTO v_assignment_patient FROM patient_professional_assignments WHERE id = NEW.assignment_id;
        SELECT patient_id INTO v_link_patient FROM caregiver_links WHERE id = NEW.caregiver_link_id;
        IF v_assignment_patient IS DISTINCT FROM v_link_patient THEN
            RAISE EXCEPTION 'the follow-up and the caregiver link are about different patients'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'cross_patient_conversation';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_chat_conversations_same_patient BEFORE INSERT OR UPDATE ON chat_conversations
    FOR EACH ROW EXECUTE FUNCTION verify_chat_same_patient();

-- 10.7 Only composite dishes have ingredients
CREATE FUNCTION verify_composite_dish_type() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM foods WHERE id = NEW.dish_food_id AND food_type = 'composite_dish') THEN
        RAISE EXCEPTION 'food % is not a composite_dish and cannot have ingredients', NEW.dish_food_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'not_composite_dish';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_food_components_composite_dish BEFORE INSERT OR UPDATE ON food_components
    FOR EACH ROW EXECUTE FUNCTION verify_composite_dish_type();

-- 10.8 Neither the clinician nor the patient has two scheduled or ongoing
-- consultations at the same time, even in different follow-ups. Both rows are
-- locked against simultaneous bookings.
CREATE FUNCTION verify_teleconsultation_no_overlap() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_professional_id UUID;
    v_patient_id UUID;
BEGIN
    IF NEW.status NOT IN ('scheduled', 'in_progress') THEN
        RETURN NEW;
    END IF;

    SELECT professional_id, patient_id INTO v_professional_id, v_patient_id
      FROM patient_professional_assignments WHERE id = NEW.assignment_id;

    PERFORM 1 FROM health_professionals WHERE id = v_professional_id FOR UPDATE;
    PERFORM 1 FROM patients WHERE id = v_patient_id FOR UPDATE;

    IF EXISTS (
        SELECT 1
          FROM teleconsultations t
          JOIN patient_professional_assignments a ON a.id = t.assignment_id
         WHERE t.id IS DISTINCT FROM NEW.id
           AND t.status IN ('scheduled', 'in_progress')
           AND (a.professional_id = v_professional_id OR a.patient_id = v_patient_id)
           AND consultation_period(t.scheduled_at, t.duration_minutes)
               && consultation_period(NEW.scheduled_at, NEW.duration_minutes)) THEN
        RAISE EXCEPTION 'the clinician or the patient already has a consultation at that time'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'slot_taken';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER trg_teleconsultations_no_overlap BEFORE INSERT OR UPDATE ON teleconsultations
    FOR EACH ROW EXECUTE FUNCTION verify_teleconsultation_no_overlap();


-- ---------------------------------------------------------------------------
-- 11. updated_at IS SET AUTOMATICALLY ON THE NEW TABLES
-- (organization_managers and heart_rate_samples never change: no updated_at)
-- ---------------------------------------------------------------------------
CREATE TRIGGER trg_identity_providers_updated_at BEFORE UPDATE ON identity_providers FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_genders_updated_at BEFORE UPDATE ON genders FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_diabetes_types_updated_at BEFORE UPDATE ON diabetes_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_therapy_types_updated_at BEFORE UPDATE ON therapy_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_subscription_plans_updated_at BEFORE UPDATE ON subscription_plans FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_device_types_updated_at BEFORE UPDATE ON device_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_meal_types_updated_at BEFORE UPDATE ON meal_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_insulin_types_updated_at BEFORE UPDATE ON insulin_types FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_medical_specialties_updated_at BEFORE UPDATE ON medical_specialties FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_care_invites_updated_at BEFORE UPDATE ON care_invites FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_subscription_beneficiaries_updated_at BEFORE UPDATE ON subscription_beneficiaries FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_food_categories_updated_at BEFORE UPDATE ON food_categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_food_components_updated_at BEFORE UPDATE ON food_components FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_meal_photos_updated_at BEFORE UPDATE ON meal_photos FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_alert_recipients_updated_at BEFORE UPDATE ON alert_recipients FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_iris_conversations_updated_at BEFORE UPDATE ON iris_conversations FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_professional_availability_updated_at BEFORE UPDATE ON professional_availability FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_legal_documents_updated_at BEFORE UPDATE ON legal_documents FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- ---------------------------------------------------------------------------
-- 12. INDEXES FOR THE NEW FOREIGN KEYS
-- Same rule as 000001: every foreign key gets one, unless a UNIQUE constraint
-- already starts with that column. Indexes on dropped columns went with them.
-- ---------------------------------------------------------------------------
CREATE INDEX idx_organization_managers_user_id ON organization_managers (user_id);
CREATE INDEX idx_company_invite_codes_created_by_manager_id ON company_invite_codes (created_by_manager_id);
CREATE INDEX idx_patients_gender_id ON patients (gender_id);
CREATE INDEX idx_patients_diabetes_type_id ON patients (diabetes_type_id);
CREATE INDEX idx_patients_therapy_type_id ON patients (therapy_type_id);
CREATE INDEX idx_care_invites_patient_id ON care_invites (patient_id);
CREATE INDEX idx_care_invites_used_by_user_id ON care_invites (used_by_user_id);
CREATE INDEX idx_b2c_subscriptions_payer_user_id ON b2c_subscriptions (payer_user_id);
CREATE INDEX idx_b2c_subscriptions_plan_id ON b2c_subscriptions (plan_id);
CREATE INDEX idx_subscription_beneficiaries_patient_id ON subscription_beneficiaries (patient_id);
CREATE INDEX idx_devices_device_type_id ON devices (device_type_id);
CREATE INDEX idx_foods_category_id ON foods (category_id);
CREATE INDEX idx_food_components_component_food_id ON food_components (component_food_id);
CREATE INDEX idx_meals_meal_type_id ON meals (meal_type_id);
CREATE INDEX idx_meal_photos_meal_id ON meal_photos (meal_id);
CREATE INDEX idx_insulin_logs_insulin_type_id ON insulin_logs (insulin_type_id);
CREATE INDEX idx_alerts_device_id ON alerts (device_id);
CREATE INDEX idx_alert_recipients_user_id ON alert_recipients (user_id);
CREATE INDEX idx_iris_conversations_patient_id ON iris_conversations (patient_id);
CREATE INDEX idx_iris_chat_interactions_conversation_id ON iris_chat_interactions (conversation_id);
CREATE INDEX idx_health_professionals_specialty_id ON health_professionals (specialty_id);
CREATE INDEX idx_professional_availability_professional_id ON professional_availability (professional_id);
CREATE INDEX idx_teleconsultations_assignment_scheduled ON teleconsultations (assignment_id, scheduled_at);
CREATE INDEX idx_chat_conversations_assignment_id ON chat_conversations (assignment_id);
CREATE INDEX idx_chat_conversations_caregiver_link_id ON chat_conversations (caregiver_link_id);
CREATE INDEX idx_clinical_reports_assignment_id ON clinical_reports (assignment_id);
CREATE INDEX idx_consents_document_id ON consents (document_id);

COMMIT;
