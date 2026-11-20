package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

// querier is a pool or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// alerts have no patient column: the patient comes from the source (000002).
const alertSelect = `
	SELECT a.id, COALESCE(gr.patient_id, gp.patient_id, dv.patient_id), a.alert_type, a.severity, a.title, a.message,
	       a.iris_suggested_action, a.triggered_at, a.ack_status, a.acknowledged_at, a.resolution_notes,
	       a.source_reading_id, a.source_prediction_id, a.device_id, COALESCE(gr.value_mg_dl, gp.predicted_mg_dl), a.created_at
	  FROM alerts a
	  LEFT JOIN glucose_readings gr ON gr.id = a.source_reading_id
	  LEFT JOIN glucose_predictions gp ON gp.id = a.source_prediction_id
	  LEFT JOIN devices dv ON dv.id = a.device_id`

const alertPatient = `COALESCE(gr.patient_id, gp.patient_id, dv.patient_id)`

func scanAlert(row pgx.Row) (domain.Alert, error) {
	var a domain.Alert
	err := row.Scan(&a.ID, &a.PatientID, &a.Type, &a.Severity, &a.Title, &a.Message, &a.IrisSuggestedAction,
		&a.TriggeredAt, &a.AckStatus, &a.AcknowledgedAt, &a.ResolutionNotes,
		&a.SourceReadingID, &a.SourcePredictionID, &a.DeviceID, &a.SourceValueMgDl, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	return a, err
}

// withRecipients loads the recipients of the alerts and who acknowledged each first.
func withRecipients(ctx context.Context, q querier, alerts []domain.Alert) error {
	if len(alerts) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(alerts))
	index := map[uuid.UUID]int{}
	for i, a := range alerts {
		ids[i], index[a.ID] = a.ID, i
		alerts[i].Recipients = []domain.AlertRecipient{}
	}
	rows, err := q.Query(ctx, `
		SELECT r.id, r.alert_id, r.user_id, u.full_name, r.channel, r.delivery_status,
		       r.sent_at, r.delivered_at, r.read_at, r.acknowledged_at
		  FROM alert_recipients r JOIN users u ON u.id = r.user_id
		 WHERE r.alert_id = ANY($1)
		 ORDER BY r.created_at, r.user_id, r.channel`, ids)
	if err != nil {
		return err
	}
	recipients, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AlertRecipient, error) {
		var r domain.AlertRecipient
		err := row.Scan(&r.ID, &r.AlertID, &r.UserID, &r.FullName, &r.Channel, &r.DeliveryStatus,
			&r.SentAt, &r.DeliveredAt, &r.ReadAt, &r.AcknowledgedAt)
		return r, err
	})
	if err != nil {
		return err
	}
	first := map[uuid.UUID]domain.AlertRecipient{}
	for _, r := range recipients {
		i := index[r.AlertID]
		alerts[i].Recipients = append(alerts[i].Recipients, r)
		if r.AcknowledgedAt != nil {
			if f, ok := first[r.AlertID]; !ok || r.AcknowledgedAt.Before(*f.AcknowledgedAt) {
				first[r.AlertID] = r
			}
		}
	}
	for id, r := range first {
		alerts[index[id]].AcknowledgedBy = &domain.Actor{UserID: r.UserID, FullName: r.FullName}
	}
	return nil
}

func alertByID(ctx context.Context, q querier, id uuid.UUID) (domain.Alert, error) {
	a, err := scanAlert(q.QueryRow(ctx, alertSelect+` WHERE a.id = $1`, id))
	if err != nil {
		return a, err
	}
	list := []domain.Alert{a}
	err = withRecipients(ctx, q, list)
	return list[0], err
}

// GetAlert reads an alert with its recipients.
func (s *Store) GetAlert(ctx context.Context, id uuid.UUID) (domain.Alert, error) {
	return alertByID(ctx, s.pool, id)
}

