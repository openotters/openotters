// White-box test: qualifyForEmbeddedRegistry is unexported and the
// pure half of localRef. Testing it from internal_test would require
// promoting it to the public API for one assertion's sake.
//
//nolint:testpackage // intentional white-box access to an unexported helper
package internal

import "testing"

func TestQualifyForEmbeddedRegistry(t *testing.T) {
	t.Parallel()

	const addr = "127.0.0.1:5527"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare name with tag",
			in:   "foo:v1",
			want: "127.0.0.1:5527/foo:v1",
		},
		{
			name: "bare path with tag",
			in:   "agents/foo:v1",
			want: "127.0.0.1:5527/agents/foo:v1",
		},
		{
			name: "already prefixed with embedded addr",
			in:   "127.0.0.1:5527/foo:v1",
			want: "127.0.0.1:5527/foo:v1",
		},
		{
			name: "remote ref gets mirrored under embedded addr",
			in:   "ghcr.io/openotters/agents/base:latest",
			want: "127.0.0.1:5527/ghcr.io/openotters/agents/base:latest",
		},
		{
			name: "foreign loopback registry is mirrored, not skipped",
			in:   "localhost:5000/foo:v1",
			want: "127.0.0.1:5527/localhost:5000/foo:v1",
		},
		{
			name: "missing tag defaults to latest",
			in:   "foo",
			want: "127.0.0.1:5527/foo:latest",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := qualifyForEmbeddedRegistry(tc.in, addr).String()
			if got != tc.want {
				t.Fatalf("qualifyForEmbeddedRegistry(%q, %q) = %q, want %q",
					tc.in, addr, got, tc.want)
			}
		})
	}
}
