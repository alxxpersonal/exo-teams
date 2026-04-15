package api

import "testing"

// --- Submission Resource Helpers ---

func TestEncodeSharingURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "simple sharepoint url",
			in:   "https://tenant.sharepoint.com/sites/foo/Shared%20Documents/bar",
			want: "u!aHR0cHM6Ly90ZW5hbnQuc2hhcmVwb2ludC5jb20vc2l0ZXMvZm9vL1NoYXJlZCUyMERvY3VtZW50cy9iYXI",
		},
		{
			name: "padding trimmed",
			in:   "a",
			want: "u!YQ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := encodeSharingURL(tc.in)
			if got != tc.want {
				t.Errorf("encodeSharingURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractDriveIDFromGraphURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "full drives items path",
			in:   "https://graph.microsoft.com/v1.0/drives/b!abc123/items/01XYZ",
			want: "b!abc123",
		},
		{
			name: "drives with nothing after id",
			in:   "https://graph.microsoft.com/v1.0/drives/b!abc123",
			want: "b!abc123",
		},
		{
			name: "no drives segment",
			in:   "https://graph.microsoft.com/v1.0/sites/foo",
			want: "",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDriveIDFromGraphURL(tc.in)
			if got != tc.want {
				t.Errorf("extractDriveIDFromGraphURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
