-- =============================================================================
-- 000003 · DVT-79 health data: food search, devices, diary, alerts, reports
-- and files. Run as voltik_migrations, after 000002 (SRS v1.0).
-- =============================================================================
BEGIN;

-- ---------------------------------------------------------------------------
-- 1. Accent- and case-insensitive food search ("pao" finds "Pão", UC093)
-- ---------------------------------------------------------------------------
-- Both extensions are trusted, like btree_gist in 000002.
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- unaccent() is only STABLE because its dictionary could change. This wrapper
-- names the dictionary, so it can be used in an index.
CREATE FUNCTION voltik_search_key(value text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    RETURN lower(public.unaccent('public.unaccent'::regdictionary, value));

-- ---------------------------------------------------------------------------
-- 2. devices: one live row per serial or MAC, and the Nightscout upload secret
-- ---------------------------------------------------------------------------
-- Registering the same serial again returns the existing device (contract).
-- Older live duplicates, if any, are removed first.
UPDATE devices d
   SET deleted_at = now(), connection_status = 'disconnected'
  FROM devices newer
 WHERE newer.patient_id = d.patient_id AND newer.serial_or_mac = d.serial_or_mac
   AND newer.deleted_at IS NULL AND d.deleted_at IS NULL
   AND (newer.created_at, newer.id) > (d.created_at, d.id);
CREATE UNIQUE INDEX uq_devices_patient_serial ON devices (patient_id, serial_or_mac)
 WHERE deleted_at IS NULL AND serial_or_mac IS NOT NULL;

-- Real sensors read by Juggluco or xDrip+ upload through the Nightscout API.
-- Only the SHA-256 of the secret is kept.
ALTER TABLE devices ADD COLUMN nightscout_secret_hash BYTEA
    CHECK (octet_length(nightscout_secret_hash) = 32);
CREATE UNIQUE INDEX uq_devices_nightscout_secret ON devices (nightscout_secret_hash)
 WHERE nightscout_secret_hash IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 3. foods: who created a custom food, and the new sources
-- ---------------------------------------------------------------------------
-- UC095: a user_custom food is visible only to its author until it is
-- verified (000002 had dropped the column). The food stays if the account is
-- erased, because other meals may use it.
ALTER TABLE foods
    ADD COLUMN created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    DROP CONSTRAINT foods_data_source_check,
    ADD CONSTRAINT foods_data_source_check CHECK (data_source IN
        ('fatsecret', 'portuguese_traditional', 'user_custom', 'insa', 'open_food_facts')),
    ALTER COLUMN data_source SET DEFAULT 'user_custom';
CREATE INDEX idx_foods_created_by_user_id ON foods (created_by_user_id);
CREATE INDEX idx_foods_search ON foods USING gin (voltik_search_key(food_name) gin_trgm_ops)
 WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- 4. food_servings: fibre is not part of the carbohydrates in Europe
-- ---------------------------------------------------------------------------
-- In the INSA table and on EU labels "hidratos de carbono" exclude fibre, so
-- fibre can be higher (avocado: 2.3 g of carbohydrates and 3 g of fibre).
ALTER TABLE food_servings
    DROP CONSTRAINT ck_serving_macros_hierarchy,
    ADD CONSTRAINT ck_serving_macros_hierarchy CHECK (sugars_g <= carbs_g AND saturated_fat_g <= fat_g);

-- ---------------------------------------------------------------------------
-- 5. stored_files: meal photos and report PDFs (ADR 0007)
-- ---------------------------------------------------------------------------
-- Kept in the database: the backups already include them, and they are
-- erased with the patient (CASCADE). The API serves them through signed links.
CREATE TABLE stored_files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id UUID NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
    purpose VARCHAR(20) NOT NULL CHECK (purpose IN ('meal_photo', 'report_pdf')),
    content_type VARCHAR(50) NOT NULL
        CHECK (content_type IN ('image/jpeg', 'image/png', 'image/webp', 'application/pdf')),
    size_bytes INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    sha256 BYTEA NOT NULL CHECK (octet_length(sha256) = 32),
    data BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_stored_file_size CHECK (octet_length(data) = size_bytes)
);
CREATE INDEX idx_stored_files_patient_id ON stored_files (patient_id);

-- ---------------------------------------------------------------------------
-- 6. meal_photos: the image, the analysis retries and the patient's answer
-- ---------------------------------------------------------------------------
ALTER TABLE meal_photos
    ADD COLUMN file_id UUID REFERENCES stored_files(id) ON DELETE CASCADE,
    ADD COLUMN analysis_attempts SMALLINT NOT NULL DEFAULT 0 CHECK (analysis_attempts BETWEEN 0 AND 10),
    ADD COLUMN confirmed_at TIMESTAMPTZ,
    ADD CONSTRAINT ck_meal_photo_confirmed CHECK (confirmed_at IS NULL OR analysis_status = 'completed');
CREATE INDEX idx_meal_photos_file_id ON meal_photos (file_id);
CREATE INDEX idx_meal_photos_pending ON meal_photos (created_at)
 WHERE analysis_status = 'pending' AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- 7. meals: the post-meal impact is computed once (UC108)
-- ---------------------------------------------------------------------------
ALTER TABLE meals ADD COLUMN postprandial_computed_at TIMESTAMPTZ;
CREATE INDEX idx_meals_postprandial_due ON meals (eaten_at)
 WHERE status = 'eaten' AND postprandial_computed_at IS NULL AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- 8. alerts: escalation of critical alerts nobody acknowledged (UC117)
