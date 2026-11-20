-- ===========================================================================
-- VOLTIK · MIGRATION 000001 · INITIAL SCHEMA (29 tables)
-- PostgreSQL 17 · applied with golang-migrate by the voltik_migrations role
--
-- Conventions
--   · identifiers: UUID with gen_random_uuid(); high-volume tables use BIGSERIAL
--   · instants: TIMESTAMPTZ (stored in UTC), columns ending in _at
--   · calendar dates: DATE, columns ending in _date
--   · booleans: columns starting with is_ / notify_ / auto_
--   · closed value sets: CHECK (... IN (...)), values in lower snake_case
--   · authentication is EXTERNAL ONLY (OpenID Connect): no passwords are stored
--
-- Timestamps (see docs/decisions/0006-timestamps-and-soft-delete.md)
--   · created_at  on EVERY table: when the row was inserted on the server
--   · updated_at  on every table whose rows can change; set automatically by
--                 the trigger set_updated_at() (section 9). Only the append-only
--                 tables have none: glucose_predictions, data_access_audit_log
--   · deleted_at  soft delete: NULL = live row, a date = deleted. Used where a
--                 real DELETE would lose clinical history, break references from
--                 historical rows, or leave offline phones with stale data.
--                 Normal queries must filter on "deleted_at IS NULL".
--   · domain times (measured_at, administered_at, triggered_at, sent_at, ...)
--     are separate columns because they can differ from created_at
--     (offline devices, entries logged after the fact).
--
-- Authors: Afonso Carvalho & Rui Passos
-- ===========================================================================


-- ---------------------------------------------------------------------------
-- 1. IDENTITY, ACCESS & CORPORATE MULTITENANCY (B2B / B2C)
-- ---------------------------------------------------------------------------
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL,                  -- returned by the identity provider; unique among live users
    full_name VARCHAR(150) NOT NULL,
    phone VARCHAR(30),
    profile_photo_url VARCHAR(500),
    account_status VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (account_status IN ('active', 'suspended', 'pending', 'deactivated')),
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ                        -- account deletion requested; anonymised later
);
CREATE UNIQUE INDEX uq_users_email_lower ON users (lower(email)) WHERE deleted_at IS NULL;

-- Every user signs in through at least one external provider (enforced by the API).
-- Rows are removed (real DELETE) when the account is deleted, so the person can sign up again.
CREATE TABLE external_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider VARCHAR(20) NOT NULL CHECK (provider IN ('google', 'microsoft', 'apple', 'facebook')),
    provider_subject VARCHAR(255) NOT NULL,       -- the OpenID Connect "sub" claim
    provider_email VARCHAR(255),
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the provider was linked
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_provider_subject UNIQUE (provider, provider_subject),
    CONSTRAINT uq_user_provider UNIQUE (user_id, provider)
);

CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_name VARCHAR(150) NOT NULL,
    tax_id VARCHAR(20) NOT NULL,                  -- Portuguese NIF; unique among live organizations
    industry VARCHAR(100),
    seat_count INT NOT NULL DEFAULT 10 CHECK (seat_count > 0),
    contract_start_date DATE NOT NULL,
    contract_end_date DATE NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'expired')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT ck_organization_contract_dates CHECK (contract_end_date >= contract_start_date)
);
CREATE UNIQUE INDEX uq_organizations_tax_id ON organizations (tax_id) WHERE deleted_at IS NULL;

CREATE TABLE user_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_type VARCHAR(30) NOT NULL
        CHECK (profile_type IN ('patient', 'caregiver', 'health_professional', 'company_manager', 'admin')),
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE, -- only for company_manager
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_user_profile_type UNIQUE (user_id, profile_type),
    CONSTRAINT ck_manager_has_organization CHECK ((profile_type = 'company_manager') = (organization_id IS NOT NULL))
);

