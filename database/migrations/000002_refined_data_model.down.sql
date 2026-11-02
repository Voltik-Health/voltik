-- ===========================================================================
-- VOLTIK · MIGRATION 000002 · ROLLBACK (48 -> 29 tables)
--
-- Restores the schema of 000001 exactly and converts the rows back where the
-- old model can hold them. Meant for development and staging. Data the old
-- model cannot hold is lost:
--   · the new tables (care invites, beneficiaries beyond the first, heart rate
--     samples, food components, meal photos, alert recipients, Iris
--     conversation titles, availability, legal document texts)
--   · new columns (idempotency keys of activities, meals and insulin, consent
--     IP addresses, device of sensor alerts, report generator, sender role)
--   · values the old CHECK lists do not have: morning_snack and evening_snack
--     meals become 'other', ultra_rapid_bolus becomes 'rapid_bolus',
--     composite dishes become 'generic', and audit rows with an operation
--     outside the old list are deleted
--   · users without an email get a placeholder address in .invalid
-- The steps run in the reverse order of the up migration, in one transaction.
-- ===========================================================================

BEGIN;

-- ---------------------------------------------------------------------------
-- 10. BUSINESS RULE TRIGGERS AND FUNCTIONS
-- ---------------------------------------------------------------------------
DROP TRIGGER trg_teleconsultations_no_overlap ON teleconsultations;
DROP TRIGGER trg_food_components_composite_dish ON food_components;
DROP TRIGGER trg_chat_conversations_same_patient ON chat_conversations;
DROP TRIGGER trg_chat_messages_integrity ON chat_messages;
DROP TRIGGER trg_data_access_audit_log_immutable ON data_access_audit_log;
DROP TRIGGER trg_patient_professional_assignments_no_self_link ON patient_professional_assignments;
DROP TRIGGER trg_caregiver_links_no_self_link ON caregiver_links;
DROP TRIGGER trg_care_invites_no_self_link ON care_invites;
DROP TRIGGER trg_subscription_beneficiaries_limit ON subscription_beneficiaries;
DROP TRIGGER trg_company_invite_codes_seat_limit ON company_invite_codes;

DROP FUNCTION verify_teleconsultation_no_overlap();
DROP FUNCTION verify_composite_dish_type();
DROP FUNCTION verify_chat_same_patient();
DROP FUNCTION verify_chat_message_integrity();
DROP FUNCTION protect_audit_log_immutability();
DROP FUNCTION prevent_self_clinical_linking();
DROP FUNCTION enforce_b2c_beneficiary_limit();
DROP FUNCTION enforce_b2b_seat_limit();


-- ---------------------------------------------------------------------------
-- 9. BLANK-TEXT CHECKS ON THE COLUMNS OF 000001
-- (the new tables and columns take theirs with them)
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
        ['physical_activity_logs', 'activity_name'],
        ['foods', 'barcode_ean'], ['foods', 'food_name'], ['foods', 'brand'],
        ['food_servings', 'serving_description'], ['food_servings', 'metric_unit'],
        ['meals', 'notes'],
        ['insulin_logs', 'insulin_brand'], ['insulin_logs', 'notes'],
        ['glucose_predictions', 'model_version'],
        ['alerts', 'title'], ['alerts', 'message'], ['alerts', 'iris_suggested_action'], ['alerts', 'resolution_notes'],
        ['iris_chat_interactions', 'user_question'], ['iris_chat_interactions', 'iris_answer'],
        ['health_professionals', 'license_number'], ['health_professionals', 'institution'], ['health_professionals', 'bio'],
        ['teleconsultations', 'webrtc_room_token'], ['teleconsultations', 'clinical_notes'],
        ['teleconsultations', 'adjusted_treatment_plan'],
        ['chat_conversations', 'topic'],
        ['clinical_reports', 'work_impact_analysis'], ['clinical_reports', 'iris_summary'], ['clinical_reports', 'pdf_file_url'],
        ['chat_messages', 'body'], ['chat_messages', 'attachment_url'],
        ['data_access_audit_log', 'operation'], ['data_access_audit_log', 'user_agent']
    ] LOOP
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', tc[1], tc[1] || '_' || tc[2] || '_check');
    END LOOP;
END
$$;


-- ---------------------------------------------------------------------------
-- 8. M7 · GDPR, CONSENTS AND AUDIT
-- ---------------------------------------------------------------------------
DELETE FROM data_access_audit_log WHERE operation NOT IN (
    'realtime_glucose_view', 'report_pdf_export', 'therapy_change', 'food_diary_view',
    'caregiver_revoked', 'consent_granted', 'consent_revoked');
