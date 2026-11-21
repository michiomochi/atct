package store

import "testing"

func TestHandoffIsDelegation(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		requestedBy, receivedBy int64
		want                    bool
	}{
		{name: "self claim", requestedBy: 1, receivedBy: 1, want: false},
		{name: "delegation", requestedBy: 1, receivedBy: 2, want: true},
		{name: "unknown requester", requestedBy: 0, receivedBy: 2, want: true},
		{name: "unknown receiver", requestedBy: 1, receivedBy: 0, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := handoffIsDelegation(tc.requestedBy, tc.receivedBy); got != tc.want {
				t.Fatalf("handoffIsDelegation(%d, %d) = %t, want %t", tc.requestedBy, tc.receivedBy, got, tc.want)
			}
		})
	}
}