-- ---------------------------------------------------------------------------
-- escalation_step 0 = not escalated; 1 = caregivers texted; 2.. = emergency
-- contacts by priority. next_escalation_at is cleared when the alert leaves
-- pending or the contacts run out.
ALTER TABLE alerts
    ADD COLUMN escalation_step SMALLINT NOT NULL DEFAULT 0 CHECK (escalation_step BETWEEN 0 AND 20),
    ADD COLUMN next_escalation_at TIMESTAMPTZ,
    ADD CONSTRAINT ck_alert_escalation CHECK (
        next_escalation_at IS NULL OR (severity = 'critical' AND ack_status = 'pending'));
CREATE INDEX idx_alerts_escalation_due ON alerts (next_escalation_at) WHERE next_escalation_at IS NOT NULL;
CREATE INDEX idx_alerts_triggered_at ON alerts (triggered_at DESC);

-- ---------------------------------------------------------------------------
-- 9. alert_recipients: retries and the provider's message id (UC116, UC121)
-- ---------------------------------------------------------------------------
-- Pending push rows are the outbox of the push sender.
ALTER TABLE alert_recipients
    ADD COLUMN provider_message_id VARCHAR(200) CHECK (btrim(provider_message_id) <> ''),
    ADD COLUMN attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 10),
    ADD COLUMN next_attempt_at TIMESTAMPTZ,
    ADD COLUMN last_error VARCHAR(255);
CREATE INDEX idx_alert_recipients_outbox ON alert_recipients (next_attempt_at)
 WHERE delivery_status = 'pending' AND channel = 'push';

-- ---------------------------------------------------------------------------
-- 10. sms_messages: every escalation SMS (UC117)
-- ---------------------------------------------------------------------------
-- Caregivers have an sms row in alert_recipients; emergency contacts have no
-- account, so this table is the record of what was sent to them.
CREATE TABLE sms_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    alert_recipient_id UUID REFERENCES alert_recipients(id) ON DELETE CASCADE,
    emergency_contact_id UUID REFERENCES emergency_contacts(id) ON DELETE SET NULL,
    escalation_step SMALLINT NOT NULL CHECK (escalation_step BETWEEN 1 AND 20),
    phone VARCHAR(30) NOT NULL CHECK (btrim(phone) <> ''),
    body VARCHAR(480) NOT NULL CHECK (btrim(body) <> ''),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'delivered', 'failed')),
    provider VARCHAR(20) CHECK (provider IN ('log', 'twilio')),
    provider_message_id VARCHAR(100) UNIQUE CHECK (btrim(provider_message_id) <> ''),
    attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 10),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    last_error VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_sms_single_target CHECK (num_nonnulls(alert_recipient_id, emergency_contact_id) <= 1),
    CONSTRAINT ck_sms_state_dates CHECK (
            (status <> 'pending' OR sent_at IS NULL)
        AND (status NOT IN ('sent', 'delivered') OR sent_at IS NOT NULL)
        AND (status <> 'delivered' OR delivered_at IS NOT NULL)
        AND (delivered_at IS NULL OR delivered_at >= sent_at))
);
CREATE INDEX idx_sms_messages_due ON sms_messages (next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_sms_messages_alert_id ON sms_messages (alert_id);
CREATE INDEX idx_sms_messages_alert_recipient_id ON sms_messages (alert_recipient_id);
CREATE INDEX idx_sms_messages_emergency_contact_id ON sms_messages (emergency_contact_id);
CREATE TRIGGER trg_sms_messages_updated_at BEFORE UPDATE ON sms_messages
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- 11. patients: weekly reports can be switched off (UC147)
-- ---------------------------------------------------------------------------
ALTER TABLE patients ADD COLUMN weekly_reports_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- ---------------------------------------------------------------------------
-- 12. clinical_reports: what the report was computed with, and its PDF
-- ---------------------------------------------------------------------------
-- The patient's target range can change later; the report keeps the one it
-- used. Reports from before 000003 have NULL here.
ALTER TABLE clinical_reports
    ADD COLUMN target_min_mg_dl INTEGER CHECK (target_min_mg_dl BETWEEN 40 AND 550),
    ADD COLUMN target_max_mg_dl INTEGER CHECK (target_max_mg_dl BETWEEN 20 AND 550),
    ADD COLUMN coverage_percent DECIMAL(5,2) CHECK (coverage_percent BETWEEN 0.00 AND 100.00),
    ADD COLUMN pdf_file_id UUID REFERENCES stored_files(id) ON DELETE SET NULL;
CREATE INDEX idx_clinical_reports_pdf_file_id ON clinical_reports (pdf_file_id);
-- One weekly report per patient and week, even with several API instances,
-- and never again after the patient deleted it.
CREATE UNIQUE INDEX uq_clinical_reports_weekly ON clinical_reports (patient_id, start_date)
 WHERE generated_by_role = 'system_scheduled';

-- ---------------------------------------------------------------------------
-- 13. Indexes for the new queries
-- ---------------------------------------------------------------------------
CREATE INDEX idx_insulin_logs_patient_administered ON insulin_logs (patient_id, administered_at);
CREATE INDEX idx_physical_activity_logs_patient_started ON physical_activity_logs (patient_id, started_at);
CREATE INDEX idx_glucose_predictions_patient_created ON glucose_predictions (patient_id, created_at DESC);
CREATE INDEX idx_caregiver_links_patient_active ON caregiver_links (patient_id) WHERE status = 'active';
CREATE INDEX idx_heart_rate_samples_measured ON heart_rate_samples (device_id, measured_at DESC);

COMMIT;