CREATE TABLE company_invite_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    code VARCHAR(30) UNIQUE NOT NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    used_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'used', 'expired', 'revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_invite_used_at CHECK (status <> 'used' OR used_at IS NOT NULL)
);

-- ---------------------------------------------------------------------------
-- 2. PATIENT CLINICAL CORE & CAREGIVER NETWORK
-- ---------------------------------------------------------------------------
CREATE TABLE patients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- one live patient per user
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL, -- employer (B2B), if any
    birth_date DATE NOT NULL,
    gender VARCHAR(20) CHECK (gender IN ('male', 'female', 'other', 'undisclosed')),
    occupation VARCHAR(100),
    diabetes_type VARCHAR(20) NOT NULL CHECK (diabetes_type IN ('type_1', 'type_2', 'gestational', 'lada', 'mody')),
    current_therapy VARCHAR(30)
        CHECK (current_therapy IN ('multiple_daily_injections', 'insulin_pump', 'oral_medication', 'diet_only')),
    target_min_mg_dl INT NOT NULL DEFAULT 70 CHECK (target_min_mg_dl >= 40),
    target_max_mg_dl INT NOT NULL DEFAULT 180,
    critical_low_mg_dl INT NOT NULL DEFAULT 55,
    critical_high_mg_dl INT NOT NULL DEFAULT 250,
    weight_kg DECIMAL(5,2) CHECK (weight_kg > 0),
    height_cm INT CHECK (height_cm > 0),
    clinical_notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,                       -- clinical record kept, hidden from the apps
    CONSTRAINT ck_patient_glucose_thresholds CHECK (
        critical_low_mg_dl >= 20
        AND critical_low_mg_dl < target_min_mg_dl
        AND target_min_mg_dl < target_max_mg_dl
        AND target_max_mg_dl < critical_high_mg_dl
        AND critical_high_mg_dl <= 550)
);
CREATE UNIQUE INDEX uq_patients_user_id ON patients (user_id) WHERE deleted_at IS NULL;

-- Payer is user_id (the patient or a caregiver); the covered patient is beneficiary_patient_id.
CREATE TABLE b2c_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    beneficiary_patient_id UUID REFERENCES patients(id) ON DELETE CASCADE,
    plan VARCHAR(30) NOT NULL CHECK (plan IN ('free', 'premium_individual', 'premium_family')),
    monthly_price DECIMAL(10,2) NOT NULL DEFAULT 0.00 CHECK (monthly_price >= 0),
    start_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    end_at TIMESTAMPTZ,
    auto_renew BOOLEAN NOT NULL DEFAULT TRUE,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled', 'expired', 'payment_pending')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_subscription_dates CHECK (end_at IS NULL OR end_at >= start_at)
);
-- A patient is covered by at most one active subscription
CREATE UNIQUE INDEX uq_b2c_subscriptions_active_beneficiary
    ON b2c_subscriptions (beneficiary_patient_id) WHERE status = 'active';

CREATE TABLE emergency_contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    full_name VARCHAR(150) NOT NULL,
    relationship VARCHAR(50),
    phone VARCHAR(30) NOT NULL,
    call_priority INT NOT NULL DEFAULT 1 CHECK (call_priority > 0),
    notify_by_sms BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE caregiver_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    caregiver_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission_level VARCHAR(30) NOT NULL DEFAULT 'alerts_and_values'
        CHECK (permission_level IN ('alerts_only', 'alerts_and_values', 'full_access')),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending_approval', 'revoked')),
    linked_at TIMESTAMPTZ,                        -- when the patient approved the link
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the link was requested
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_patient_caregiver UNIQUE (patient_id, caregiver_user_id), -- re-linking reactivates this row
    CONSTRAINT ck_caregiver_link_dates CHECK (
        (status <> 'active' OR linked_at IS NOT NULL) AND (status <> 'revoked' OR revoked_at IS NOT NULL))
);

