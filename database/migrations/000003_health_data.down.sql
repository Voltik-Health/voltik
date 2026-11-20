-- Reverts 000003. Run 000004 down first (it removes the INSA foods).
-- Open Food Facts products are deleted with their servings; this fails while a
-- meal still uses one of them (meal_items is ON DELETE RESTRICT).
BEGIN;

DROP INDEX IF EXISTS idx_heart_rate_samples_measured;
DROP INDEX IF EXISTS idx_caregiver_links_patient_active;
DROP INDEX IF EXISTS idx_glucose_predictions_patient_created;
DROP INDEX IF EXISTS idx_physical_activity_logs_patient_started;
DROP INDEX IF EXISTS idx_insulin_logs_patient_administered;

DROP INDEX IF EXISTS uq_clinical_reports_weekly;
ALTER TABLE clinical_reports
    DROP COLUMN pdf_file_id,
    DROP COLUMN coverage_percent,
    DROP COLUMN target_max_mg_dl,
    DROP COLUMN target_min_mg_dl;

ALTER TABLE patients DROP COLUMN weekly_reports_enabled;

DROP TABLE sms_messages;

DROP INDEX IF EXISTS idx_alert_recipients_outbox;
ALTER TABLE alert_recipients
    DROP COLUMN last_error,
    DROP COLUMN next_attempt_at,
    DROP COLUMN attempts,
    DROP COLUMN provider_message_id;

DROP INDEX IF EXISTS idx_alerts_triggered_at;
DROP INDEX IF EXISTS idx_alerts_escalation_due;
ALTER TABLE alerts
    DROP CONSTRAINT ck_alert_escalation,
    DROP COLUMN next_escalation_at,
    DROP COLUMN escalation_step;

DROP INDEX IF EXISTS idx_meals_postprandial_due;
ALTER TABLE meals DROP COLUMN postprandial_computed_at;

DROP INDEX IF EXISTS idx_meal_photos_pending;
DROP INDEX IF EXISTS idx_meal_photos_file_id;
ALTER TABLE meal_photos
    DROP CONSTRAINT ck_meal_photo_confirmed,
    DROP COLUMN confirmed_at,
    DROP COLUMN analysis_attempts,
    DROP COLUMN file_id;

DROP TABLE stored_files;

ALTER TABLE food_servings
    DROP CONSTRAINT ck_serving_macros_hierarchy,
    ADD CONSTRAINT ck_serving_macros_hierarchy CHECK (
        fiber_g <= carbs_g AND sugars_g <= carbs_g AND saturated_fat_g <= fat_g);

DELETE FROM foods WHERE data_source = 'open_food_facts';
DROP INDEX IF EXISTS idx_foods_search;
DROP INDEX IF EXISTS idx_foods_created_by_user_id;
ALTER TABLE foods
    ALTER COLUMN data_source SET DEFAULT 'fatsecret',
    DROP CONSTRAINT foods_data_source_check,
    ADD CONSTRAINT foods_data_source_check CHECK (data_source IN ('fatsecret', 'portuguese_traditional', 'user_custom')),
    DROP COLUMN created_by_user_id;

DROP INDEX IF EXISTS uq_devices_nightscout_secret;
ALTER TABLE devices DROP COLUMN nightscout_secret_hash;
DROP INDEX IF EXISTS uq_devices_patient_serial;

DROP FUNCTION voltik_search_key(text);
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS unaccent;

COMMIT;
