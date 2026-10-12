package cli

import "testing"

func TestParseSize(t *testing.T) {
	for in, want := range map[string]uint64{
		"80G": 80 << 30, "80g": 80 << 30, "100GiB": 100 << 30, "4096M": 4096 << 20, "1T": 1 << 40,
		"512K": 512 << 10, "1024": 1024, "7B": 7, " 8G ": 8 << 30,
	} {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "G", "-1G", "1.5G", "0G", "eightyG", "80X", "99999999999T"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q): want error", bad)
		}
	}
}

func TestByteSizeString(t *testing.T) {
	for n, want := range map[uint64]string{80 << 30: "80G", 1536 << 20: "1536M", 1 << 40: "1T", 1000: "1000"} {
		b := byteSize(n)
		if got := b.String(); got != want {
			t.Errorf("String(%d) = %q, want %q", n, got, want)
		}
	}
}
