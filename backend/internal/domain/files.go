package domain

import (
	"time"

	"github.com/google/uuid"
)

// What a stored file is for (stored_files.purpose).
const (
	FileMealPhoto = "meal_photo"
	FileReportPDF = "report_pdf"
)

// StoredFile is a meal photo or a report PDF kept in the database (ADR 0007).
type StoredFile struct {
	ID          uuid.UUID
	PatientID   uuid.UUID
	Purpose     string
	ContentType string
	Data        []byte
	CreatedAt   time.Time
}

// FileLinkLifetime is how long a signed file link works (UC148).
const FileLinkLifetime = 15 * time.Minute
