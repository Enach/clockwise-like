package storage

import (
	"github.com/Enach/paceday/backend/internal/secretbox"
	"github.com/google/uuid"
)

// PAC-84: personal calendar secrets (S4 credentials_json, S5 url) are bound
// to the row id (contract 3.3). A row whose secret cannot be decrypted is
// still listed and fetched, with that field "" (the url is then omitted from
// responses, contract 9.3) and the failure in SecretErr.

const personalCalendarCols = `id, user_id, provider, name, url, credentials_json, enabled, last_synced_at, created_at`

type rowScanner interface{ Scan(...any) error }

func scanPersonalCalendar(row rowScanner) (*PersonalCalendar, error) {
	var c PersonalCalendar
	var userID uuid.NullUUID
	var url, creds string
	if err := row.Scan(&c.ID, &userID, &c.Provider, &c.Name, &url, &creds, &c.Enabled, &c.LastSyncedAt, &c.CreatedAt); err != nil {
		return nil, err
	}
	c.UserID = userID.UUID
	var err error
	if c.URL, err = openSecret(bindPersonal("url", c.ID), url, c.UserID); err != nil {
		c.SecretErr = err
	}
	if c.CredentialsJSON, err = openSecret(bindPersonal("credentials_json", c.ID), creds, c.UserID); err != nil && c.SecretErr == nil {
		c.SecretErr = err
	}
	return &c, nil
}

func sealPersonal(id int64, url, creds string) (string, string, error) {
	u, err := secretbox.Seal(bindPersonal("url", id), url)
	if err != nil {
		return "", "", err
	}
	c, err := secretbox.Seal(bindPersonal("credentials_json", id), creds)
	if err != nil {
		return "", "", err
	}
	return u, c, nil
}
