package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// byteSize is a flag value for sizes like 80G, 100GiB, 4096M (binary units).
type byteSize uint64

var sizeUnits = []struct {
	suffix string
	shift  uint
}{{"TIB", 40}, {"GIB", 30}, {"MIB", 20}, {"KIB", 10}, {"T", 40}, {"G", 30}, {"M", 20}, {"K", 10}, {"B", 0}}

func parseSize(s string) (uint64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	shift := uint(0)
	for _, u := range sizeUnits {
		if strings.HasSuffix(t, u.suffix) {
			t, shift = strings.TrimSpace(strings.TrimSuffix(t, u.suffix)), u.shift
			break
		}
	}
	n, err := strconv.ParseUint(t, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("invalid size %q (examples: 80G, 100GiB, 4096M)", s)
	}
	if n > (^uint64(0))>>shift {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	return n << shift, nil
}

func (b *byteSize) Set(s string) error {
	n, err := parseSize(s)
	if err != nil {
		return err
	}
	*b = byteSize(n)
	return nil
}

func (b *byteSize) String() string {
	n := uint64(*b)
	for _, u := range []struct {
		s     string
		shift uint
	}{{"T", 40}, {"G", 30}, {"M", 20}, {"K", 10}} {
		if n != 0 && n%(1<<u.shift) == 0 {
			return strconv.FormatUint(n>>u.shift, 10) + u.s
		}
	}
	return strconv.FormatUint(n, 10)
}

func (b *byteSize) Type() string { return "size" }