-- ---------------------------------------------------------------------------
-- 3. REAL-TIME TELEMETRY & MEDICAL DEVICES (CGM & WEARABLES)
-- ---------------------------------------------------------------------------
CREATE TABLE devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    device_type VARCHAR(20) NOT NULL CHECK (device_type IN ('cgm_sensor', 'smartwatch', 'smartband', 'glucometer')),
    brand_model VARCHAR(100) NOT NULL,
    serial_or_mac VARCHAR(100),
    connection_status VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (connection_status IN ('active', 'disconnected', 'warming_up', 'expired', 'replaced')),
    battery_percent INT CHECK (battery_percent BETWEEN 0 AND 100),
    first_reading_at TIMESTAMPTZ,
    sensor_expires_at TIMESTAMPTZ,
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ                        -- removed by the user; old readings still point to it
);

CREATE TABLE glucose_readings (
    id BIGSERIAL PRIMARY KEY,
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    measured_at TIMESTAMPTZ NOT NULL,             -- when the sensor/meter took the reading
    value_mg_dl INT NOT NULL CHECK (value_mg_dl BETWEEN 20 AND 550),
    trend VARCHAR(20) NOT NULL DEFAULT 'stable'
        CHECK (trend IN ('rising_fast', 'rising', 'stable', 'falling', 'falling_fast', 'unknown')),
    source VARCHAR(30) NOT NULL DEFAULT 'cgm_sensor'
        CHECK (source IN ('cgm_sensor', 'manual_fingerstick', 'healthkit_sync', 'health_connect_sync')),
    signal_quality INT DEFAULT 100 CHECK (signal_quality BETWEEN 0 AND 100),
    idempotency_key VARCHAR(100) UNIQUE,          -- generated on the phone; makes offline re-sync safe
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the reading reached the server
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ                        -- e.g. a wrong manual fingerstick removed by the patient
);

CREATE TABLE physical_activity_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    activity_name VARCHAR(100) NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    duration_minutes INT NOT NULL CHECK (duration_minutes > 0),
    intensity VARCHAR(20) NOT NULL CHECK (intensity IN ('light', 'moderate', 'vigorous')),
    total_steps INT CHECK (total_steps >= 0),
    avg_heart_rate_bpm INT CHECK (avg_heart_rate_bpm > 0),
    calories_burned_kcal INT CHECK (calories_burned_kcal >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

-- ---------------------------------------------------------------------------
-- 4. NUTRITION (ALIGNED WITH THE FATSECRET PLATFORM API)
-- ---------------------------------------------------------------------------
CREATE TABLE foods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fatsecret_food_id BIGINT,                     -- unique among live foods
    barcode_ean VARCHAR(50),                      -- unique among live foods
    food_name VARCHAR(200) NOT NULL,
    brand VARCHAR(100),
    food_type VARCHAR(20) NOT NULL DEFAULT 'generic' CHECK (food_type IN ('generic', 'brand')),
    category VARCHAR(80),
    data_source VARCHAR(25) NOT NULL DEFAULT 'fatsecret'
        CHECK (data_source IN ('fatsecret', 'portuguese_traditional', 'user_custom')),
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,   -- reviewed by the Voltik nutrition team
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ                        -- hidden from search; past meals still use it
);
CREATE UNIQUE INDEX uq_foods_fatsecret_food_id ON foods (fatsecret_food_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_foods_barcode_ean ON foods (barcode_ean) WHERE deleted_at IS NULL;

CREATE TABLE food_servings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    food_id UUID NOT NULL REFERENCES foods(id) ON DELETE CASCADE,
    fatsecret_serving_id BIGINT,
    serving_description VARCHAR(100) NOT NULL,
    metric_amount DECIMAL(8,2),
    metric_unit VARCHAR(20),
    carbs_g DECIMAL(6,2) NOT NULL CHECK (carbs_g >= 0),
    fiber_g DECIMAL(6,2) DEFAULT 0.00,
    sugars_g DECIMAL(6,2) DEFAULT 0.00,
    calories_kcal DECIMAL(7,2) NOT NULL CHECK (calories_kcal >= 0),
    protein_g DECIMAL(6,2) NOT NULL DEFAULT 0.00,
    fat_g DECIMAL(6,2) NOT NULL DEFAULT 0.00,
    saturated_fat_g DECIMAL(6,2) DEFAULT 0.00,
    sodium_mg DECIMAL(7,2) DEFAULT 0.00,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_food_servings_description ON food_servings (food_id, serving_description) WHERE deleted_at IS NULL;

CREATE TABLE meals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    meal_type VARCHAR(20) NOT NULL CHECK (meal_type IN ('breakfast', 'lunch', 'afternoon_snack', 'dinner', 'other')),
    meal_date DATE NOT NULL,
    planned_time TIME,
    status VARCHAR(20) NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'eaten', 'cancelled')),
    eaten_at TIMESTAMPTZ,
    total_carbs_g DECIMAL(6,2) NOT NULL DEFAULT 0.00,
    total_calories_kcal DECIMAL(7,2) NOT NULL DEFAULT 0.00,
    postprandial_delta_mg_dl INT,                 -- glucose change after the meal
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT ck_meal_eaten_at CHECK (status <> 'eaten' OR eaten_at IS NOT NULL)
);

