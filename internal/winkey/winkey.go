// Package winkey handles Windows product keys: their format, and recovering
// an installed key from the DigitalProductId registry value.
//
// Windows' licensing service only ever reports the last five characters of
// the installed key (PartialProductKey). The full key is encoded, not
// encrypted, in HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion
// \DigitalProductId: 15 bytes at offset 52 hold a base-24 number over the
// key alphabet, with the position of the key's single "N" folded in (the
// Windows 8+ scheme). It is absent once someone runs slmgr /cpky.
//
// A decoded key is only trusted when its last five characters equal the
// PartialProductKey the licensing service reports: see Matches.
package winkey

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// GenericPro is Microsoft's published installation key for Windows 11 Pro.
// It selects the edition and never activates; a VM that is activated while
// it is installed holds a digital license (hardware-ID based), not a key.
const GenericPro = "VK7JG-NPHTM-C97JM-9MPGT-3V66T"

// alphabet is the product key alphabet, in digit order.
const alphabet = "BCDFGHJKMPQRTVWXY2346789"

const (
	keyOffset = 52 // first byte of the encoded key in DigitalProductId
	keyLen    = 15
)

var keyRE = regexp.MustCompile(`^[` + alphabet + `N]{5}(-[` + alphabet + `N]{5}){4}$`)

// Normalize upper-cases a key and checks its format (five groups of five
// from the key alphabet, at most one N).
func Normalize(key string) (string, error) {
	k := strings.ToUpper(strings.TrimSpace(key))
	if !keyRE.MatchString(k) || strings.Count(k, "N") > 1 {
		return "", fmt.Errorf("%q is not a product key (XXXXX-XXXXX-XXXXX-XXXXX-XXXXX)", key)
	}
	return k, nil
}

// Partial is the last five characters, as the licensing service reports them.
func Partial(key string) string {
	if len(key) < 5 {
		return ""
	}
	return key[len(key)-5:]
}

// Matches reports whether key is the key Windows says is installed.
func Matches(key, partialProductKey string) bool {
	return partialProductKey != "" && strings.EqualFold(Partial(key), partialProductKey)
}

// Decode recovers the product key from a DigitalProductId value.
func Decode(dpid []byte) (string, error) {
	if len(dpid) < keyOffset+keyLen {
		return "", fmt.Errorf("DigitalProductId too short (%d bytes)", len(dpid))
	}
	b := make([]byte, keyLen)
	copy(b, dpid[keyOffset:keyOffset+keyLen])
	// The number is below 24^25 < 2^115, so the top byte is at most 6 and
	// bit 3 is free: it flags the Windows 8+ scheme. (The decoder commonly
	// copied around tests (b/6)&1, which misreads keys with the N at
	// position 16 or later.)
	if b[keyLen-1]&0x08 == 0 {
		return "", errors.New("DigitalProductId holds a pre-Windows 8 key encoding")
	}
	b[keyLen-1] &^= 0x08 // the Windows 8+ flag

	// Repeatedly divide the 120-bit little-endian number by 24.
	var digits [25]byte
	for i := 24; i >= 0; i-- {
		rem := 0
		for j := keyLen - 1; j >= 0; j-- {
			cur := rem<<8 | int(b[j])
			b[j] = byte(cur / 24)
			rem = cur % 24
		}
		digits[i] = alphabet[rem]
	}
	// The most significant digit is the position of the N in the rest.
	pos := strings.IndexByte(alphabet, digits[0])
	rest := string(digits[1:])
	if pos > len(rest) {
		return "", errors.New("DigitalProductId does not decode to a key")
	}
	k := rest[:pos] + "N" + rest[pos:]
	return k[0:5] + "-" + k[5:10] + "-" + k[10:15] + "-" + k[15:20] + "-" + k[20:25], nil
}

// Encode is Decode's inverse: a minimal DigitalProductId holding key. Tests
// use it to stand in for a VM's registry value.
func Encode(key string) ([]byte, error) {
	k := strings.ReplaceAll(key, "-", "")
	pos := strings.IndexByte(k, 'N')
	if len(k) != 25 || pos < 0 || pos >= len(alphabet) {
		return nil, fmt.Errorf("encode: need a 25-character key with one N: %q", key)
	}
	digits := string(alphabet[pos]) + k[:pos] + k[pos+1:]
	b := make([]byte, keyLen)
	for _, c := range []byte(digits) {
		carry := strings.IndexByte(alphabet, c)
		for j := 0; j < keyLen; j++ {
			cur := int(b[j])*24 + carry
			b[j] = byte(cur)
			carry = cur >> 8
		}
	}
	b[keyLen-1] |= 0x08
	dpid := make([]byte, keyOffset+keyLen+16)
	copy(dpid[keyOffset:], b)
	return dpid, nil
}