// RaiseAlert creates an alert and its recipient rows, in one transaction
// (UC114, UC116, UC092). If the patient already has a pending alert of the
// same type (in the last 30 minutes, or for the same sensor), it only updates
// its message and created is false. Glucose alerts go to the patient and to
// the caregivers with an active link; sensor alerts only to the patient.
func (s *Store) RaiseAlert(ctx context.Context, n domain.NewAlert, now time.Time) (domain.Alert, bool, error) {
	var id uuid.UUID
	created := false
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockPatient(ctx, tx, n.PatientID); err != nil {
			return err
		}
		var err error
		if n.DeviceID != nil {
			err = tx.QueryRow(ctx, `
				SELECT id FROM alerts WHERE device_id = $1 AND alert_type = $2 AND ack_status = 'pending'
				 ORDER BY triggered_at DESC LIMIT 1`, *n.DeviceID, n.Type).Scan(&id)
		} else {
			err = tx.QueryRow(ctx, `
				SELECT a.id FROM alerts a JOIN glucose_readings gr ON gr.id = a.source_reading_id
				 WHERE gr.patient_id = $1 AND a.alert_type = $2 AND a.ack_status = 'pending' AND a.triggered_at >= $3
				 ORDER BY a.triggered_at DESC LIMIT 1`, n.PatientID, n.Type, now.Add(-domain.AlertRepeatWindow)).Scan(&id)
		}
		switch {
		case err == nil:
			_, err = tx.Exec(ctx, `UPDATE alerts SET message = $2 WHERE id = $1`, id, n.Message)
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		var escalateAt *time.Time
		if n.Escalate {
			t := now.Add(domain.EscalationDelay)
			escalateAt = &t
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO alerts (source_reading_id, device_id, alert_type, severity, title, message,
			                    iris_suggested_action, triggered_at, next_escalation_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			n.SourceReadingID, n.DeviceID, n.Type, n.Severity, n.Title, n.Message,
			n.SuggestedAction, n.TriggeredAt, escalateAt).Scan(&id); err != nil {
			return err
		}
		created = true
		_, err = tx.Exec(ctx, `
			WITH people AS (
			    SELECT p.user_id FROM patients p WHERE p.id = $2
			    UNION
			    SELECT cl.caregiver_user_id FROM caregiver_links cl
			      JOIN users u ON u.id = cl.caregiver_user_id AND u.deleted_at IS NULL AND u.account_status = 'active'
			     WHERE cl.patient_id = $2 AND cl.status = 'active' AND $3::boolean)
			INSERT INTO alert_recipients (alert_id, user_id, channel, delivery_status, sent_at, next_attempt_at)
			SELECT $1, people.user_id, c.channel, c.status, c.sent_at, c.next_at
			  FROM people
			 CROSS JOIN (VALUES ('push', 'pending', NULL::timestamptz, now()),
			                    ('in_app', 'sent', now(), NULL::timestamptz)) AS c (channel, status, sent_at, next_at)`,
			id, n.PatientID, n.DeviceID == nil)
		return err
	})
	if err != nil {
		return domain.Alert{}, false, err
	}
	a, err := alertByID(ctx, s.pool, id)
	return a, created, err
}

// ListAlerts returns one page of the patient's alerts, newest first (UC120).
func (s *Store) ListAlerts(ctx context.Context, patientID uuid.UUID, f domain.AlertFilter) ([]domain.Alert, string, error) {
	var afterAt *time.Time
	var afterID *uuid.UUID
	if f.Cursor != "" {
		t, raw, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, "", domain.Invalid("cursor", "is not a cursor returned by the API")
		}
		afterAt, afterID = &t, &id
	}
	rows, err := s.pool.Query(ctx, alertSelect+`
		 WHERE `+alertPatient+` = $1
		   AND ($2::text IS NULL OR a.ack_status = $2)
		   AND ($3::text IS NULL OR a.alert_type = $3)
		   AND ($4::text IS NULL OR a.severity = $4)
		   AND ($5::timestamptz IS NULL OR a.triggered_at >= $5)
		   AND ($6::timestamptz IS NULL OR a.triggered_at < $6)
		   AND ($7::timestamptz IS NULL OR (a.triggered_at, a.id) < ($7, $8))
		 ORDER BY a.triggered_at DESC, a.id DESC LIMIT $9`,
		patientID, f.AckStatus, f.Type, f.Severity, f.From, f.To, afterAt, afterID, f.Limit+1)
	if err != nil {
		return nil, "", err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Alert, error) { return scanAlert(row) })
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > f.Limit {
		items = items[:f.Limit]
		last := items[len(items)-1]
		next = encodeCursor(last.TriggeredAt, last.ID.String())
	}
	return items, next, withRecipients(ctx, s.pool, items)
}

