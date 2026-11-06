package grpcserver

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConcurrentModificationMessage is the client-facing message of an Aborted status caused by an
// optimistic-lock conflict. It deliberately carries no aggregate IDs or versions.
const ConcurrentModificationMessage = "concurrent_modification"

// ConcurrentModificationStatus maps err to a codes.Aborted status if it matches one of the
// given conflict sentinels (errors.Is). It returns nil for every other error, so callers can
// fall through to their own mapping.
func ConcurrentModificationStatus(err error, sentinels ...error) error {
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			return status.Error(codes.Aborted, ConcurrentModificationMessage)
		}
	}

	return nil
}