CREATE TABLE meal_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meal_id UUID NOT NULL REFERENCES meals(id) ON DELETE CASCADE,
    serving_id UUID NOT NULL REFERENCES food_servings(id) ON DELETE RESTRICT, -- the food is serving_id -> food_id
    serving_quantity DECIMAL(4,2) NOT NULL DEFAULT 1.00 CHECK (serving_quantity > 0),
    carbs_g DECIMAL(6,2) NOT NULL,                -- serving carbs x quantity, frozen at logging time
    calories_kcal DECIMAL(7,2) NOT NULL,
    is_eaten BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE insulin_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    meal_id UUID REFERENCES meals(id) ON DELETE SET NULL,
    insulin_type VARCHAR(20) NOT NULL CHECK (insulin_type IN ('rapid_bolus', 'basal', 'premixed')),
    insulin_brand VARCHAR(80),
    dose_units DECIMAL(4,1) NOT NULL CHECK (dose_units > 0),
    administered_at TIMESTAMPTZ NOT NULL,
    reason VARCHAR(20) NOT NULL DEFAULT 'meal' CHECK (reason IN ('meal', 'correction', 'scheduled_basal', 'mixed')),
    notes VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

-- ---------------------------------------------------------------------------
-- 5. IRIS AI ENGINE & CLINICAL ALERTS
-- ---------------------------------------------------------------------------
-- Append-only: predictions are never edited, so there is no updated_at.
CREATE TABLE glucose_predictions (
    id BIGSERIAL PRIMARY KEY,
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    horizon_minutes INT NOT NULL DEFAULT 30 CHECK (horizon_minutes > 0),
    predicted_mg_dl INT NOT NULL,
    ci_low_mg_dl INT NOT NULL,                    -- confidence interval
    ci_high_mg_dl INT NOT NULL,
    predicted_risk VARCHAR(30) NOT NULL DEFAULT 'none'
        CHECK (predicted_risk IN ('none', 'imminent_hypoglycemia', 'imminent_hyperglycemia')),
    model_confidence_percent DECIMAL(5,2) CHECK (model_confidence_percent BETWEEN 0 AND 100),
    model_version VARCHAR(50) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the prediction was generated
    CONSTRAINT ck_prediction_interval CHECK (ci_low_mg_dl <= predicted_mg_dl AND predicted_mg_dl <= ci_high_mg_dl)
);

