package grpcserver

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errConflict = errors.New("aggregate concurrently modified")
	errOther    = errors.New("other")
)

func TestConcurrentModificationStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		wantOK bool
	}{
		{name: "sentinel", err: errConflict, wantOK: true},
		{name: "wrapped sentinel", err: fmt.Errorf("saving: %w", errConflict), wantOK: true},
		{name: "other error", err: errOther},
		{name: "nil", err: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := ConcurrentModificationStatus(tt.err, errConflict)
			if ok := st != nil; ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}

			if st == nil {
				return
			}

			if status.Code(st) != codes.Aborted {
				t.Errorf("code = %v, want Aborted", status.Code(st))
			}

			if status.Convert(st).Message() != ConcurrentModificationMessage {
				t.Errorf("message = %q", status.Convert(st).Message())
			}
		})
	}
}