// lockAlertForRecipient locks the alert and checks that the user is one of its recipients.
func lockAlertForRecipient(ctx context.Context, tx pgx.Tx, alertID, userID uuid.UUID) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT ack_status FROM alerts WHERE id = $1 FOR UPDATE`, alertID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var recipient bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alert_recipients WHERE alert_id = $1 AND user_id = $2)`,
		alertID, userID).Scan(&recipient); err != nil {
		return "", err
	}
	if !recipient {
		return "", domain.ErrNotFound
	}
	return status, nil
}

// markHandled records on the user's delivered rows that they acknowledged the alert.
func markHandled(ctx context.Context, tx pgx.Tx, alertID, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE alert_recipients
		   SET delivery_status = 'acknowledged', acknowledged_at = now(),
		       read_at = COALESCE(read_at, now()), delivered_at = COALESCE(delivered_at, now())
		 WHERE alert_id = $1 AND user_id = $2 AND sent_at IS NOT NULL AND delivery_status <> 'acknowledged'`,
		alertID, userID)
	return err
}

// AcknowledgeAlert records the user's acknowledgement (UC118). The first one
// moves a pending alert to acknowledged and stops the escalation; changed
// says whether that happened now.
func (s *Store) AcknowledgeAlert(ctx context.Context, alertID, userID uuid.UUID) (domain.Alert, bool, error) {
	changed := false
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		status, err := lockAlertForRecipient(ctx, tx, alertID, userID)
		if err != nil {
			return err
		}
		if err := markHandled(ctx, tx, alertID, userID); err != nil {
			return err
		}
		if status != domain.AlertPending {
			return nil
		}
		changed = true
		_, err = tx.Exec(ctx, `
			UPDATE alerts SET ack_status = 'acknowledged', acknowledged_at = GREATEST(now(), triggered_at),
			       next_escalation_at = NULL
			 WHERE id = $1`, alertID)
		return err
	})
	if err != nil {
		return domain.Alert{}, false, err
	}
	a, err := alertByID(ctx, s.pool, alertID)
	return a, changed, err
}

// ResolveAlert closes the alert as resolved or dismissed (UC119). A closed
// alert is returned unchanged.
func (s *Store) ResolveAlert(ctx context.Context, alertID, userID uuid.UUID, status string, notes *string) (domain.Alert, bool, error) {
	changed := false
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		current, err := lockAlertForRecipient(ctx, tx, alertID, userID)
		if err != nil {
			return err
		}
		if current == domain.AlertResolved || current == domain.AlertDismissed {
			return nil
		}
		if err := markHandled(ctx, tx, alertID, userID); err != nil {
			return err
		}
		changed = true
		_, err = tx.Exec(ctx, `
			UPDATE alerts SET ack_status = $2, resolution_notes = $3, next_escalation_at = NULL,
			       acknowledged_at = COALESCE(acknowledged_at, GREATEST(now(), triggered_at))
			 WHERE id = $1`, alertID, status, notes)
		return err
	})
	if err != nil {
		return domain.Alert{}, false, err
	}
	a, err := alertByID(ctx, s.pool, alertID)
	return a, changed, err
}

// MarkNotification moves one of the user's notifications to delivered or
// read (UC121). It never goes back; a notification of someone else is ErrNotFound.
func (s *Store) MarkNotification(ctx context.Context, recipientID, userID uuid.UUID, status string) error {
	sql := `UPDATE alert_recipients SET delivery_status = 'delivered', delivered_at = now()
		 WHERE id = $1 AND user_id = $2 AND delivery_status = 'sent'`
	if status == domain.DeliveryRead {
		sql = `UPDATE alert_recipients SET delivery_status = 'read', read_at = now(), delivered_at = COALESCE(delivered_at, now())
			 WHERE id = $1 AND user_id = $2 AND delivery_status IN ('sent', 'delivered')`
	}
	tag, err := s.pool.Exec(ctx, sql, recipientID, userID)
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alert_recipients WHERE id = $1 AND user_id = $2)`,
		recipientID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

// AlertUsers lists the people who received the alert, except one.
func (s *Store) AlertUsers(ctx context.Context, alertID uuid.UUID, except uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT user_id FROM alert_recipients WHERE alert_id = $1 AND user_id <> $2`, alertID, except)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// --- Push outbox (UC116) -----------------------------------------------------

// ClaimPushJobs takes up to limit pending push notifications that are due.
// Each one is leased for 2 minutes: if the API stops while sending, another
// instance picks it up afterwards.
func (s *Store) ClaimPushJobs(ctx context.Context, now time.Time, limit int) ([]domain.PushJob, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE alert_recipients r SET next_attempt_at = $1::timestamptz + interval '2 minutes', attempts = r.attempts + 1
		 WHERE r.id IN (SELECT id FROM alert_recipients
		                 WHERE channel = 'push' AND delivery_status = 'pending'
		                   AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		                 ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING r.id, r.alert_id, r.user_id, r.attempts`, now, limit)
	if err != nil {
		return nil, err
	}
	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PushJob, error) {
		var j domain.PushJob
		err := row.Scan(&j.RecipientID, &j.AlertID, &j.UserID, &j.Attempts)
		return j, err
	})
	if err != nil || len(jobs) == 0 {
		return jobs, err
	}
	alertIDs, userIDs := make([]uuid.UUID, len(jobs)), make([]uuid.UUID, len(jobs))
	for i, j := range jobs {
		alertIDs[i], userIDs[i] = j.AlertID, j.UserID
	}
	type info struct {
		typ, severity, title, message, status string
		patientUser                           uuid.UUID
		patientName                           string
	}
	infos := map[uuid.UUID]info{}
	rows, err = s.pool.Query(ctx, `
		SELECT a.id, a.alert_type, a.severity, a.title, a.message, a.ack_status, p.user_id, u.full_name
		  FROM alerts a
		  LEFT JOIN glucose_readings gr ON gr.id = a.source_reading_id
		  LEFT JOIN glucose_predictions gp ON gp.id = a.source_prediction_id
		  LEFT JOIN devices dv ON dv.id = a.device_id
		  JOIN patients p ON p.id = `+alertPatient+`
		  JOIN users u ON u.id = p.user_id
		 WHERE a.id = ANY($1)`, alertIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var i info
		if err := rows.Scan(&id, &i.typ, &i.severity, &i.title, &i.message, &i.status, &i.patientUser, &i.patientName); err != nil {
			rows.Close()
			return nil, err
		}
		infos[id] = i
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tokens, err := s.ActiveTokens(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	for k, j := range jobs {
		i := infos[j.AlertID]
		jobs[k].AlertType, jobs[k].Severity, jobs[k].Title, jobs[k].Message, jobs[k].AckStatus = i.typ, i.severity, i.title, i.message, i.status
		jobs[k].PatientUserID, jobs[k].PatientName = i.patientUser, i.patientName
		jobs[k].Tokens = tokens[j.UserID]
	}
	return jobs, nil
}

// PushSent records that FCM accepted the notification.
func (s *Store) PushSent(ctx context.Context, recipientID uuid.UUID, providerID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_recipients SET delivery_status = 'sent', sent_at = now(), provider_message_id = NULLIF($2, ''),
		       last_error = NULL, next_attempt_at = NULL
		 WHERE id = $1 AND delivery_status = 'pending'`, recipientID, providerID)
	return err
}

// PushRetry leaves the notification pending until retryAt.
func (s *Store) PushRetry(ctx context.Context, recipientID uuid.UUID, retryAt time.Time, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_recipients SET next_attempt_at = $2, last_error = left($3, 255)
		 WHERE id = $1 AND delivery_status = 'pending'`, recipientID, retryAt, reason)
	return err
}

