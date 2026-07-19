package mux

import "errors"

var (
	ErrRunnerNotFound       = errors.New("runner not found")
	ErrRunnerAlreadyExists  = errors.New("runner already exists")
	ErrRunnerAlreadyRunning = errors.New("runner is already running")
	ErrPermissionDenied     = errors.New("permission denied")
	ErrInvalidTransition    = errors.New("invalid state transition")
	ErrRegistrationFailed   = errors.New("registration failed")
)
