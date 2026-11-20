package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

const deviceColumns = `
	d.id, d.patient_id, dt.code, d.brand_model, d.serial_or_mac, d.connection_status, d.battery_percent,
	d.first_reading_at, d.sensor_expires_at, d.last_synced_at, d.created_at, d.updated_at`

const deviceFrom = ` FROM devices d JOIN device_types dt ON dt.id = d.device_type_id`

func scanDevice(row pgx.Row) (domain.Device, error) {
	var d domain.Device
	err := row.Scan(&d.ID, &d.PatientID, &d.DeviceType, &d.BrandModel, &d.SerialOrMAC, &d.ConnectionStatus,
		&d.BatteryPercent, &d.FirstReadingAt, &d.SensorExpiresAt, &d.LastSyncedAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Device{}, domain.ErrNotFound
	}
	return d, err
}

func collectDevices(rows pgx.Rows, err error) ([]domain.Device, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Device, error) { return scanDevice(row) })
}

// ListDevices lists the patient's devices that were not removed (UC077).
func (s *Store) ListDevices(ctx context.Context, patientID uuid.UUID) ([]domain.Device, error) {
	return collectDevices(s.pool.Query(ctx, `SELECT `+deviceColumns+deviceFrom+`
		 WHERE d.patient_id = $1 AND d.deleted_at IS NULL ORDER BY d.created_at, d.id`, patientID))
}

// GetDevice reads one of the patient's live devices.
func (s *Store) GetDevice(ctx context.Context, patientID, deviceID uuid.UUID) (domain.Device, error) {
	return scanDevice(s.pool.QueryRow(ctx, `SELECT `+deviceColumns+deviceFrom+`
		 WHERE d.id = $1 AND d.patient_id = $2 AND d.deleted_at IS NULL`, deviceID, patientID))
}

// lockPatient serialises the changes to one patient's devices and alerts.
func lockPatient(ctx context.Context, tx pgx.Tx, patientID uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM patients WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, patientID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrPatientProfileNotFound
	}
	return err
}

// RegisterDevice stores a new device (UC076). A live device with the same
// serial is returned as it is (created is false). A CGM sensor starts
// warming_up, and the patient can have only one CGM sensor in use.
func (s *Store) RegisterDevice(ctx context.Context, d domain.Device) (domain.Device, bool, error) {
	var out domain.Device
	created := false
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockPatient(ctx, tx, d.PatientID); err != nil {
			return err
		}
		if d.SerialOrMAC != nil {
			existing, err := scanDevice(tx.QueryRow(ctx, `SELECT `+deviceColumns+deviceFrom+`
				 WHERE d.patient_id = $1 AND d.serial_or_mac = $2 AND d.deleted_at IS NULL`, d.PatientID, *d.SerialOrMAC))
			if err == nil {
				out = existing
				return nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		}
		if d.DeviceType == domain.DeviceCGM {
			var inUse bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1`+deviceFrom+`
				 WHERE d.patient_id = $1 AND d.deleted_at IS NULL AND dt.code = 'cgm_sensor'
				   AND d.connection_status IN ('active', 'warming_up', 'disconnected'))`, d.PatientID).Scan(&inUse); err != nil {
				return err
			}
			if inUse {
				return domain.ErrActiveSensorExists
			}
		}
		var err error
		out, err = insertDevice(ctx, tx, d)
		created = err == nil
		return err
	})
	return out, created, err
}

func insertDevice(ctx context.Context, tx pgx.Tx, d domain.Device) (domain.Device, error) {
	status := domain.DeviceActive
	if d.DeviceType == domain.DeviceCGM {
		status = domain.DeviceWarmingUp
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO devices (patient_id, device_type_id, brand_model, serial_or_mac, connection_status,
		                     battery_percent, sensor_expires_at)
		SELECT $1, dt.id, $3, $4, $5, $6, $7 FROM device_types dt WHERE dt.code = $2 AND dt.is_active
		RETURNING id`,
		d.PatientID, d.DeviceType, d.BrandModel, d.SerialOrMAC, status, d.BatteryPercent, d.SensorExpiresAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Device{}, domain.Invalid("device_type", "this device type is not available")
	}
	if database.IsUniqueViolation(err, "uq_devices_patient_serial") {
		return domain.Device{}, domain.Invalid("serial_or_mac", "another device of the patient has this serial")
	}
	if err != nil {
		return domain.Device{}, err
	}
	return scanDevice(tx.QueryRow(ctx, `SELECT `+deviceColumns+deviceFrom+` WHERE d.id = $1`, id))
}