ALTER TABLE data_access_audit_log ADD CONSTRAINT data_access_audit_log_operation_check CHECK (operation IN (
    'realtime_glucose_view', 'report_pdf_export', 'therapy_change', 'food_diary_view',
    'caregiver_revoked', 'consent_granted', 'consent_revoked'));

ALTER TABLE consents
    ADD COLUMN consent_type VARCHAR(40),
    ADD COLUMN document_version VARCHAR(20);
UPDATE consents c SET consent_type = d.document_type, document_version = d.version
  FROM legal_documents d WHERE d.id = c.document_id;
ALTER TABLE consents
    ALTER COLUMN consent_type SET NOT NULL,
    ALTER COLUMN document_version SET NOT NULL,
    DROP CONSTRAINT ck_consent_revoked_time,
    DROP COLUMN document_id, -- also drops uq_user_document_consent and its index
    DROP COLUMN ip_address,
    ADD CONSTRAINT consents_consent_type_check
        CHECK (consent_type IN ('terms_of_service', 'health_data_processing', 'external_ai_processing', 'beta_testing')),
    ADD CONSTRAINT uq_consent_version UNIQUE (user_id, consent_type, document_version);

DROP TABLE legal_documents;


-- ---------------------------------------------------------------------------
-- 7. M6 · CLINICAL FOLLOW-UP AND TELEMEDICINE
-- ---------------------------------------------------------------------------
-- 7.7 clinical_reports
ALTER TABLE clinical_reports
    DROP COLUMN assignment_id, -- also drops fk_report_assignment_patient and ck_report_generator
    DROP COLUMN generated_by_role,
    DROP CONSTRAINT ck_report_tir_sum,
    DROP CONSTRAINT clinical_reports_time_in_range_percent_check,
    DROP CONSTRAINT clinical_reports_time_below_range_percent_check,
    DROP CONSTRAINT clinical_reports_time_above_range_percent_check,
    DROP CONSTRAINT clinical_reports_mean_glucose_mg_dl_check,
    DROP CONSTRAINT clinical_reports_std_dev_mg_dl_check,
    DROP CONSTRAINT clinical_reports_coefficient_of_variation_percent_check,
    DROP CONSTRAINT clinical_reports_gmi_percent_check,
    DROP CONSTRAINT clinical_reports_hypo_episodes_check,
    DROP CONSTRAINT clinical_reports_hyper_episodes_check,
    ADD CONSTRAINT ck_report_percentages CHECK (
        time_in_range_percent BETWEEN 0 AND 100
        AND time_below_range_percent BETWEEN 0 AND 100
        AND time_above_range_percent BETWEEN 0 AND 100
        AND time_in_range_percent + time_below_range_percent + time_above_range_percent BETWEEN 99.9 AND 100.1),
    ADD CONSTRAINT ck_report_mean CHECK (mean_glucose_mg_dl > 0);

-- 7.6 chat_conversations: the parties come back from the follow-up and the link
ALTER TABLE chat_conversations
    ADD COLUMN patient_id UUID,
    ADD COLUMN professional_id UUID,
    ADD COLUMN caregiver_user_id UUID;
UPDATE chat_conversations c SET patient_id = a.patient_id, professional_id = a.professional_id
  FROM patient_professional_assignments a WHERE a.id = c.assignment_id;
UPDATE chat_conversations c SET patient_id = cl.patient_id, caregiver_user_id = cl.caregiver_user_id
  FROM caregiver_links cl WHERE cl.id = c.caregiver_link_id;

-- 7.4 chat_messages: the sender's user comes from the role and the conversation
ALTER TABLE chat_messages ADD COLUMN sender_user_id UUID;
UPDATE chat_messages m SET sender_user_id = CASE m.sender_role
           WHEN 'patient' THEN p.user_id
           WHEN 'caregiver' THEN c.caregiver_user_id
           WHEN 'professional' THEN hp.user_id
       END
  FROM chat_conversations c
  JOIN patients p ON p.id = c.patient_id
  LEFT JOIN health_professionals hp ON hp.id = c.professional_id
 WHERE c.id = m.conversation_id;
