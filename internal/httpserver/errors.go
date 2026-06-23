package httpserver

import "net/http"

type AppError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e AppError) Error() string {
	return e.Message
}

type NotFoundError struct {
	AppError
}

type ConflictError struct {
	AppError
}

type UnauthorizedError struct {
	AppError
}

type ValidationError struct {
	AppError
}

func NewNotFoundError(code string, message string) NotFoundError {
	return NotFoundError{AppError{StatusCode: http.StatusNotFound, Code: code, Message: message}}
}

func NewConflictError(code string, message string) ConflictError {
	return ConflictError{AppError{StatusCode: http.StatusConflict, Code: code, Message: message}}
}

func NewUnauthorizedError(code string, message string) UnauthorizedError {
	return UnauthorizedError{AppError{StatusCode: http.StatusUnauthorized, Code: code, Message: message}}
}

func NewValidationError(code string, message string) ValidationError {
	return ValidationError{AppError{StatusCode: http.StatusBadRequest, Code: code, Message: message}}
}

func InternalError(code string, message string) AppError {
	return AppError{StatusCode: http.StatusInternalServerError, Code: code, Message: message}
}