// PushFailed gives up on the notification. A critical alert that could not
// reach someone is escalated at once (UC116 A1).
func (s *Store) PushFailed(ctx context.Context, recipientID uuid.UUID, reason string) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var alertID uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE alert_recipients SET delivery_status = 'failed', last_error = left($2, 255), next_attempt_at = NULL
			 WHERE id = $1 AND delivery_status = 'pending' RETURNING alert_id`, recipientID, reason).Scan(&alertID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE alerts SET next_escalation_at = now()
			 WHERE id = $1 AND next_escalation_at > now()`, alertID)
		return err
	})
}

// --- Push tokens (UC008, UC009) ---------------------------------------------

// RegisterPushToken stores the token for the user. A known token is returned
// (created is false) and moves to the user if another user of the phone had it.
func (s *Store) RegisterPushToken(ctx context.Context, userID uuid.UUID, platform, token string) (domain.PushToken, bool, error) {
	var t domain.PushToken
	var inserted bool
	err := s.pool.QueryRow(ctx, `
		INSERT INTO notification_tokens (user_id, platform, push_token) VALUES ($1, $2, $3)
		ON CONFLICT (push_token) DO UPDATE SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, is_active = TRUE
		RETURNING id, user_id, platform, push_token, is_active, created_at, updated_at, (xmax = 0)`,
		userID, platform, token).Scan(&t.ID, &t.UserID, &t.Platform, &t.Token, &t.IsActive, &t.CreatedAt, &t.UpdatedAt, &inserted)
	return t, inserted, err
}