// ReplaceSensor marks a CGM sensor replaced and pairs the new one, in one
// transaction (UC079). The old readings stay with the old sensor.
func (s *Store) ReplaceSensor(ctx context.Context, patientID, oldID uuid.UUID, d domain.Device) (domain.Device, error) {
	var out domain.Device
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockPatient(ctx, tx, patientID); err != nil {
			return err
		}
		old, err := scanDevice(tx.QueryRow(ctx, `SELECT `+deviceColumns+deviceFrom+`
			 WHERE d.id = $1 AND d.patient_id = $2 AND d.deleted_at IS NULL`, oldID, patientID))
		if err != nil {
			return err
		}
		switch {
		case old.DeviceType != domain.DeviceCGM:
			return domain.InvalidState("Only CGM sensors can be replaced.")
		case old.ConnectionStatus == domain.DeviceReplaced:
			return domain.InvalidState("This sensor was already replaced.")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE devices SET connection_status = 'replaced', nightscout_secret_hash = NULL WHERE id = $1`, oldID); err != nil {
			return err
		}
		d.PatientID = patientID
		out, err = insertDevice(ctx, tx, d)
		return err
	})
	return out, err
}

// UpdateDeviceStatus locks one of the patient's devices, lets apply change it
// and saves the battery, the status and the sync times (UC078).
func (s *Store) UpdateDeviceStatus(ctx context.Context, patientID, deviceID uuid.UUID, apply func(*domain.Device) error) (domain.Device, error) {
	var d domain.Device
	err := database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		d, err = scanDevice(tx.QueryRow(ctx, `SELECT `+deviceColumns+deviceFrom+`
			 WHERE d.id = $1 AND d.patient_id = $2 AND d.deleted_at IS NULL FOR UPDATE OF d`, deviceID, patientID))
		if err != nil {
			return err
		}
		if err := apply(&d); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			UPDATE devices SET connection_status = $2, battery_percent = $3, first_reading_at = $4, last_synced_at = $5
			 WHERE id = $1 RETURNING updated_at`,
			d.ID, d.ConnectionStatus, d.BatteryPercent, d.FirstReadingAt, d.LastSyncedAt).Scan(&d.UpdatedAt)
	})
	return d, err
}

// RemoveDevice removes a device logically (UC080). Its readings stay.
func (s *Store) RemoveDevice(ctx context.Context, patientID, deviceID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE devices SET deleted_at = now(), connection_status = 'disconnected', nightscout_secret_hash = NULL
		 WHERE id = $1 AND patient_id = $2 AND deleted_at IS NULL`, deviceID, patientID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

// SetNightscoutSecret stores the hash of a new upload secret for a CGM sensor.
func (s *Store) SetNightscoutSecret(ctx context.Context, patientID, deviceID uuid.UUID, hash []byte) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		d, err := scanDevice(tx.QueryRow(ctx, `SELECT `+deviceColumns+deviceFrom+`
			 WHERE d.id = $1 AND d.patient_id = $2 AND d.deleted_at IS NULL FOR UPDATE OF d`, deviceID, patientID))
		if err != nil {
			return err
		}
		if d.DeviceType != domain.DeviceCGM || !d.InUse() {
			return domain.InvalidState("Only a CGM sensor in use can receive Nightscout uploads.")
		}
		_, err = tx.Exec(ctx, `UPDATE devices SET nightscout_secret_hash = $2 WHERE id = $1`, deviceID, hash)
		return err
	})
}

// DeviceByNightscoutSecret finds the live sensor of an upload secret, of a
// live patient with an active account.
func (s *Store) DeviceByNightscoutSecret(ctx context.Context, hash []byte) (domain.Device, error) {
	return scanDevice(s.pool.QueryRow(ctx, `SELECT `+deviceColumns+deviceFrom+`
		  JOIN patients p ON p.id = d.patient_id AND p.deleted_at IS NULL
		  JOIN users u ON u.id = p.user_id AND u.deleted_at IS NULL AND u.account_status = 'active'
		 WHERE d.nightscout_secret_hash = $1 AND d.deleted_at IS NULL`, hash))
}

// SimulatorDevices are the CGM sensors in use that the simulator feeds.
func (s *Store) SimulatorDevices(ctx context.Context) ([]domain.Device, error) {
	return collectDevices(s.pool.Query(ctx, `SELECT `+deviceColumns+deviceFrom+`
		  JOIN patients p ON p.id = d.patient_id AND p.deleted_at IS NULL
		 WHERE d.brand_model = $1 AND dt.code = 'cgm_sensor' AND d.deleted_at IS NULL
		   AND d.connection_status IN ('active', 'warming_up', 'disconnected')
		 ORDER BY d.created_at`, domain.SimulatorBrandModel))
}
