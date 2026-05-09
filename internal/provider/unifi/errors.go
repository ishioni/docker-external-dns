package unifi

import "fmt"

const errorBodyLimit = 512

type APIError struct {
	Operation  string
	URL        string
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("unifi API error during %s %s: HTTP %d: %s", e.Operation, e.URL, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("unifi API error during %s %s: HTTP %d: %s", e.Operation, e.URL, e.StatusCode, e.Body)
}

type NetworkError struct {
	Operation string
	URL       string
	Err       error
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("unifi network error during %s %s: %v", e.Operation, e.URL, e.Err)
}

func (e *NetworkError) Unwrap() error {
	return e.Err
}

type DataError struct {
	Operation string
	DataType  string
	Err       error
}

func (e *DataError) Error() string {
	return fmt.Sprintf("unifi data error during %s of %s: %v", e.Operation, e.DataType, e.Err)
}

func (e *DataError) Unwrap() error {
	return e.Err
}

type errorResponse struct {
	Code      string         `json:"code"`
	Details   map[string]any `json:"details"`
	ErrorCode int            `json:"errorCode"`
	Message   string         `json:"message"`
}