// UnregisterPushToken deletes one of the user's tokens (sign-out).
func (s *Store) UnregisterPushToken(ctx context.Context, userID, tokenID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notification_tokens WHERE id = $1 AND user_id = $2`, tokenID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// ActiveTokens returns the active push tokens of each user.
func (s *Store) ActiveTokens(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]domain.PushToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, platform, push_token, is_active, created_at, updated_at
		  FROM notification_tokens WHERE user_id = ANY($1) AND is_active ORDER BY updated_at DESC`, userIDs)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PushToken, error) {
		var t domain.PushToken
		err := row.Scan(&t.ID, &t.UserID, &t.Platform, &t.Token, &t.IsActive, &t.CreatedAt, &t.UpdatedAt)
		return t, err
	})
	out := map[uuid.UUID][]domain.PushToken{}
	for _, t := range list {
		out[t.UserID] = append(out[t.UserID], t)
	}
	return out, err
}

// DeactivatePushToken stops using a token that FCM no longer accepts (UC009).
func (s *Store) DeactivatePushToken(ctx context.Context, tokenID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE notification_tokens SET is_active = FALSE WHERE id = $1`, tokenID)
	return err
}

// --- Escalation (UC117) ------------------------------------------------------

// EscalateNext runs the next escalation step of one critical alert that is
// due: SMS to the caregivers first, then to the emergency contacts by
// priority, one every 3 minutes. found is false when nothing is due.
func (s *Store) EscalateNext(ctx context.Context, now time.Time) (found bool, err error) {
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var alertID, patientID uuid.UUID
		var patientName, alertType string
		var value *int
		var measuredAt time.Time
		var step int
		err := tx.QueryRow(ctx, `
			SELECT a.id, p.id, u.full_name, a.alert_type, gr.value_mg_dl, gr.measured_at, a.escalation_step
			  FROM alerts a
			  JOIN glucose_readings gr ON gr.id = a.source_reading_id
			  JOIN patients p ON p.id = gr.patient_id
			  JOIN users u ON u.id = p.user_id
			 WHERE a.next_escalation_at <= $1
			 ORDER BY a.next_escalation_at LIMIT 1
			   FOR UPDATE OF a SKIP LOCKED`, now).Scan(&alertID, &patientID, &patientName, &alertType, &value, &measuredAt, &step)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		step++
		next := now.Add(domain.EscalationInterval)
		nextAt := &next
		if step == 1 {
			body := domain.EscalationText(patientName, alertType, value, measuredAt, true)
			if _, err := tx.Exec(ctx, `
				WITH carers AS (
				    SELECT cl.caregiver_user_id AS user_id, u.phone
				      FROM caregiver_links cl
				      JOIN users u ON u.id = cl.caregiver_user_id
				     WHERE cl.patient_id = $2 AND cl.status = 'active' AND u.phone IS NOT NULL
				       AND u.deleted_at IS NULL AND u.account_status = 'active'),
				rows AS (
				    INSERT INTO alert_recipients (alert_id, user_id, channel)
				    SELECT $1, user_id, 'sms' FROM carers
				    ON CONFLICT (alert_id, user_id, channel) DO NOTHING
				    RETURNING id, user_id)
				INSERT INTO sms_messages (alert_id, alert_recipient_id, escalation_step, phone, body)
				SELECT $1, rows.id, 1, carers.phone, $3 FROM rows JOIN carers ON carers.user_id = rows.user_id`,
				alertID, patientID, body); err != nil {
				return err
			}
		} else {
			var contactID uuid.UUID
			var phone string
			err := tx.QueryRow(ctx, `
				SELECT id, phone FROM emergency_contacts
				 WHERE patient_id = $1 AND notify_by_sms
				 ORDER BY call_priority, created_at, id OFFSET $2 LIMIT 1`, patientID, step-2).Scan(&contactID, &phone)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				nextAt = nil // every contact was texted: the alert stays pending for the patient
			case err != nil:
				return err
			default:
				body := domain.EscalationText(patientName, alertType, value, measuredAt, false)
				if _, err := tx.Exec(ctx, `
					INSERT INTO sms_messages (alert_id, emergency_contact_id, escalation_step, phone, body)
					VALUES ($1, $2, $3, $4, $5)`, alertID, contactID, step, phone, body); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(ctx, `UPDATE alerts SET escalation_step = $2, next_escalation_at = $3 WHERE id = $1`,
			alertID, step, nextAt)
		return err
	})
	return found, err
}

// ClaimSMSJobs takes up to limit pending SMS that are due, leased for 2 minutes.
func (s *Store) ClaimSMSJobs(ctx context.Context, now time.Time, limit int) ([]domain.SMSJob, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE sms_messages m SET next_attempt_at = $1::timestamptz + interval '2 minutes', attempts = m.attempts + 1
		 WHERE m.id IN (SELECT id FROM sms_messages WHERE status = 'pending' AND next_attempt_at <= $1
		                 ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING m.id, m.phone, m.body, m.attempts`, now, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SMSJob, error) {
		var j domain.SMSJob
		err := row.Scan(&j.ID, &j.Phone, &j.Body, &j.Attempts)
		return j, err
	})
}