ALTER TABLE chat_messages
    ALTER COLUMN sender_user_id SET NOT NULL,
    ADD CONSTRAINT chat_messages_sender_user_id_fkey FOREIGN KEY (sender_user_id) REFERENCES users(id) ON DELETE CASCADE,
    DROP COLUMN sender_role,
    DROP CONSTRAINT ck_chat_message_dates;
CREATE INDEX idx_chat_messages_sender_user_id ON chat_messages (sender_user_id);

ALTER TABLE chat_conversations
    ALTER COLUMN patient_id SET NOT NULL,
    DROP COLUMN assignment_id, -- also drops ck_chat_channel_parties, uq_chat_channel and the index
    DROP COLUMN caregiver_link_id,
    ADD CONSTRAINT chat_conversations_patient_id_fkey FOREIGN KEY (patient_id) REFERENCES patients(id) ON DELETE CASCADE,
    ADD CONSTRAINT chat_conversations_professional_id_fkey FOREIGN KEY (professional_id)
        REFERENCES health_professionals(id) ON DELETE RESTRICT,
    ADD CONSTRAINT chat_conversations_caregiver_user_id_fkey FOREIGN KEY (caregiver_user_id)
        REFERENCES users(id) ON DELETE CASCADE,
    ADD CONSTRAINT ck_conversation_parties CHECK (
           (channel_type = 'patient_doctor'    AND professional_id IS NOT NULL AND caregiver_user_id IS NULL)
        OR (channel_type = 'patient_caregiver' AND professional_id IS NULL     AND caregiver_user_id IS NOT NULL)
        OR (channel_type = 'caregiver_doctor'  AND professional_id IS NOT NULL AND caregiver_user_id IS NOT NULL)),
    ADD CONSTRAINT fk_conversation_caregiver_link FOREIGN KEY (patient_id, caregiver_user_id)
        REFERENCES caregiver_links (patient_id, caregiver_user_id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_conversation_assignment FOREIGN KEY (patient_id, professional_id)
        REFERENCES patient_professional_assignments (patient_id, professional_id) ON DELETE RESTRICT,
    ADD CONSTRAINT uq_conversation_parties UNIQUE NULLS NOT DISTINCT (patient_id, channel_type, professional_id, caregiver_user_id);
CREATE INDEX idx_chat_conversations_professional_id ON chat_conversations (professional_id);
CREATE INDEX idx_chat_conversations_caregiver_user_id ON chat_conversations (caregiver_user_id);

-- 7.5 teleconsultations
ALTER TABLE teleconsultations
    ADD COLUMN patient_id UUID,
    ADD COLUMN professional_id UUID;
UPDATE teleconsultations t SET patient_id = a.patient_id, professional_id = a.professional_id
  FROM patient_professional_assignments a WHERE a.id = t.assignment_id;
ALTER TABLE teleconsultations
    ALTER COLUMN patient_id SET NOT NULL,
    ALTER COLUMN professional_id SET NOT NULL,
    DROP COLUMN assignment_id, -- also drops ex_teleconsultation_no_overlap and the index
    ADD CONSTRAINT teleconsultations_patient_id_fkey FOREIGN KEY (patient_id) REFERENCES patients(id) ON DELETE CASCADE,
    ADD CONSTRAINT teleconsultations_professional_id_fkey FOREIGN KEY (professional_id)
        REFERENCES health_professionals(id) ON DELETE RESTRICT,
    ADD CONSTRAINT fk_teleconsultation_assignment FOREIGN KEY (patient_id, professional_id)
        REFERENCES patient_professional_assignments (patient_id, professional_id) ON DELETE RESTRICT,
    DROP CONSTRAINT teleconsultations_duration_minutes_check,
    ADD CONSTRAINT teleconsultations_duration_minutes_check CHECK (duration_minutes > 0);
CREATE INDEX idx_teleconsultations_patient_id ON teleconsultations (patient_id);
CREATE INDEX idx_teleconsultations_professional_scheduled ON teleconsultations (professional_id, scheduled_at);

-- 7.3 patient_professional_assignments
ALTER TABLE patient_professional_assignments
    DROP CONSTRAINT uq_assignment_id_patient,
    DROP CONSTRAINT ck_assignment_dates,
    ADD CONSTRAINT ck_assignment_dates CHECK (end_date IS NULL OR end_date >= start_date),
    DROP CONSTRAINT patient_professional_assignments_professional_id_fkey,
    ADD CONSTRAINT patient_professional_assignments_professional_id_fkey FOREIGN KEY (professional_id)
        REFERENCES health_professionals(id) ON DELETE CASCADE;

-- 7.2 professional_availability
DROP TABLE professional_availability;

-- 7.1 health_professionals
ALTER TABLE health_professionals ADD COLUMN specialty VARCHAR(30);
UPDATE health_professionals hp SET specialty = ms.code FROM medical_specialties ms WHERE ms.id = hp.specialty_id;
ALTER TABLE health_professionals
    ALTER COLUMN specialty SET NOT NULL,
    ADD CONSTRAINT health_professionals_specialty_check
        CHECK (specialty IN ('endocrinology', 'general_practice', 'diabetes_nursing', 'clinical_nutrition')),
    DROP COLUMN specialty_id;
ALTER INDEX uq_hp_user_id RENAME TO uq_health_professionals_user_id;
ALTER INDEX uq_hp_license RENAME TO uq_health_professionals_license;


-- ---------------------------------------------------------------------------
-- 6. M5 · PREDICTION, ALERTS AND IRIS
-- ---------------------------------------------------------------------------
-- 6.4 iris
ALTER TABLE iris_chat_interactions ADD COLUMN patient_id UUID;
UPDATE iris_chat_interactions i SET patient_id = c.patient_id FROM iris_conversations c WHERE c.id = i.conversation_id;
ALTER TABLE iris_chat_interactions
    ALTER COLUMN patient_id SET NOT NULL,
    ADD CONSTRAINT iris_chat_interactions_patient_id_fkey FOREIGN KEY (patient_id) REFERENCES patients(id) ON DELETE CASCADE,
    DROP COLUMN conversation_id,
    DROP COLUMN llm_model_version,
    DROP COLUMN safety_guardrail_triggered;
CREATE INDEX idx_iris_chat_interactions_patient_id ON iris_chat_interactions (patient_id);
DROP TABLE iris_conversations;

-- 6.2/6.3 alerts: the patient comes back from the source, and the person who
-- acknowledged it from the first recipient that did
ALTER TABLE alerts
    ADD COLUMN patient_id UUID,
    ADD COLUMN acknowledged_by_user_id UUID;
UPDATE alerts a SET patient_id = COALESCE(
           (SELECT g.patient_id FROM glucose_readings g WHERE g.id = a.source_reading_id),
           (SELECT gp.patient_id FROM glucose_predictions gp WHERE gp.id = a.source_prediction_id),
           (SELECT d.patient_id FROM devices d WHERE d.id = a.device_id));
UPDATE alerts a SET acknowledged_by_user_id = (
           SELECT r.user_id FROM alert_recipients r
            WHERE r.alert_id = a.id AND r.acknowledged_at IS NOT NULL
            ORDER BY r.acknowledged_at LIMIT 1);
DROP TABLE alert_recipients;
ALTER TABLE alerts
    ALTER COLUMN patient_id SET NOT NULL,
    ADD CONSTRAINT alerts_patient_id_fkey FOREIGN KEY (patient_id) REFERENCES patients(id) ON DELETE CASCADE,
    ADD CONSTRAINT alerts_acknowledged_by_user_id_fkey FOREIGN KEY (acknowledged_by_user_id)
        REFERENCES users(id) ON DELETE SET NULL,
    DROP CONSTRAINT ck_alert_ack_time,
    DROP CONSTRAINT ck_alert_single_source,
    DROP CONSTRAINT ck_alert_type_source,
    DROP COLUMN device_id,
    ADD CONSTRAINT ck_alert_acknowledged_at CHECK (ack_status = 'pending' OR acknowledged_at IS NOT NULL),
    DROP CONSTRAINT alerts_source_reading_id_fkey,
    ADD CONSTRAINT alerts_source_reading_id_fkey FOREIGN KEY (source_reading_id)
        REFERENCES glucose_readings(id) ON DELETE SET NULL,
    DROP CONSTRAINT alerts_source_prediction_id_fkey,
    ADD CONSTRAINT alerts_source_prediction_id_fkey FOREIGN KEY (source_prediction_id)
        REFERENCES glucose_predictions(id) ON DELETE SET NULL;
CREATE INDEX idx_alerts_patient_triggered ON alerts (patient_id, triggered_at DESC);
CREATE INDEX idx_alerts_acknowledged_by_user_id ON alerts (acknowledged_by_user_id);

-- 6.1 glucose_predictions
ALTER TABLE glucose_predictions
    DROP CONSTRAINT glucose_predictions_predicted_mg_dl_check,
    DROP CONSTRAINT glucose_predictions_ci_low_mg_dl_check,
    DROP CONSTRAINT glucose_predictions_ci_high_mg_dl_check,
    DROP CONSTRAINT glucose_predictions_horizon_minutes_check,
    ADD CONSTRAINT glucose_predictions_horizon_minutes_check CHECK (horizon_minutes > 0);


-- ---------------------------------------------------------------------------
-- 5. M4 · NUTRITION AND INSULIN
-- ---------------------------------------------------------------------------
-- 5.8 insulin_logs
ALTER TABLE insulin_logs ADD COLUMN insulin_type VARCHAR(20);
UPDATE insulin_logs il SET insulin_type = CASE it.code WHEN 'ultra_rapid_bolus' THEN 'rapid_bolus' ELSE it.code END
  FROM insulin_types it WHERE it.id = il.insulin_type_id;
ALTER TABLE insulin_logs
    ALTER COLUMN insulin_type SET NOT NULL,
    ADD CONSTRAINT insulin_logs_insulin_type_check CHECK (insulin_type IN ('rapid_bolus', 'basal', 'premixed')),
    DROP COLUMN insulin_type_id,
    DROP COLUMN idempotency_key,
    DROP CONSTRAINT fk_insulin_meal_patient,
    ADD CONSTRAINT insulin_logs_meal_id_fkey FOREIGN KEY (meal_id) REFERENCES meals(id) ON DELETE SET NULL,
    DROP CONSTRAINT insulin_logs_administered_at_check,
    DROP CONSTRAINT insulin_logs_dose_units_check,
    ADD CONSTRAINT insulin_logs_dose_units_check CHECK (dose_units > 0);

-- 5.7 meal_photos
DROP TABLE meal_photos;

-- 5.6 meal_items
ALTER TABLE meal_items
    DROP CONSTRAINT meal_items_carbs_g_check,
    DROP CONSTRAINT meal_items_calories_kcal_check,
    DROP CONSTRAINT meal_items_serving_quantity_check,
    ADD CONSTRAINT meal_items_serving_quantity_check CHECK (serving_quantity > 0);

-- 5.5 meals
ALTER TABLE meals ADD COLUMN meal_type VARCHAR(20);
UPDATE meals m SET meal_type = CASE WHEN mt.code IN ('morning_snack', 'evening_snack') THEN 'other' ELSE mt.code END
  FROM meal_types mt WHERE mt.id = m.meal_type_id;
ALTER TABLE meals
    ALTER COLUMN meal_type SET NOT NULL,
    ADD CONSTRAINT meals_meal_type_check CHECK (meal_type IN ('breakfast', 'lunch', 'afternoon_snack', 'dinner', 'other')),
    DROP COLUMN meal_type_id,
    DROP COLUMN idempotency_key,
    DROP CONSTRAINT uq_meals_id_patient,
    DROP CONSTRAINT ck_meal_eaten_time,
    ADD CONSTRAINT ck_meal_eaten_at CHECK (status <> 'eaten' OR eaten_at IS NOT NULL),
    DROP CONSTRAINT meals_total_carbs_g_check,
    DROP CONSTRAINT meals_total_calories_kcal_check,
    DROP CONSTRAINT meals_postprandial_delta_mg_dl_check;

-- 5.4 food_servings
ALTER INDEX uq_food_serving_desc RENAME TO uq_food_servings_description;
ALTER TABLE food_servings
    DROP CONSTRAINT ck_serving_macros_hierarchy,
    DROP CONSTRAINT food_servings_fatsecret_serving_id_check,
    DROP CONSTRAINT food_servings_metric_amount_check,
    DROP CONSTRAINT food_servings_fiber_g_check,
    DROP CONSTRAINT food_servings_sugars_g_check,
    DROP CONSTRAINT food_servings_protein_g_check,
    DROP CONSTRAINT food_servings_fat_g_check,
    DROP CONSTRAINT food_servings_saturated_fat_g_check,
    DROP CONSTRAINT food_servings_sodium_mg_check,
    DROP CONSTRAINT food_servings_carbs_g_check,
    ADD CONSTRAINT food_servings_carbs_g_check CHECK (carbs_g >= 0),
    DROP CONSTRAINT food_servings_calories_kcal_check,
    ADD CONSTRAINT food_servings_calories_kcal_check CHECK (calories_kcal >= 0);

-- 5.3 food_components
DROP TABLE food_components;

-- 5.2/5.1 foods and food_categories
ALTER TABLE foods
    ADD COLUMN category VARCHAR(80),
    ADD COLUMN created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
UPDATE foods f SET category = fc.name FROM food_categories fc WHERE fc.id = f.category_id;
UPDATE foods SET food_type = 'generic' WHERE food_type = 'composite_dish';
ALTER TABLE foods
    DROP COLUMN category_id,
    DROP CONSTRAINT foods_fatsecret_food_id_check,
    DROP CONSTRAINT foods_food_type_check,
    ADD CONSTRAINT foods_food_type_check CHECK (food_type IN ('generic', 'brand'));
CREATE INDEX idx_foods_created_by_user_id ON foods (created_by_user_id);
DROP TABLE food_categories;


-- ---------------------------------------------------------------------------
-- 4. M3 · TELEMETRY AND DEVICES
-- ---------------------------------------------------------------------------
-- 4.4 physical_activity_logs
ALTER TABLE physical_activity_logs
    DROP COLUMN idempotency_key,
    DROP CONSTRAINT fk_activity_device_patient,
    ADD CONSTRAINT physical_activity_logs_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE SET NULL,
    DROP CONSTRAINT physical_activity_logs_started_at_check,
    DROP CONSTRAINT physical_activity_logs_duration_minutes_check,
    ADD CONSTRAINT physical_activity_logs_duration_minutes_check CHECK (duration_minutes > 0),
    DROP CONSTRAINT physical_activity_logs_total_steps_check,
    ADD CONSTRAINT physical_activity_logs_total_steps_check CHECK (total_steps >= 0),
    DROP CONSTRAINT physical_activity_logs_avg_heart_rate_bpm_check,
    ADD CONSTRAINT physical_activity_logs_avg_heart_rate_bpm_check CHECK (avg_heart_rate_bpm > 0),
    DROP CONSTRAINT physical_activity_logs_calories_burned_kcal_check,
    ADD CONSTRAINT physical_activity_logs_calories_burned_kcal_check CHECK (calories_burned_kcal >= 0);

-- 4.3 glucose_readings
ALTER TABLE glucose_readings
    ALTER COLUMN signal_quality SET DEFAULT 100,
    DROP CONSTRAINT fk_glucose_device_patient,
    ADD CONSTRAINT glucose_readings_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE SET NULL,
    DROP CONSTRAINT ck_glucose_source_device,
    DROP CONSTRAINT glucose_readings_measured_at_check;

-- 4.2 heart_rate_samples
DROP TABLE heart_rate_samples;

-- 4.1 devices
ALTER TABLE devices ADD COLUMN device_type VARCHAR(20);
UPDATE devices d SET device_type = dt.code FROM device_types dt WHERE dt.id = d.device_type_id;
ALTER TABLE devices
    ALTER COLUMN device_type SET NOT NULL,
    ADD CONSTRAINT devices_device_type_check CHECK (device_type IN ('cgm_sensor', 'smartwatch', 'smartband', 'glucometer')),
    DROP COLUMN device_type_id,
    DROP CONSTRAINT uq_devices_id_patient,
    DROP CONSTRAINT ck_device_dates;


-- ---------------------------------------------------------------------------
-- 3. M2 · PATIENT, CARE NETWORK AND SUBSCRIPTIONS
-- ---------------------------------------------------------------------------
-- 3.5 b2c_subscriptions: one beneficiary per subscription. An active
-- subscription keeps a patient it covers now; an ended one, the last it covered.
ALTER TABLE b2c_subscriptions
    ADD COLUMN user_id UUID,
    ADD COLUMN beneficiary_patient_id UUID,
    ADD COLUMN plan VARCHAR(30);
UPDATE b2c_subscriptions s SET user_id = s.payer_user_id, plan = sp.code
  FROM subscription_plans sp WHERE sp.id = s.plan_id;
UPDATE b2c_subscriptions s SET beneficiary_patient_id = (
           SELECT b.patient_id FROM subscription_beneficiaries b
            WHERE b.subscription_id = s.id AND (b.removed_at IS NULL OR s.status <> 'active')
            ORDER BY b.removed_at DESC NULLS FIRST, b.added_at LIMIT 1);
DROP TABLE subscription_beneficiaries;
ALTER TABLE b2c_subscriptions RENAME CONSTRAINT ck_sub_dates TO ck_subscription_dates;
ALTER TABLE b2c_subscriptions
    ALTER COLUMN user_id SET NOT NULL,
    ALTER COLUMN plan SET NOT NULL,
    ADD CONSTRAINT b2c_subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    ADD CONSTRAINT b2c_subscriptions_beneficiary_patient_id_fkey FOREIGN KEY (beneficiary_patient_id)
        REFERENCES patients(id) ON DELETE CASCADE,
    ADD CONSTRAINT b2c_subscriptions_plan_check CHECK (plan IN ('free', 'premium_individual', 'premium_family')),
    DROP COLUMN payer_user_id,
    DROP COLUMN plan_id,
    DROP CONSTRAINT b2c_subscriptions_monthly_price_check,
    ADD CONSTRAINT b2c_subscriptions_monthly_price_check CHECK (monthly_price >= 0);
CREATE INDEX idx_b2c_subscriptions_user_id ON b2c_subscriptions (user_id);
CREATE INDEX idx_b2c_subscriptions_beneficiary_patient_id ON b2c_subscriptions (beneficiary_patient_id);
CREATE UNIQUE INDEX uq_b2c_subscriptions_active_beneficiary ON b2c_subscriptions (beneficiary_patient_id) WHERE status = 'active';

-- 3.4 caregiver_links
ALTER TABLE caregiver_links
    DROP CONSTRAINT uq_caregiver_link_id_patient,
    DROP CONSTRAINT ck_caregiver_dates,
    ADD CONSTRAINT ck_caregiver_link_dates CHECK (
        (status <> 'active' OR linked_at IS NOT NULL) AND (status <> 'revoked' OR revoked_at IS NOT NULL));

-- 3.3 care_invites
DROP TABLE care_invites;

-- 3.2 emergency_contacts
ALTER TABLE emergency_contacts
    DROP CONSTRAINT uq_emergency_contact_priority,
    DROP CONSTRAINT emergency_contacts_call_priority_check,
    ADD CONSTRAINT emergency_contacts_call_priority_check CHECK (call_priority > 0);

-- 3.1 patients: the employer comes back from the company code the patient redeemed
ALTER TABLE patients
    ADD COLUMN gender VARCHAR(20),
    ADD COLUMN diabetes_type VARCHAR(20),
    ADD COLUMN current_therapy VARCHAR(30),
    ADD COLUMN organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
UPDATE patients p SET gender = g.code FROM genders g WHERE g.id = p.gender_id;
UPDATE patients p SET diabetes_type = d.code FROM diabetes_types d WHERE d.id = p.diabetes_type_id;
UPDATE patients p SET current_therapy = t.code FROM therapy_types t WHERE t.id = p.therapy_type_id;
UPDATE patients p SET organization_id = c.organization_id
  FROM company_invite_codes c WHERE c.used_by_patient_id = p.id;
ALTER TABLE patients
    ALTER COLUMN diabetes_type SET NOT NULL,
    ADD CONSTRAINT patients_gender_check CHECK (gender IN ('male', 'female', 'other', 'undisclosed')),
    ADD CONSTRAINT patients_diabetes_type_check CHECK (diabetes_type IN ('type_1', 'type_2', 'gestational', 'lada', 'mody')),
    ADD CONSTRAINT patients_current_therapy_check
        CHECK (current_therapy IN ('multiple_daily_injections', 'insulin_pump', 'oral_medication', 'diet_only')),
    DROP COLUMN gender_id,
    DROP COLUMN diabetes_type_id,
    DROP COLUMN therapy_type_id,
    DROP CONSTRAINT patients_birth_date_check,
    DROP CONSTRAINT patients_weight_kg_check,
    ADD CONSTRAINT patients_weight_kg_check CHECK (weight_kg > 0),
    DROP CONSTRAINT patients_height_cm_check,
    ADD CONSTRAINT patients_height_cm_check CHECK (height_cm > 0);
ALTER TABLE patients RENAME CONSTRAINT ck_patient_thresholds TO ck_patient_glucose_thresholds;
CREATE INDEX idx_patients_organization_id ON patients (organization_id);


-- ---------------------------------------------------------------------------
-- 2. M1 · IDENTITY, ACCESS AND COMPANIES
-- ---------------------------------------------------------------------------
-- 2.6 user_profiles, rebuilt from the data that replaced it
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
CREATE INDEX idx_user_profiles_organization_id ON user_profiles (organization_id);
CREATE TRIGGER trg_user_profiles_updated_at BEFORE UPDATE ON user_profiles FOR EACH ROW EXECUTE FUNCTION set_updated_at();

INSERT INTO user_profiles (user_id, profile_type, created_at)
SELECT user_id, 'patient', created_at FROM patients WHERE deleted_at IS NULL;
INSERT INTO user_profiles (user_id, profile_type, created_at)
SELECT caregiver_user_id, 'caregiver', MIN(created_at) FROM caregiver_links WHERE status = 'active' GROUP BY caregiver_user_id;
INSERT INTO user_profiles (user_id, profile_type, created_at)
SELECT user_id, 'health_professional', created_at FROM health_professionals WHERE deleted_at IS NULL;
INSERT INTO user_profiles (user_id, profile_type, organization_id, created_at) -- one company per manager in 000001
SELECT DISTINCT ON (user_id) user_id, 'company_manager', organization_id, created_at
  FROM organization_managers ORDER BY user_id, created_at;
INSERT INTO user_profiles (user_id, profile_type)
SELECT id, 'admin' FROM users WHERE is_platform_admin;

-- 2.5 company_invite_codes
ALTER TABLE company_invite_codes
    ADD COLUMN created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN used_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
UPDATE company_invite_codes c SET created_by_user_id = om.user_id
  FROM organization_managers om WHERE om.id = c.created_by_manager_id;
UPDATE company_invite_codes c SET used_by_user_id = p.user_id
  FROM patients p WHERE p.id = c.used_by_patient_id;
ALTER TABLE company_invite_codes
    DROP COLUMN created_by_manager_id, -- also drops fk_invite_manager_org and its index
    DROP COLUMN used_by_patient_id, -- also drops uq_company_invite_patient and ck_company_invite_state
    DROP CONSTRAINT ck_company_invite_dates,
    ADD CONSTRAINT ck_invite_used_at CHECK (status <> 'used' OR used_at IS NOT NULL);
CREATE INDEX idx_company_invite_codes_created_by_user_id ON company_invite_codes (created_by_user_id);
CREATE INDEX idx_company_invite_codes_used_by_user_id ON company_invite_codes (used_by_user_id);

-- 2.4 organization_managers
DROP TABLE organization_managers;

-- 2.3 organizations
ALTER TABLE organizations RENAME CONSTRAINT ck_org_contract_dates TO ck_organization_contract_dates;
ALTER TABLE organizations
    DROP CONSTRAINT organizations_seat_count_check,
    ADD CONSTRAINT organizations_seat_count_check CHECK (seat_count > 0);

-- 2.2 external_identities
ALTER TABLE external_identities ADD COLUMN provider VARCHAR(20);
UPDATE external_identities e SET provider = ip.code FROM identity_providers ip WHERE ip.id = e.provider_id;
ALTER TABLE external_identities
    ALTER COLUMN provider SET NOT NULL,
    ADD CONSTRAINT external_identities_provider_check CHECK (provider IN ('google', 'microsoft', 'apple', 'facebook')),
    DROP COLUMN provider_id, -- also drops uq_ext_id_provider_subject and uq_ext_id_user_provider
    ADD CONSTRAINT uq_provider_subject UNIQUE (provider, provider_subject),
    ADD CONSTRAINT uq_user_provider UNIQUE (user_id, provider);

-- 2.1 users
ALTER TABLE users ADD COLUMN last_login_at TIMESTAMPTZ;
UPDATE users u SET last_login_at = e.last_login_at
  FROM (SELECT user_id, MAX(last_login_at) AS last_login_at FROM external_identities GROUP BY user_id) e
 WHERE e.user_id = u.id;
UPDATE users SET email = 'no-email+' || id || '@voltik.invalid' WHERE email IS NULL;
ALTER TABLE users
    DROP CONSTRAINT ck_users_deleted_status,
    ALTER COLUMN email SET NOT NULL,
    DROP COLUMN is_platform_admin;


-- ---------------------------------------------------------------------------
-- 1. ENUMERATION TABLES
-- ---------------------------------------------------------------------------
DROP TABLE medical_specialties;
DROP TABLE insulin_types;
DROP TABLE meal_types;
DROP TABLE device_types;
DROP TABLE subscription_plans;
DROP TABLE therapy_types;
DROP TABLE diabetes_types;
DROP TABLE genders;
DROP TABLE identity_providers;


-- ---------------------------------------------------------------------------
-- 0. EXTENSION AND HELPER
-- ---------------------------------------------------------------------------
DROP FUNCTION consultation_period(TIMESTAMPTZ, INTEGER);
DROP EXTENSION IF EXISTS btree_gist;

COMMIT;