CREATE TABLE alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    source_reading_id BIGINT REFERENCES glucose_readings(id) ON DELETE SET NULL,
    source_prediction_id BIGINT REFERENCES glucose_predictions(id) ON DELETE SET NULL,
    alert_type VARCHAR(30) NOT NULL CHECK (alert_type IN (
        'predicted_low', 'predicted_high', 'critical_low', 'critical_high', 'sensor_disconnected', 'sensor_expiring')),
    severity VARCHAR(20) NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    title VARCHAR(150) NOT NULL,
    message TEXT NOT NULL,
    iris_suggested_action TEXT,
    triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- may be earlier than created_at if raised offline
    ack_status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (ack_status IN ('pending', 'acknowledged', 'resolved', 'dismissed')),
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    resolution_notes VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_alert_acknowledged_at CHECK (ack_status = 'pending' OR acknowledged_at IS NOT NULL)
);

-- Deleting an Iris conversation is a real DELETE (privacy), so there is no deleted_at.
CREATE TABLE iris_chat_interactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    user_question TEXT NOT NULL,
    iris_answer TEXT NOT NULL,
    clinical_context_snapshot JSONB,              -- anonymised context sent to the model
    user_rating VARCHAR(20) CHECK (user_rating IN ('helpful', 'not_helpful', 'incorrect')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the question was asked
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()  -- changes when the user rates the answer
);

-- ---------------------------------------------------------------------------
-- 6. TELEMEDICINE, CONSULTATIONS & CLINICAL COMMUNICATION
-- ---------------------------------------------------------------------------
-- Professionals are soft-deleted only: their consultations and messages are clinical history.
CREATE TABLE health_professionals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    license_number VARCHAR(50) NOT NULL,          -- professional licence number
    specialty VARCHAR(30) NOT NULL
        CHECK (specialty IN ('endocrinology', 'general_practice', 'diabetes_nursing', 'clinical_nutrition')),
    institution VARCHAR(150) DEFAULT 'Voltik clinical team',
    bio TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ                        -- left Voltik; no new patients or consultations
);
CREATE UNIQUE INDEX uq_health_professionals_user_id ON health_professionals (user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_health_professionals_license ON health_professionals (license_number) WHERE deleted_at IS NULL;

CREATE TABLE patient_professional_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    professional_id UUID NOT NULL REFERENCES health_professionals(id) ON DELETE CASCADE,
    start_date DATE NOT NULL DEFAULT CURRENT_DATE,
    end_date DATE,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'transferred')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_patient_professional UNIQUE (patient_id, professional_id),
    CONSTRAINT ck_assignment_dates CHECK (end_date IS NULL OR end_date >= start_date)
);

CREATE TABLE teleconsultations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    professional_id UUID NOT NULL REFERENCES health_professionals(id) ON DELETE RESTRICT,
    scheduled_at TIMESTAMPTZ NOT NULL,
    duration_minutes INT NOT NULL DEFAULT 30 CHECK (duration_minutes > 0),
    consultation_type VARCHAR(20) NOT NULL DEFAULT 'video' CHECK (consultation_type IN ('video', 'async_review')),
    status VARCHAR(20) NOT NULL DEFAULT 'scheduled'
        CHECK (status IN ('scheduled', 'in_progress', 'completed', 'cancelled', 'no_show')),
    webrtc_room_token VARCHAR(100) UNIQUE,
    clinical_notes TEXT,
    adjusted_treatment_plan TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_teleconsultation_assignment FOREIGN KEY (patient_id, professional_id)
        REFERENCES patient_professional_assignments (patient_id, professional_id) ON DELETE RESTRICT
);

