package customhandler

import "log/slog"

// discardLogger returns a logger that drops all records, keeping test output
// clean when exercising panic/error paths that log diagnostics.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
