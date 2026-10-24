package store

import "errors"

var (
	ErrMessageIDConflict  = errors.New("message id belongs to another origin")
	ErrDuplicateMessage   = errors.New("message already exists")
	ErrDMTopologyConflict = errors.New("direct conversation topology conflict")
	ErrRoomExists         = errors.New("room already exists")
	ErrRoomNotFound       = errors.New("room not found")
	ErrInvalidCursor      = errors.New("invalid cursor")
	ErrAgentCredential    = errors.New("agent credential mismatch")
)

// DuplicateMessageError retains the stored ID when an origin retry uses a new ID.
type DuplicateMessageError struct{ MessageID string }

func (e *DuplicateMessageError) Error() string { return ErrDuplicateMessage.Error() }
func (e *DuplicateMessageError) Unwrap() error { return ErrDuplicateMessage }
