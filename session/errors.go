package session

import "errors"

var (
	// The ErrSessionNotFound session does not exist
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionAlreadyExists session already exists
	ErrSessionAlreadyExists = errors.New("session already exists")

	// ErrInvalidSessionKey Invalid session key
	ErrInvalidSessionKey = errors.New("invalid session key")

	// ErrStorageClosed Storage is closed
	ErrStorageClosed = errors.New("storage closed")
)
