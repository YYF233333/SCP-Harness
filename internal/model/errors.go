package model

import "fmt"

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string                   { return e.Code + ": " + e.Message }
func Err(code, format string, args ...any) error { return &Error{code, fmt.Sprintf(format, args...)} }
func Code(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return "INTERNAL_ERROR"
}
func Exit(code string) int {
	switch code {
	case "INTERNAL_ERROR":
		return 1
	case "USAGE_ERROR", "INVALID_CONFIG", "INVALID_JSON", "SCHEMA_INVALID":
		return 2
	case "AMBIGUOUS_ID", "NOT_FOUND", "CAPABILITY_DENIED", "PRECONDITION_FAILED", "INVALID_STATE", "INSUFFICIENT_RESOURCE", "LIMIT_EXCEEDED", "PRECONDITION_CHANGED":
		return 3
	case "WORKER_UNAVAILABLE", "RUNNER_UNAVAILABLE", "REPOSITORY_UNAVAILABLE", "BLOCKED":
		return 4
	case "STORAGE_FAILURE", "CORE_INCONSISTENT":
		return 5
	}
	return 1
}
