package winkey

import (
	"encoding/base64"
	"testing"
)

// realDPID is DigitalProductId from a winslot VM (Windows 11 Pro, build
// 26300) installed by the golden image's answer file with GenericPro.
const realDPID = "pAAAAAMAAAAwMDMzMC04MDAwMC0wMDAwMC1BQTYwMwDsDAAAW1RIXVgxOS05ODg0MQAAAOwMAAAAAKjSe26JgU9tCQAAAAAA3AvLagsR73kAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAOmJ0s4="

func TestDecodeReal(t *testing.T) {
	b, err := base64.StdEncoding.DecodeString(realDPID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != GenericPro {
		t.Fatalf("Decode = %s, want %s", got, GenericPro)
	}
	if !Matches(got, "3V66T") {
		t.Fatal("Matches(decoded, PartialProductKey) = false")
	}
}

func TestRoundTrip(t *testing.T) {
	for _, k := range []string{
		GenericPro,
		"NBCDF-GHJKM-PQRTV-WXY23-46789", // N first
		"BCDFG-HJKMP-QRTVW-XY234-678N9", // N at the last position a digit can name
		"W269N-WFGWX-YVC9B-4J6C9-T83GX", // Pro GVLK
		"YTMG3-N6DKC-DKB77-7M9GH-8HVX7", // Home generic
	} {
		dpid, err := Encode(k)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(dpid)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if got != k {
			t.Errorf("round trip %s -> %s", k, got)
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	if _, err := Decode(make([]byte, 20)); err == nil {
		t.Error("short value accepted")
	}
	if _, err := Decode(make([]byte, 100)); err == nil {
		t.Error("value without the Windows 8 flag accepted")
	}
}

func TestNormalize(t *testing.T) {
	if k, err := Normalize(" vk7jg-nphtm-c97jm-9mpgt-3v66t\n"); err != nil || k != GenericPro {
		t.Errorf("Normalize = %q, %v", k, err)
	}
	for _, bad := range []string{
		"", "VK7JG-NPHTM-C97JM-9MPGT", "VK7JGNPHTMC97JM9MPGT3V66T",
		"VK7JG-NPHTM-C97JM-9MPGT-3V66A", // A is not in the alphabet
		"NK7JG-NPHTM-C97JM-9MPGT-3V66T", // two Ns
	} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) accepted", bad)
		}
	}
}

func TestMatches(t *testing.T) {
	if Matches(GenericPro, "") || Matches(GenericPro, "ABCDE") || !Matches(GenericPro, "3v66t") {
		t.Error("Matches wrong")
	}
}
