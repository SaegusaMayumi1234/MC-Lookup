package api

type BaseResponse struct {
	Success bool `json:"success"`
}

type PlayerResponse struct {
	BaseResponse
	Data *PlayerData `json:"data,omitempty"`
}

type PlayerData struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
}

type ErrorResponse struct {
	BaseResponse
	Error ErrorResult `json:"error"`
}

type ErrorResult struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details *ErrorDetail `json:"details,omitempty"`
}

type ErrorDetail struct {
	Resolver   map[string]string `json:"resolver,omitempty"`
	StackTrace string            `json:"stack_trace,omitempty"`
}