// SMSSent records that the provider accepted the SMS.
func (s *Store) SMSSent(ctx context.Context, id uuid.UUID, provider, providerID string) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var recipient *uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE sms_messages SET status = 'sent', sent_at = now(), provider = $2,
			       provider_message_id = NULLIF($3, ''), last_error = NULL
			 WHERE id = $1 AND status = 'pending' RETURNING alert_recipient_id`, id, provider, providerID).Scan(&recipient)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && recipient == nil) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE alert_recipients SET delivery_status = 'sent', sent_at = now(), provider_message_id = NULLIF($2, '')
			 WHERE id = $1 AND delivery_status = 'pending'`, *recipient, providerID)
		return err
	})
}

// SMSRetry leaves the SMS pending until retryAt.
func (s *Store) SMSRetry(ctx context.Context, id uuid.UUID, retryAt time.Time, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sms_messages SET next_attempt_at = $2, last_error = left($3, 255)
		 WHERE id = $1 AND status = 'pending'`, id, retryAt, reason)
	return err
}

// SMSFailed gives up on the SMS (UC117 A1: after 3 attempts).
func (s *Store) SMSFailed(ctx context.Context, id uuid.UUID, reason string) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var recipient *uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE sms_messages SET status = 'failed', last_error = left($2, 255)
			 WHERE id = $1 AND status = 'pending' RETURNING alert_recipient_id`, id, reason).Scan(&recipient)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && recipient == nil) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE alert_recipients SET delivery_status = 'failed' WHERE id = $1 AND delivery_status = 'pending'`, *recipient)
		return err
	})
}

// SMSStatus applies a delivery receipt from the provider (UC121).
func (s *Store) SMSStatus(ctx context.Context, providerID string, delivered bool, reason string) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var recipient *uuid.UUID
		var err error
		if delivered {
			err = tx.QueryRow(ctx, `
				UPDATE sms_messages SET status = 'delivered', delivered_at = GREATEST(now(), sent_at)
				 WHERE provider_message_id = $1 AND status = 'sent' RETURNING alert_recipient_id`, providerID).Scan(&recipient)
		} else {
			err = tx.QueryRow(ctx, `
				UPDATE sms_messages SET status = 'failed', last_error = left($2, 255)
				 WHERE provider_message_id = $1 AND status = 'sent' RETURNING alert_recipient_id`, providerID, reason).Scan(&recipient)
		}
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && recipient == nil) {
			return nil
		}
		if err != nil {
			return err
		}
		if delivered {
			_, err = tx.Exec(ctx, `
				UPDATE alert_recipients SET delivery_status = 'delivered', delivered_at = GREATEST(now(), sent_at)
				 WHERE id = $1 AND delivery_status = 'sent'`, *recipient)
		} else {
			_, err = tx.Exec(ctx, `UPDATE alert_recipients SET delivery_status = 'failed' WHERE id = $1 AND delivery_status = 'sent'`, *recipient)
		}
		return err
	})
}

// --- Sensor monitoring (UC092, UC078) ---------------------------------------

// DisconnectSilentSensors marks disconnected the active CGM sensors without
// data for 20 minutes and returns them, so each gets one alert.
func (s *Store) DisconnectSilentSensors(ctx context.Context, now time.Time) ([]domain.Device, error) {
	rows, err := s.pool.Query(ctx, `
		WITH silent AS (
		    UPDATE devices d SET connection_status = 'disconnected'
		      FROM device_types t
		     WHERE t.id = d.device_type_id AND t.code = 'cgm_sensor' AND d.deleted_at IS NULL
		       AND d.connection_status = 'active' AND d.last_synced_at < $1
		    RETURNING d.id)
		SELECT `+deviceColumns+deviceFrom+` JOIN silent ON silent.id = d.id`, now.Add(-domain.SensorSilence))
	return collectDevices(rows, err)
}

// ExpiringSensors are the CGM sensors in use that expire within 24 hours and
// were not warned about yet.
func (s *Store) ExpiringSensors(ctx context.Context, now time.Time) ([]domain.Device, error) {
	return collectDevices(s.pool.Query(ctx, `SELECT `+deviceColumns+deviceFrom+`
		 WHERE dt.code = 'cgm_sensor' AND d.deleted_at IS NULL
		   AND d.connection_status IN ('active', 'warming_up', 'disconnected')
		   AND d.sensor_expires_at > $1 AND d.sensor_expires_at <= $2
		   AND NOT EXISTS (SELECT 1 FROM alerts a WHERE a.device_id = d.id AND a.alert_type = 'sensor_expiring')`,
		now, now.Add(domain.SensorExpiryNotice)))
}

// ExpireSensors marks expired the sensors whose end of life has passed (UC078 A1).
func (s *Store) ExpireSensors(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE devices d SET connection_status = 'expired'
		  FROM device_types t
		 WHERE t.id = d.device_type_id AND t.code = 'cgm_sensor' AND d.deleted_at IS NULL
		   AND d.connection_status IN ('active', 'warming_up', 'disconnected') AND d.sensor_expires_at <= $1`, now)
	return tag.RowsAffected(), err
}