-- A conversation is always about one patient, between two parties given by channel_type.
CREATE TABLE chat_conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    channel_type VARCHAR(20) NOT NULL CHECK (channel_type IN ('patient_doctor', 'patient_caregiver', 'caregiver_doctor')),
    professional_id UUID REFERENCES health_professionals(id) ON DELETE RESTRICT,
    caregiver_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    topic VARCHAR(100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_conversation_parties CHECK (
           (channel_type = 'patient_doctor'    AND professional_id IS NOT NULL AND caregiver_user_id IS NULL)
        OR (channel_type = 'patient_caregiver' AND professional_id IS NULL     AND caregiver_user_id IS NOT NULL)
        OR (channel_type = 'caregiver_doctor'  AND professional_id IS NOT NULL AND caregiver_user_id IS NOT NULL)),
    -- the caregiver must be linked to the patient, the professional assigned to them
    CONSTRAINT fk_conversation_caregiver_link FOREIGN KEY (patient_id, caregiver_user_id)
        REFERENCES caregiver_links (patient_id, caregiver_user_id) ON DELETE CASCADE,
    CONSTRAINT fk_conversation_assignment FOREIGN KEY (patient_id, professional_id)
        REFERENCES patient_professional_assignments (patient_id, professional_id) ON DELETE RESTRICT,
    CONSTRAINT uq_conversation_parties UNIQUE NULLS NOT DISTINCT (patient_id, channel_type, professional_id, caregiver_user_id)
);

CREATE TABLE clinical_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    period_type VARCHAR(20) NOT NULL CHECK (period_type IN ('day', 'week', 'month', 'custom')),
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    time_in_range_percent DECIMAL(5,2) NOT NULL,       -- TIR
    time_below_range_percent DECIMAL(5,2) NOT NULL DEFAULT 0.00, -- TBR
    time_above_range_percent DECIMAL(5,2) NOT NULL DEFAULT 0.00, -- TAR
    mean_glucose_mg_dl INT NOT NULL,
    std_dev_mg_dl DECIMAL(5,2),
    coefficient_of_variation_percent DECIMAL(5,2),
    gmi_percent DECIMAL(4,2),                     -- glucose management indicator (estimated HbA1c)
    hypo_episodes INT NOT NULL DEFAULT 0,
    hyper_episodes INT NOT NULL DEFAULT 0,
    work_impact_analysis TEXT,
    iris_summary TEXT,
    pdf_file_url VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the report was generated
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,                       -- removed from the patient's list; kept for the clinician
    CONSTRAINT ck_report_dates CHECK (end_date >= start_date),
    CONSTRAINT ck_report_percentages CHECK (
        time_in_range_percent BETWEEN 0 AND 100
        AND time_below_range_percent BETWEEN 0 AND 100
        AND time_above_range_percent BETWEEN 0 AND 100
        AND time_in_range_percent + time_below_range_percent + time_above_range_percent BETWEEN 99.9 AND 100.1),
    CONSTRAINT ck_report_mean CHECK (mean_glucose_mg_dl > 0)
);

CREATE TABLE chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    sender_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    attachment_type VARCHAR(25) NOT NULL DEFAULT 'none'
        CHECK (attachment_type IN ('none', 'clinical_report_pdf', 'glucose_chart', 'meal_photo')),
    attached_report_id UUID REFERENCES clinical_reports(id) ON DELETE SET NULL,
    attachment_url VARCHAR(500),                  -- chart or meal photo file
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),   -- on the sender's device (may be offline)
    read_at TIMESTAMPTZ,                          -- NULL = unread
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,                       -- shown as "message deleted"
    CONSTRAINT ck_message_report_attachment CHECK (attached_report_id IS NULL OR attachment_type = 'clinical_report_pdf'),
    CONSTRAINT ck_message_no_attachment CHECK (attachment_type <> 'none' OR (attached_report_id IS NULL AND attachment_url IS NULL))
);

-- ---------------------------------------------------------------------------
-- 7. GDPR: AUDIT LOG, CONSENTS & NOTIFICATIONS
-- ---------------------------------------------------------------------------
-- Append-only: never updated or deleted (see section 10), so there is no updated_at.
CREATE TABLE data_access_audit_log (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    target_patient_id UUID REFERENCES patients(id) ON DELETE SET NULL,
    operation VARCHAR(40) NOT NULL CHECK (operation IN (
        'realtime_glucose_view', 'report_pdf_export', 'therapy_change', 'food_diary_view',
        'caregiver_revoked', 'consent_granted', 'consent_revoked')),
    ip_address INET NOT NULL,
    user_agent VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()  -- when the access happened
);

