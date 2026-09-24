package httpapi

import "testing"

func TestFormatSourceSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0MB"},
		{512 * 1024, "0.50MB"},
		{70_557_696, "67MB"},
		{168_543_751, "160MB"},
		{10 * 1024 * 1024, "10MB"},
		{1 << 30, "1.0GB"},
		{1<<30 + 1<<29, "1.5GB"},
		{5 << 30, "5.0GB"},
	}
	for _, c := range cases {
		if got := formatSourceSize(c.n); got != c.want {
			t.Errorf("formatSourceSize(%d)=%q want %q", c.n, got, c.want)
		}
	}
}
