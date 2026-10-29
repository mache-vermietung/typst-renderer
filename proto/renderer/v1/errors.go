// Package rendererv1 contains the generated renderer gRPC API and its error reason helpers.
package rendererv1

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorInfoDomain is the google.rpc.ErrorInfo domain of the renderer's error reasons.
const ErrorInfoDomain = "renderer.domio"

// NewStatusError returns a gRPC status error with the given code and message that carries reason
// as google.rpc.ErrorInfo. If the detail cannot be attached, the plain status is returned.
func NewStatusError(code codes.Code, reason ErrorReason, msg string) error {
	st := status.New(code, msg)

	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason.String(), Domain: ErrorInfoDomain})
	if err != nil {
		return st.Err()
	}

	return withDetails.Err()
}

// ReasonOf returns the renderer ErrorReason attached to st, or ERROR_REASON_UNSPECIFIED.
func ReasonOf(st *status.Status) ErrorReason {
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.GetDomain() != ErrorInfoDomain {
			continue
		}

		if reason, ok := ErrorReason_value[info.GetReason()]; ok {
			return ErrorReason(reason)
		}
	}

	return ErrorReason_ERROR_REASON_UNSPECIFIED
}