CREATE TABLE consents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    consent_type VARCHAR(40) NOT NULL
        CHECK (consent_type IN ('terms_of_service', 'health_data_processing', 'external_ai_processing', 'beta_testing')),
    document_version VARCHAR(20) NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the user accepted
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_consent_version UNIQUE (user_id, consent_type, document_version)
);

CREATE TABLE notification_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform VARCHAR(10) NOT NULL CHECK (platform IN ('android', 'ios', 'web')),
    push_token VARCHAR(500) UNIQUE NOT NULL,      -- FCM (Android/web) or APNs (iOS) token
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 8. INDEXES
-- PostgreSQL indexes primary keys and UNIQUE columns automatically, but NOT
-- foreign keys. Every foreign key gets one here, unless a UNIQUE constraint
-- already starts with that column (e.g. external_identities, consents,
-- caregiver_links, patient_professional_assignments).
-- ---------------------------------------------------------------------------
CREATE INDEX idx_user_profiles_organization_id ON user_profiles (organization_id);
CREATE INDEX idx_company_invite_codes_organization_id ON company_invite_codes (organization_id);
CREATE INDEX idx_company_invite_codes_created_by_user_id ON company_invite_codes (created_by_user_id);
CREATE INDEX idx_company_invite_codes_used_by_user_id ON company_invite_codes (used_by_user_id);
CREATE INDEX idx_patients_organization_id ON patients (organization_id);
CREATE INDEX idx_b2c_subscriptions_user_id ON b2c_subscriptions (user_id);
CREATE INDEX idx_b2c_subscriptions_beneficiary_patient_id ON b2c_subscriptions (beneficiary_patient_id);
CREATE INDEX idx_emergency_contacts_patient_id ON emergency_contacts (patient_id);
CREATE INDEX idx_caregiver_links_caregiver_user_id ON caregiver_links (caregiver_user_id);
CREATE INDEX idx_devices_patient_id ON devices (patient_id);
CREATE INDEX idx_glucose_readings_patient_measured ON glucose_readings (patient_id, measured_at DESC);
CREATE INDEX idx_glucose_readings_patient_updated ON glucose_readings (patient_id, updated_at); -- sync: "what changed since"
CREATE INDEX idx_glucose_readings_device_id ON glucose_readings (device_id);
CREATE INDEX idx_physical_activity_logs_patient_id ON physical_activity_logs (patient_id);
CREATE INDEX idx_physical_activity_logs_device_id ON physical_activity_logs (device_id);
CREATE INDEX idx_foods_food_name ON foods (food_name);
CREATE INDEX idx_foods_created_by_user_id ON foods (created_by_user_id);
CREATE INDEX idx_meals_patient_date ON meals (patient_id, meal_date DESC);
CREATE INDEX idx_meal_items_meal_id ON meal_items (meal_id);
CREATE INDEX idx_meal_items_serving_id ON meal_items (serving_id);
CREATE INDEX idx_insulin_logs_patient_id ON insulin_logs (patient_id);
CREATE INDEX idx_insulin_logs_meal_id ON insulin_logs (meal_id);
CREATE INDEX idx_glucose_predictions_patient_id ON glucose_predictions (patient_id);
CREATE INDEX idx_alerts_patient_triggered ON alerts (patient_id, triggered_at DESC);
CREATE INDEX idx_alerts_source_reading_id ON alerts (source_reading_id);
CREATE INDEX idx_alerts_source_prediction_id ON alerts (source_prediction_id);
CREATE INDEX idx_alerts_acknowledged_by_user_id ON alerts (acknowledged_by_user_id);
CREATE INDEX idx_iris_chat_interactions_patient_id ON iris_chat_interactions (patient_id);
CREATE INDEX idx_patient_professional_assignments_professional_id ON patient_professional_assignments (professional_id);
CREATE INDEX idx_teleconsultations_patient_id ON teleconsultations (patient_id);
CREATE INDEX idx_teleconsultations_professional_scheduled ON teleconsultations (professional_id, scheduled_at);
CREATE INDEX idx_chat_conversations_professional_id ON chat_conversations (professional_id);
CREATE INDEX idx_chat_conversations_caregiver_user_id ON chat_conversations (caregiver_user_id);
CREATE INDEX idx_clinical_reports_patient_id ON clinical_reports (patient_id);
CREATE INDEX idx_chat_messages_conversation_sent ON chat_messages (conversation_id, sent_at);
CREATE INDEX idx_chat_messages_sender_user_id ON chat_messages (sender_user_id);
CREATE INDEX idx_chat_messages_attached_report_id ON chat_messages (attached_report_id);
CREATE INDEX idx_data_access_audit_log_actor_user_id ON data_access_audit_log (actor_user_id);
CREATE INDEX idx_data_access_audit_log_target_patient_id ON data_access_audit_log (target_patient_id);
CREATE INDEX idx_notification_tokens_user_id ON notification_tokens (user_id);

