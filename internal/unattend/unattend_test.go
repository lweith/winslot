package unattend

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	out, err := Render(Params{ComputerName: "wiresx", TimeZone: "Eastern Standard Time", AdminPassword: `p<&>"'w`})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<ComputerName>wiresx</ComputerName>",
		"<TimeZone>Eastern Standard Time</TimeZone>",
		"<Value>p&lt;&amp;&gt;&#34;&#39;w</Value>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(s, "{{") {
		t.Error("unrendered template action")
	}
	// The result must be well-formed XML for Windows Setup.
	var doc struct{ XMLName xml.Name }
	if err := xml.Unmarshal(out, &doc); err != nil || doc.XMLName.Local != "unattend" {
		t.Errorf("not well-formed: %v", err)
	}
}

func TestRenderIncomplete(t *testing.T) {
	if _, err := Render(Params{ComputerName: "x"}); err == nil {
		t.Error("want error")
	}
}

func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := NewPassword(20)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 23 || !strings.HasSuffix(p, "-9a") || strings.ContainsAny(p[:20], "0O1lI") || seen[p] {
			t.Fatalf("bad or repeated password %q", p)
		}
		seen[p] = true
	}
}
