package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDescribeVersion(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{
			name: "no build info at all",
			ok:   false,
			want: "dev",
		},
		{
			name: "installed from a tag",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.0.0"}},
			ok:   true,
			want: "v1.0.0",
		},
		{
			name: "built locally from an untagged commit",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260907165246-13f96cca1e99"}},
			ok:   true,
			want: "v0.0.0-20260907165246-13f96cca1e99",
		},
		{
			name: "built locally with uncommitted changes",
			info: &debug.BuildInfo{Main: debug.Module{Version: "v1.0.0+dirty"}},
			ok:   true,
			want: "v1.0.0+dirty",
		},
		{
			name: "no version information",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			ok:   true,
			want: "dev",
		},
		{
			name: "empty version",
			info: &debug.BuildInfo{},
			ok:   true,
			want: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, describeVersion(tt.info, tt.ok))
		})
	}
}