-- ---------------------------------------------------------------------------
-- 9. updated_at IS SET AUTOMATICALLY
-- Every UPDATE on these tables stamps updated_at with the current time, so the
-- API never has to remember it. New tables with updated_at need their own trigger.
-- ---------------------------------------------------------------------------
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := NOW();
    RETURN NEW;
END
$$;

CREATE TRIGGER trg_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_external_identities_updated_at BEFORE UPDATE ON external_identities FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_organizations_updated_at BEFORE UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_user_profiles_updated_at BEFORE UPDATE ON user_profiles FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_company_invite_codes_updated_at BEFORE UPDATE ON company_invite_codes FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_patients_updated_at BEFORE UPDATE ON patients FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_b2c_subscriptions_updated_at BEFORE UPDATE ON b2c_subscriptions FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_emergency_contacts_updated_at BEFORE UPDATE ON emergency_contacts FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_caregiver_links_updated_at BEFORE UPDATE ON caregiver_links FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_devices_updated_at BEFORE UPDATE ON devices FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_glucose_readings_updated_at BEFORE UPDATE ON glucose_readings FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_physical_activity_logs_updated_at BEFORE UPDATE ON physical_activity_logs FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_foods_updated_at BEFORE UPDATE ON foods FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_food_servings_updated_at BEFORE UPDATE ON food_servings FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_meals_updated_at BEFORE UPDATE ON meals FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_meal_items_updated_at BEFORE UPDATE ON meal_items FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_insulin_logs_updated_at BEFORE UPDATE ON insulin_logs FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_alerts_updated_at BEFORE UPDATE ON alerts FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_iris_chat_interactions_updated_at BEFORE UPDATE ON iris_chat_interactions FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_health_professionals_updated_at BEFORE UPDATE ON health_professionals FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_patient_professional_assignments_updated_at BEFORE UPDATE ON patient_professional_assignments FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_teleconsultations_updated_at BEFORE UPDATE ON teleconsultations FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_chat_conversations_updated_at BEFORE UPDATE ON chat_conversations FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_clinical_reports_updated_at BEFORE UPDATE ON clinical_reports FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_chat_messages_updated_at BEFORE UPDATE ON chat_messages FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_consents_updated_at BEFORE UPDATE ON consents FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_notification_tokens_updated_at BEFORE UPDATE ON notification_tokens FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- 10. INSERT-ONLY AUDIT LOG: the API can record accesses but never change or delete them
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'voltik_api') THEN
        REVOKE UPDATE, DELETE ON data_access_audit_log FROM voltik_api;
    END IF;
END
$$;