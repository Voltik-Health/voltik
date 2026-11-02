package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/voltik/voltik/backend/internal/database"
	"github.com/voltik/voltik/backend/internal/domain"
)

// LegalDocuments lists every published version of every legal document.
func (s *Store) LegalDocuments(ctx context.Context) ([]domain.LegalDocument, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, document_type, version, is_mandatory, published_at
		  FROM legal_documents
		 ORDER BY document_type, published_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LegalDocument, error) {
		var d domain.LegalDocument
		err := row.Scan(&d.ID, &d.Type, &d.Version, &d.IsMandatory, &d.PublishedAt)
		return d, err
	})
}

// ListConsents lists the user's consents, newest first, revoked ones included.
func (s *Store) ListConsents(ctx context.Context, userID uuid.UUID) ([]domain.Consent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.document_type, d.version, c.created_at, c.revoked_at
		  FROM consents c
		  JOIN legal_documents d ON d.id = c.document_id
		 WHERE c.user_id = $1
		 ORDER BY c.created_at DESC, d.document_type`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Consent, error) {
		var c domain.Consent
		err := row.Scan(&c.DocumentType, &c.Version, &c.CreatedAt, &c.RevokedAt)
		return c, err
	})
}

// AcceptConsent records the acceptance of a document version and its audit
// row. created is false when the version was already accepted and active.
func (s *Store) AcceptConsent(ctx context.Context, userID uuid.UUID, patientID *uuid.UUID, doc domain.LegalDocument, meta domain.RequestMeta) (c domain.Consent, created bool, err error) {
	err = database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		c, created, err = acceptConsent(ctx, tx, userID, patientID, doc, meta)
		return err
	})
	return c, created, err
}

// acceptConsent inserts the consent, or activates it again if it was revoked
// (SRS UC012: "aceitar de novo limpa revoked_at"). An active consent is left
// untouched and gets no audit row.
func acceptConsent(ctx context.Context, tx pgx.Tx, userID uuid.UUID, patientID *uuid.UUID, doc domain.LegalDocument, meta domain.RequestMeta) (domain.Consent, bool, error) {
	c := domain.Consent{DocumentType: doc.Type, Version: doc.Version}
	err := tx.QueryRow(ctx, `
		INSERT INTO consents (user_id, document_id, ip_address) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, document_id) DO UPDATE
		   SET revoked_at = NULL, created_at = now(), ip_address = EXCLUDED.ip_address
		 WHERE consents.revoked_at IS NOT NULL
		RETURNING created_at, revoked_at`,
		userID, doc.ID, ipOrNil(meta.IP)).Scan(&c.CreatedAt, &c.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already accepted and active: nothing changes
		err = tx.QueryRow(ctx, `SELECT created_at, revoked_at FROM consents WHERE user_id = $1 AND document_id = $2`,
			userID, doc.ID).Scan(&c.CreatedAt, &c.RevokedAt)
		return c, false, err
	}
	if err != nil {
		return c, false, err
	}
	err = insertAudit(ctx, tx, audit{actor: &userID, patient: patientID, operation: domain.AuditConsentGranted, meta: meta})
	return c, true, err
}

// ActiveConsents lists the user's active consents of one document type.
func (s *Store) ActiveConsents(ctx context.Context, userID uuid.UUID, docType string) ([]domain.LegalDocument, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.document_type, d.version, d.is_mandatory, d.published_at
		  FROM consents c
		  JOIN legal_documents d ON d.id = c.document_id
		 WHERE c.user_id = $1 AND d.document_type = $2 AND c.revoked_at IS NULL`, userID, docType)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LegalDocument, error) {
		var d domain.LegalDocument
		err := row.Scan(&d.ID, &d.Type, &d.Version, &d.IsMandatory, &d.PublishedAt)
		return d, err
	})
}

// RevokeConsents revokes the user's active consents of one document type and
// writes the audit row. It returns domain.ErrNotFound when none was active.
func (s *Store) RevokeConsents(ctx context.Context, userID uuid.UUID, patientID *uuid.UUID, docType string, meta domain.RequestMeta) error {
	return database.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE consents c SET revoked_at = now()
			  FROM legal_documents d
			 WHERE d.id = c.document_id AND c.user_id = $1 AND d.document_type = $2 AND c.revoked_at IS NULL`,
			userID, docType)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return insertAudit(ctx, tx, audit{actor: &userID, patient: patientID, operation: domain.AuditConsentRevoked, meta: meta})
	})
}
